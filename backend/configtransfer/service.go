package configtransfer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

type retainedPreview struct {
	dto      PreviewDTO
	payload  *Payload
	raw      []byte
	plan     namePlan
	deadline time.Time // includes Go's monotonic clock
}
type Service struct {
	db            *gorm.DB
	instanceID    string
	ready         atomic.Bool
	stopping      atomic.Bool
	unavailable   atomic.Pointer[Error]
	start         sync.Once
	mu            sync.Mutex
	previews      map[string]*retainedPreview
	running       map[string]bool
	retainedBytes int
	slot          chan struct{}
	// fault is only used by same-package synthetic transaction tests.
	fault func(string) error
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func newPreviewID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func NewService(db *gorm.DB, instanceID string) *Service {
	if instanceID == "" {
		instanceID = newUUID()
	}
	return &Service{db: silentDB(db), instanceID: instanceID, previews: map[string]*retainedPreview{}, running: map[string]bool{}, slot: make(chan struct{}, 1)}
}
func (s *Service) InstanceID() string { return s.instanceID }
func (s *Service) Ready() bool        { return s.ready.Load() }

// Start returns after the initial schema/fencing pass. Failure disables only
// this feature; bounded subsequent passes may make it ready without restarting.
func (s *Service) Start(ctx context.Context) error {
	var initial error
	s.start.Do(func() {
		initial = s.initialize(ctx)
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			defer s.releaseAll()
			for {
				select {
				case <-ctx.Done():
					s.stopping.Store(true)
					s.ready.Store(false)
					return
				case <-ticker.C:
					if !s.ready.Load() {
						_ = s.initialize(ctx)
					}
					s.expire(ctx)
				}
			}
		}()
	})
	return initial
}
func (s *Service) initialize(ctx context.Context) error {
	if err := Migrate(s.db.WithContext(ctx)); err != nil {
		var e *Error
		if errors.As(err, &e) {
			s.unavailable.Store(e)
		}
		return err
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.db.WithContext(bounded).Exec("UPDATE config_import_receipts SET status='aborted', failure_code='SESSION_LOST', failure_stage='recovery' WHERE status='prepared' AND owner_instance_id <> ?", s.instanceID).Error
		cancel()
		if err == nil {
			if ctx.Err() != nil || s.stopping.Load() {
				return NewError("TRANSFER_UNAVAILABLE", "recovery")
			}
			s.unavailable.Store(nil)
			s.ready.Store(true)
			return nil
		}
		if !isBusy(err) {
			break
		}
		select {
		case <-ctx.Done():
			return NewError("TRANSFER_UNAVAILABLE", "recovery")
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}
	return safeDBError(err, "recovery")
}

type leaseKey struct{}
type responseLeaseKey struct{}
type responseLease struct {
	mu      sync.Mutex
	held    bool
	service *Service
}

// ResponseScope retains any subsequently acquired lease through the caller's
// response encoding. It does not acquire early, so same-op running/state checks
// remain available while a different heavy task is in progress.
func (s *Service) ResponseScope(ctx context.Context) (context.Context, func()) {
	lease := &responseLease{service: s}
	var once sync.Once
	return context.WithValue(ctx, responseLeaseKey{}, lease), func() {
		once.Do(func() {
			lease.mu.Lock()
			defer lease.mu.Unlock()
			if lease.held {
				<-s.slot
				lease.held = false
			}
		})
	}
}

// Acquire is called by HTTP before reading large request bodies. Context leases
// make direct domain calls use the same admission rule without double-acquiring.
func (s *Service) Acquire(ctx context.Context) (context.Context, func(), error) {
	if ctx.Value(leaseKey{}) == s {
		return ctx, func() {}, nil
	}
	if lease, ok := ctx.Value(responseLeaseKey{}).(*responseLease); ok && lease.service == s {
		lease.mu.Lock()
		defer lease.mu.Unlock()
		if lease.held {
			return context.WithValue(ctx, leaseKey{}, s), func() {}, nil
		}
		if !s.Ready() {
			return ctx, func() {}, s.Unavailable()
		}
		select {
		case s.slot <- struct{}{}:
			lease.held = true
			return context.WithValue(ctx, leaseKey{}, s), func() {}, nil
		default:
			return ctx, func() {}, NewError("TRANSFER_BUSY", "input")
		}
	}
	if !s.Ready() {
		return ctx, func() {}, s.Unavailable()
	}
	select {
	case s.slot <- struct{}{}:
		var once sync.Once
		return context.WithValue(ctx, leaseKey{}, s), func() { once.Do(func() { <-s.slot }) }, nil
	default:
		return ctx, func() {}, NewError("TRANSFER_BUSY", "input")
	}
}
func (s *Service) Unavailable() *Error {
	if e := s.unavailable.Load(); e != nil {
		copy := *e
		return &copy
	}
	return NewError("TRANSFER_UNAVAILABLE", "input")
}
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "database is locked") || strings.Contains(s, "database is busy") || strings.Contains(s, "sqlite_busy") || strings.Contains(s, "sqlite_locked")
}
func safeDBError(err error, stage string) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if isBusy(err) {
		return NewError("DB_BUSY", stage)
	}
	return NewError("TRANSFER_UNAVAILABLE", stage)
}
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	d := Summary{FormatVersion: 1, InstanceID: s.instanceID, Ready: s.Ready(), Limits: map[string]int{"plaintext_bytes": MaxPlaintextBytes, "file_bytes": MaxFileBytes, "channels": MaxChannels, "models": MaxModels, "groups": MaxGroups, "items": MaxItems, "preview_bytes": MaxPreviewBytes, "result_bytes": MaxResultBytes, "password_bytes": MaxPasswordBytes, "preview_ttl_seconds": int(PreviewTTL / time.Second)}}
	for _, entry := range []struct {
		table string
		count *int
	}{{"channels", &d.Counts.Channels}, {"models", &d.Counts.Models}, {"groups", &d.Counts.Groups}} {
		var n int64
		if err := s.db.WithContext(ctx).Table(entry.table).Count(&n).Error; err != nil {
			return d, safeDBError(err, "snapshot")
		}
		*entry.count = int(n)
	}
	d.CanExport = d.Ready && !d.Counts.Empty()
	return d, nil
}
func (s *Service) Export(ctx context.Context, password []byte, appVersion string) ([]byte, Counts, error) {
	ctx, release, err := s.Acquire(ctx)
	if err != nil {
		return nil, Counts{}, err
	}
	defer release()
	if !ValidPassword(password) || !validString(appVersion, 128, true) {
		return nil, Counts{}, NewError("INVALID_INPUT", "input")
	}
	p, err := snapshot(ctx, s.db, appVersion)
	if err != nil {
		return nil, Counts{}, err
	}
	plain, err := payloadBytes(p)
	if err != nil {
		return nil, Counts{}, err
	}
	defer wipe(plain)
	if ctx.Err() != nil {
		return nil, Counts{}, NewError("TRANSFER_UNAVAILABLE", "encrypt")
	}
	file, err := Encrypt(plain, password)
	if err != nil {
		return nil, Counts{}, err
	}
	if ctx.Err() != nil {
		wipe(file)
		return nil, Counts{}, NewError("TRANSFER_UNAVAILABLE", "encrypt")
	}
	return file, p.Counts(), nil
}
func (s *Service) Preview(ctx context.Context, file, password []byte) (PreviewDTO, error) {
	ctx, release, err := s.Acquire(ctx)
	if err != nil {
		return PreviewDTO{}, err
	}
	defer release()
	plain, err := Decrypt(file, password)
	if err != nil {
		return PreviewDTO{}, err
	}
	kept := false
	defer func() {
		if !kept {
			wipe(plain)
		}
	}()
	if ctx.Err() != nil {
		return PreviewDTO{}, NewError("TRANSFER_UNAVAILABLE", "decrypt")
	}
	var payload Payload
	if err := DecodeStrict(plain, &payload); err != nil {
		return PreviewDTO{}, err
	}
	if err := Validate(&payload); err != nil {
		return PreviewDTO{}, err
	}
	if err := checkBudgets(&payload); err != nil {
		return PreviewDTO{}, err
	}
	h := sha256.Sum256(file)
	preview, err := s.prepare(ctx, &payload, plain, hex.EncodeToString(h[:]))
	if err != nil {
		return PreviewDTO{}, err
	}
	kept = true
	return preview, nil
}
func (s *Service) prepare(ctx context.Context, payload *Payload, raw []byte, fileHash string) (PreviewDTO, error) {
	s.expire(ctx)
	s.mu.Lock()
	full := len(s.previews) >= 2 || s.retainedBytes+len(raw) > 64*1024*1024
	s.mu.Unlock()
	if full {
		return PreviewDTO{}, NewError("LIMIT_EXCEEDED", "plan")
	}
	plan, err := makePlan(s.db.WithContext(ctx), payload, fileHash)
	if err != nil {
		return PreviewDTO{}, safeDBError(err, "plan")
	}
	now := time.Now()
	dto := previewDTO(payload, plan)
	dto.PreviewID = newPreviewID()
	dto.OperationID = newUUID()
	dto.FileSHA256 = fileHash
	dto.PlanSHA256 = plan.hash()
	dto.ExpiresAt = now.Add(PreviewTTL).UTC().Format(time.RFC3339Nano)
	n, err := encodedSize(dto)
	if err != nil || n > MaxPreviewBytes {
		return PreviewDTO{}, NewError("LIMIT_EXCEEDED", "plan")
	}
	row := map[string]any{"operation_id": dto.OperationID, "owner_instance_id": s.instanceID, "file_sha256": fileHash, "plan_sha256": dto.PlanSHA256, "status": "prepared", "created_at_ms": now.UnixMilli(), "expires_at_ms": now.Add(PreviewTTL).UnixMilli()}
	if err := s.db.WithContext(ctx).Table("config_import_receipts").Create(row).Error; err != nil {
		return PreviewDTO{}, safeDBError(err, "plan")
	}
	s.mu.Lock()
	if s.stopping.Load() {
		s.mu.Unlock()
		_ = s.fence(ctx, dto.OperationID, "SESSION_LOST", "recovery")
		return PreviewDTO{}, NewError("TRANSFER_UNAVAILABLE", "recovery")
	}
	s.previews[dto.PreviewID] = &retainedPreview{dto: dto, payload: payload, raw: raw, plan: plan, deadline: now.Add(PreviewTTL)}
	s.retainedBytes += len(raw)
	s.mu.Unlock()
	return dto, nil
}
func (s *Service) releasePreviewLocked(id string) {
	if p, ok := s.previews[id]; ok {
		wipe(p.raw)
		s.retainedBytes -= len(p.raw)
		p.raw = nil
		p.payload = nil
		delete(s.previews, id)
	}
}
func (s *Service) releaseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.previews {
		if !s.running[s.previews[id].dto.OperationID] {
			s.releasePreviewLocked(id)
		}
	}
}
func (s *Service) expire(ctx context.Context) {
	now := time.Now()
	ops := []string{}
	s.mu.Lock()
	for id, p := range s.previews {
		if !s.running[p.dto.OperationID] && !now.Before(p.deadline) {
			ops = append(ops, p.dto.OperationID)
			s.releasePreviewLocked(id)
		}
	}
	s.mu.Unlock()
	for _, op := range ops {
		_ = s.fence(ctx, op, "PREVIEW_EXPIRED", "recovery")
	}
}
func (s *Service) beginOp(op string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[op] {
		return false
	}
	s.running[op] = true
	return true
}
func (s *Service) endOp(op string) {
	s.mu.Lock()
	delete(s.running, op)
	if s.stopping.Load() {
		for id, p := range s.previews {
			if p.dto.OperationID == op {
				s.releasePreviewLocked(id)
			}
		}
	}
	s.mu.Unlock()
}
func (s *Service) isRunning(op string) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.running[op] }

type receipt struct {
	OperationID      string
	OwnerInstanceID  string
	FileSHA256       string
	PlanSHA256       string
	Status           string
	CreatedAtMs      int64
	ExpiresAtMs      int64
	ConfirmedAtMs    *int64
	ClaimStartedAtMs *int64
	CommittedAtMs    *int64
	AcknowledgedAtMs *int64
	ResultVersion    int
	ChannelsCount    int
	ModelsCount      int
	GroupsCount      int
	RenameCount      int
	ResultJSON       *string `gorm:"column:result_json"`
	FailureCode      *string
	FailureStage     *string
}

func (r receipt) counts() Counts { return Counts{r.ChannelsCount, r.ModelsCount, r.GroupsCount} }

const receiptStatusColumns = "operation_id, owner_instance_id, file_sha256, plan_sha256, status, created_at_ms, expires_at_ms, confirmed_at_ms, claim_started_at_ms, committed_at_ms, acknowledged_at_ms, result_version, channels_count, models_count, groups_count, rename_count, failure_code, failure_stage"

func (s *Service) receipt(ctx context.Context, op string) (receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var r receipt
	err := s.db.WithContext(ctx).Table("config_import_receipts").Select(receiptStatusColumns).Where("operation_id = ?", op).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r, NewError("NOT_FOUND", "recovery")
	}
	if err != nil {
		return r, safeDBError(err, "recovery")
	}
	return r, nil
}
func (s *Service) state(r receipt) StateDTO {
	d := StateDTO{OperationID: r.OperationID, State: r.Status, Counts: r.counts()}
	if r.Status == "prepared" {
		d.ExpiresAt = timestamp(r.ExpiresAtMs)
	}
	if r.FailureCode != nil {
		d.FailureCode = *r.FailureCode
	}
	if r.FailureStage != nil {
		d.Stage = *r.FailureStage
	}
	return d
}
func (s *Service) storedResult(ctx context.Context, r receipt, replayed bool) (ResultDTO, error) {
	ctx, release, err := s.Acquire(ctx)
	if err != nil {
		return ResultDTO{}, err
	}
	defer release()
	var row receipt
	if r.ResultVersion != 1 {
		return ResultDTO{}, NewError("RESULT_UNCONFIRMED", "recovery")
	}
	if err := s.db.WithContext(ctx).Table("config_import_receipts").Select("CASE WHEN length(CAST(result_json AS BLOB)) <= ? THEN result_json ELSE NULL END AS result_json", MaxResultBytes).Where("operation_id = ? AND status = 'committed'", r.OperationID).Take(&row).Error; err != nil {
		return ResultDTO{}, NewError("RESULT_UNCONFIRMED", "recovery")
	}
	if row.ResultJSON == nil || len(*row.ResultJSON) > MaxResultBytes {
		return ResultDTO{}, NewError("RESULT_UNCONFIRMED", "recovery")
	}
	var d ResultDTO
	if err := DecodeStrict([]byte(*row.ResultJSON), &d); err != nil || d.OperationID != r.OperationID || d.State != "committed" || d.ResultVersion != 1 || d.Counts != r.counts() || d.RenameCount != r.RenameCount {
		return ResultDTO{}, NewError("RESULT_UNCONFIRMED", "recovery")
	}
	d.Replayed = replayed
	return d, nil
}
func (s *Service) Result(ctx context.Context, op string) (any, int, error) {
	r, err := s.receipt(ctx, op)
	if err != nil {
		return nil, 0, err
	}
	if r.Status == "committed" {
		d, err := s.storedResult(ctx, r, true)
		return d, 200, withOperation(err, op)
	}
	if r.Status == "prepared" && s.isRunning(op) {
		return s.state(r), 202, nil
	}
	return s.state(r), 200, nil
}
func (s *Service) fence(ctx context.Context, op, code, stage string) error {
	result := s.db.WithContext(ctx).Exec("UPDATE config_import_receipts SET status='aborted', failure_code=?, failure_stage=? WHERE operation_id=? AND status='prepared'", code, stage, op)
	if result.Error != nil {
		return NewError("RESULT_UNCONFIRMED", "recovery")
	}
	return nil
}
func (s *Service) Recover(ctx context.Context, op string) (any, int, error) {
	if !s.beginOp(op) {
		return s.Result(ctx, op)
	}
	terminal := false
	defer func() {
		// Only a proven terminal state enables this cleanup. Keep this operation
		// owned until every retained copy is released; the running-operation
		// path above never enters this cleanup.
		if terminal {
			s.mu.Lock()
			for id, preview := range s.previews {
				if preview.dto.OperationID == op {
					s.releasePreviewLocked(id)
				}
			}
			s.mu.Unlock()
		}
		s.endOp(op)
	}()
	r, err := s.receipt(ctx, op)
	if err != nil {
		return nil, 0, err
	}
	if r.Status != "prepared" {
		terminal = r.Status == "committed" || r.Status == "aborted"
		return s.Result(ctx, op)
	}
	s.mu.Lock()
	var live *retainedPreview
	for _, p := range s.previews {
		if p.dto.OperationID == op {
			live = p
			break
		}
	}
	lost := live == nil || (live != nil && !time.Now().Before(live.deadline))
	if lost && live != nil {
		s.releasePreviewLocked(live.dto.PreviewID)
	}
	s.mu.Unlock()
	if r.OwnerInstanceID != s.instanceID || lost || r.ConfirmedAtMs != nil {
		if err := s.fence(ctx, op, "SESSION_LOST", "recovery"); err != nil {
			return nil, 0, withOperation(err, op)
		}
	}
	// Ignore this method's own short ownership marker for the returned status.
	r, err = s.receipt(ctx, op)
	if err != nil {
		return nil, 0, withOperation(NewError("RESULT_UNCONFIRMED", "recovery"), op)
	}
	terminal = r.Status == "committed" || r.Status == "aborted"
	if r.Status == "committed" {
		d, err := s.storedResult(ctx, r, true)
		return d, 200, err
	}
	return s.state(r), 200, nil
}
func withOperation(err error, op string) error {
	var e *Error
	if errors.As(err, &e) {
		copy := *e
		copy.OperationID = op
		return &copy
	}
	return err
}
func (s *Service) Discard(ctx context.Context, id string) (StateDTO, error) {
	s.mu.Lock()
	p := s.previews[id]
	if p == nil {
		s.mu.Unlock()
		return StateDTO{}, NewError("PREVIEW_EXPIRED", "input")
	}
	op := p.dto.OperationID
	if s.running[op] {
		s.mu.Unlock()
		e := NewError("OPERATION_RUNNING", "commit")
		e.Status = 409
		return StateDTO{}, withOperation(e, op)
	}
	s.running[op] = true
	s.releasePreviewLocked(id)
	s.mu.Unlock()
	defer s.endOp(op)
	if err := s.fence(ctx, op, "USER_CANCELLED", "input"); err != nil {
		return StateDTO{}, withOperation(err, op)
	}
	r, err := s.receipt(ctx, op)
	if err != nil {
		return StateDTO{}, err
	}
	return s.state(r), nil
}
func (s *Service) Ack(ctx context.Context, op string) error {
	result := s.db.WithContext(ctx).Exec("UPDATE config_import_receipts SET acknowledged_at_ms=COALESCE(acknowledged_at_ms,?) WHERE operation_id=? AND status IN ('committed','aborted')", time.Now().UnixMilli(), op)
	if result.Error != nil {
		return safeDBError(result.Error, "recovery")
	}
	if result.RowsAffected == 0 {
		r, err := s.receipt(ctx, op)
		if err != nil {
			return err
		}
		if r.Status == "prepared" {
			return NewError("OPERATION_MISMATCH", "recovery")
		}
	}
	return nil
}

type recoveryCursor struct {
	CreatedAtMs int64  `json:"created_at_ms"`
	OperationID string `json:"operation_id"`
}

func (s *Service) RecoveryList(ctx context.Context, cursor string, limit int) (RecoveryList, error) {
	d := RecoveryList{Data: []RecoverySummary{}}
	if limit < 1 || limit > 100 || len(cursor) > 256 {
		return d, NewError("INVALID_INPUT", "input")
	}
	query := s.db.WithContext(ctx).Table("config_import_receipts").Select(receiptStatusColumns).Where("confirmed_at_ms IS NOT NULL AND acknowledged_at_ms IS NULL")
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		var c recoveryCursor
		if err != nil || DecodeStrict(b, &c) != nil || !ValidOperationID(c.OperationID) || c.CreatedAtMs < 0 {
			return d, NewError("INVALID_INPUT", "input")
		}
		query = query.Where("created_at_ms < ? OR (created_at_ms = ? AND operation_id < ?)", c.CreatedAtMs, c.CreatedAtMs, c.OperationID)
	}
	var rows []receipt
	if err := query.Order("created_at_ms DESC, operation_id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return d, safeDBError(err, "recovery")
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	for _, r := range rows {
		summary := RecoverySummary{OperationID: r.OperationID, State: r.Status, CreatedAt: timestamp(r.CreatedAtMs), ConfirmedAt: timestamp(*r.ConfirmedAtMs), Counts: r.counts(), RenameCount: r.RenameCount}
		if r.FailureCode != nil {
			summary.FailureCode = *r.FailureCode
		}
		d.Data = append(d.Data, summary)
	}
	if more && len(rows) > 0 {
		last := rows[len(rows)-1]
		b, _ := json.Marshal(recoveryCursor{last.CreatedAtMs, last.OperationID})
		d.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return d, nil
}
