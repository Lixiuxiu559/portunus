package configtransfer

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
)

func (s *Service) Commit(ctx context.Context, op string, request CommitRequest) (any, int, error) {
	if !ValidOperationID(op) || !ValidPreviewID(request.PreviewID) || !ValidHash(request.FileSHA256) || !ValidHash(request.PlanSHA256) {
		return nil, 0, NewError("INVALID_INPUT", "input")
	}
	if !s.beginOp(op) {
		r, err := s.receipt(ctx, op)
		if err != nil {
			return nil, 0, err
		}
		if r.FileSHA256 != request.FileSHA256 || r.PlanSHA256 != request.PlanSHA256 {
			return nil, 0, withOperation(NewError("OPERATION_MISMATCH", "commit"), op)
		}
		return s.Result(ctx, op)
	}
	defer s.endOp(op)
	r, err := s.receipt(ctx, op)
	if err != nil {
		return nil, 0, err
	}
	if r.FileSHA256 != request.FileSHA256 || r.PlanSHA256 != request.PlanSHA256 {
		return nil, 0, withOperation(NewError("OPERATION_MISMATCH", "commit"), op)
	}
	if r.Status == "committed" {
		d, err := s.storedResult(ctx, r, true)
		return d, 200, err
	}
	if r.Status == "aborted" {
		return nil, 0, withOperation(NewError("OPERATION_ABORTED", "commit"), op)
	}
	ctx, release, err := s.Acquire(ctx)
	if err != nil {
		return nil, 0, withOperation(err, op)
	}
	defer release()
	s.mu.Lock()
	p := s.previews[request.PreviewID]
	valid := p != nil && p.dto.OperationID == op && p.dto.FileSHA256 == request.FileSHA256 && p.dto.PlanSHA256 == request.PlanSHA256
	expired := valid && !time.Now().Before(p.deadline)
	s.mu.Unlock()
	if !valid || expired || r.OwnerInstanceID != s.instanceID || r.ExpiresAtMs <= time.Now().UnixMilli() {
		code := "PREVIEW_EXPIRED"
		if r.OwnerInstanceID != s.instanceID {
			code = "SESSION_LOST"
		}
		if err := s.fence(ctx, op, code, "commit"); err != nil {
			return nil, 0, withOperation(err, op)
		}
		s.mu.Lock()
		if valid {
			s.releasePreviewLocked(request.PreviewID)
		}
		s.mu.Unlock()
		return nil, 0, withOperation(NewError("PREVIEW_EXPIRED", "commit"), op)
	}
	confirm := s.db.WithContext(ctx).Exec("UPDATE config_import_receipts SET confirmed_at_ms=COALESCE(confirmed_at_ms,?) WHERE operation_id=? AND status='prepared' AND owner_instance_id=? AND file_sha256=? AND plan_sha256=? AND expires_at_ms > ?", time.Now().UnixMilli(), op, s.instanceID, request.FileSHA256, request.PlanSHA256, time.Now().UnixMilli())
	if confirm.Error != nil || confirm.RowsAffected != 1 {
		return s.settle(ctx, op, p, confirm.Error, false)
	}
	var result ResultDTO
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// This conditional DML must remain the first database access in the Tx.
		claim := tx.Exec("UPDATE config_import_receipts SET claim_started_at_ms=? WHERE operation_id=? AND owner_instance_id=? AND status='prepared' AND file_sha256=? AND plan_sha256=? AND expires_at_ms > ? AND confirmed_at_ms IS NOT NULL", time.Now().UnixMilli(), op, s.instanceID, request.FileSHA256, request.PlanSHA256, time.Now().UnixMilli())
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return NewError("PREVIEW_EXPIRED", "commit")
		}
		if !time.Now().Before(p.deadline) {
			return NewError("PREVIEW_EXPIRED", "commit")
		}
		if err := checkBusinessSchema(tx); err != nil {
			return err
		}
		if err := checkPlanAvailable(tx, p.plan); err != nil {
			return err
		}
		var err error
		result, err = s.restoreTx(tx, p)
		return err
	})
	if err != nil {
		return s.settle(ctx, op, p, err, true)
	}
	s.mu.Lock()
	s.releasePreviewLocked(request.PreviewID)
	s.mu.Unlock()
	return result, 200, nil
}

// settle uses a fresh bounded context because a cancelled HTTP context proves
// neither COMMIT nor rollback. The heavy lease remains held until verification.
func (s *Service) settle(_ context.Context, op string, p *retainedPreview, cause error, confirmed bool) (any, int, error) {
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), leaseKey{}, s), 5*time.Second)
	defer cancel()
	r, err := s.receipt(ctx, op)
	if err != nil {
		return nil, 0, withOperation(NewError("RESULT_UNCONFIRMED", "recovery"), op)
	}
	if r.Status == "committed" {
		d, err := s.storedResult(ctx, r, true)
		if err == nil {
			s.mu.Lock()
			s.releasePreviewLocked(p.dto.PreviewID)
			s.mu.Unlock()
		}
		return d, 200, withOperation(err, op)
	}
	if r.Status == "aborted" {
		s.mu.Lock()
		s.releasePreviewLocked(p.dto.PreviewID)
		s.mu.Unlock()
		return s.state(r), 200, nil
	}
	code := "IMPORT_FAILED"
	var e *Error
	if errors.As(cause, &e) {
		switch e.Code {
		case "PREVIEW_CHANGED", "PREVIEW_EXPIRED", "LIMIT_EXCEEDED", "UNSUPPORTED_DATABASE_SCHEMA":
			code = e.Code
		}
	}
	if !confirmed && isBusy(cause) {
		code = "DB_BUSY"
	}
	if err := s.fence(ctx, op, code, "commit"); err != nil {
		return nil, 0, withOperation(err, op)
	}
	r, err = s.receipt(ctx, op)
	if err != nil {
		return nil, 0, withOperation(NewError("RESULT_UNCONFIRMED", "recovery"), op)
	}
	if r.Status == "committed" {
		d, err := s.storedResult(ctx, r, true)
		return d, 200, withOperation(err, op)
	}
	if r.Status != "aborted" {
		return nil, 0, withOperation(NewError("RESULT_UNCONFIRMED", "recovery"), op)
	}
	// A frozen name was occupied: end the old operation, then prepare a fresh
	// operation from the still-retained source. It requires explicit confirmation.
	if code == "PREVIEW_CHANGED" {
		s.mu.Lock()
		raw := p.raw
		payload := p.payload
		delete(s.previews, p.dto.PreviewID)
		s.retainedBytes -= len(raw)
		p.raw = nil
		p.payload = nil
		s.mu.Unlock()
		replacement, prepareErr := s.prepare(ctx, payload, raw, p.dto.FileSHA256)
		if prepareErr != nil {
			wipe(raw)
			return nil, 0, withOperation(prepareErr, op)
		}
		err := NewError("PREVIEW_CHANGED", "plan")
		err.OperationID = op
		err.ReplacementPreview = &replacement
		return nil, 0, err
	}
	s.mu.Lock()
	s.releasePreviewLocked(p.dto.PreviewID)
	s.mu.Unlock()
	return s.state(r), 200, nil
}
func checkPlanAvailable(tx *gorm.DB, plan namePlan) error {
	for _, entry := range []struct {
		table string
		names []rename
	}{{"channels", plan.Channels}, {"groups", plan.Groups}} {
		for _, name := range entry.names {
			var n int64
			if err := tx.Table(entry.table).Where("name COLLATE BINARY = ?", name.To).Count(&n).Error; err != nil {
				return err
			}
			if n != 0 {
				return NewError("PREVIEW_CHANGED", "plan")
			}
		}
	}
	return nil
}
func insertID(tx *gorm.DB, table string, values map[string]any, where map[string]any) (int64, error) {
	if err := tx.Table(table).Create(values).Error; err != nil {
		return 0, err
	}
	var row struct{ ID int64 }
	query := tx.Table(table).Select("id")
	for key, value := range where {
		if key == "name" {
			query = query.Where("name COLLATE BINARY = ?", value)
		} else {
			query = query.Where(quoteIdentifier(key)+" = ?", value)
		}
	}
	if err := query.Take(&row).Error; err != nil {
		return 0, err
	}
	if row.ID < 1 || row.ID > MaxSafeID {
		return 0, NewError("LIMIT_EXCEEDED", "commit")
	}
	return row.ID, nil
}
func (s *Service) inject(stage string) error {
	if s.fault != nil {
		return s.fault(stage)
	}
	return nil
}
func (s *Service) restoreTx(tx *gorm.DB, p *retainedPreview) (ResultDTO, error) {
	now := time.Now().UTC()
	result := ResultDTO{OperationID: p.dto.OperationID, State: "committed", CommittedAt: now.Format(time.RFC3339Nano), ResultVersion: 1, Counts: p.payload.Counts(), RenameCount: p.dto.RenameCount, Channels: []ResultName{}, Groups: []ResultName{}}
	channelIDs, modelIDs := map[string]int64{}, map[string]int64{}
	for i, c := range p.payload.Channels {
		options, err := json.Marshal(c.ConnectionOptions)
		if err != nil {
			return result, NewError("CONFIG_INVALID", "commit")
		}
		name := p.plan.Channels[i].To
		values := map[string]any{"name": name, "icon": c.Icon, "provider_id": c.ProviderID, "profile_id": c.ProfileID, "adapter_id": c.AdapterID, "type": c.Type, "base_url": c.BaseURL, "key": c.Key, "connection_options": string(options), "auto_sync": c.AutoSync, "thinking_compat": c.ThinkingCompat, "max_concurrent_requests": c.MaxConcurrentRequests, "rate_limit_scope": c.RateLimitScope, "created_at": now, "updated_at": now}
		id, err := insertID(tx, "channels", values, map[string]any{"name": name})
		if err != nil {
			return result, err
		}
		channelIDs[c.Ref] = id
		if name != c.Name {
			result.Channels = append(result.Channels, ResultName{c.Ref, strconv.FormatInt(id, 10), c.Name, name})
		}
	}
	if err := s.inject("channels"); err != nil {
		return result, err
	}
	for _, m := range p.payload.Models {
		where := map[string]any{"channel_id": channelIDs[m.ChannelRef], "name": m.Name}
		id, err := insertID(tx, "models", map[string]any{"channel_id": channelIDs[m.ChannelRef], "name": m.Name, "created_at": now, "updated_at": now}, where)
		if err != nil {
			return result, err
		}
		modelIDs[m.Ref] = id
	}
	if err := s.inject("models"); err != nil {
		return result, err
	}
	groupIDs := map[string]int64{}
	for i, g := range p.payload.Groups {
		name := p.plan.Groups[i].To
		id, err := insertID(tx, "groups", map[string]any{"name": name, "strategy": g.Strategy, "active_item_id": 0, "created_at": now, "updated_at": now}, map[string]any{"name": name})
		if err != nil {
			return result, err
		}
		groupIDs[g.Ref] = id
		result.Groups = append(result.Groups, ResultName{g.Ref, strconv.FormatInt(id, 10), g.Name, name})
	}
	if err := s.inject("groups"); err != nil {
		return result, err
	}
	for _, g := range p.payload.Groups {
		var activeID int64
		for _, item := range g.Items {
			priority, _ := strconv.ParseInt(item.Priority, 10, 64)
			values := map[string]any{"group_id": groupIDs[g.Ref], "model_id": modelIDs[item.ModelRef], "priority": priority}
			id, err := insertID(tx, "group_items", values, map[string]any{"group_id": groupIDs[g.Ref], "model_id": modelIDs[item.ModelRef]})
			if err != nil {
				return result, err
			}
			if g.ActiveItemRef != nil && *g.ActiveItemRef == item.Ref {
				activeID = id
			}
		}
		if err := tx.Table("groups").Where("id = ?", groupIDs[g.Ref]).Update("active_item_id", activeID).Error; err != nil {
			return result, err
		}
	}
	if err := s.inject("items"); err != nil {
		return result, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxResultBytes {
		return result, NewError("LIMIT_EXCEEDED", "commit")
	}
	update := tx.Table("config_import_receipts").Where("operation_id=? AND owner_instance_id=? AND status='prepared' AND claim_started_at_ms IS NOT NULL", p.dto.OperationID, s.instanceID).Updates(map[string]any{"status": "committed", "committed_at_ms": now.UnixMilli(), "channels_count": result.Counts.Channels, "models_count": result.Counts.Models, "groups_count": result.Counts.Groups, "rename_count": result.RenameCount, "result_json": string(encoded), "result_version": 1})
	if update.Error != nil {
		return result, update.Error
	}
	if update.RowsAffected != 1 {
		return result, NewError("OPERATION_MISMATCH", "commit")
	}
	if err := s.inject("receipt"); err != nil {
		return result, err
	}
	return result, nil
}
