package quota

import (
	"context"
	"gorm.io/gorm"
	"reflect"
	"sync"
	"time"
)

type activeQuery struct {
	channelID int64
	revision  int64
	cancel    context.CancelFunc
}

type Service struct {
	db                *gorm.DB
	client            HTTPDoer
	loader            ChannelLoader
	execute           Executor
	now               func() time.Time
	mu                sync.Mutex
	busy              map[int64]*activeQuery
	nextPreviewID     int64
	slots             chan struct{}
	schedulerInterval time.Duration
}

func NewService(db *gorm.DB, client HTTPDoer, loader ChannelLoader) *Service {
	if client == nil {
		client = NewHTTPClient()
	}
	return &Service{db: db, client: client, loader: loader, execute: Execute, now: time.Now, busy: map[int64]*activeQuery{}, slots: make(chan struct{}, 2)}
}
func (s *Service) inProgress(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy[id] != nil
}
func (s *Service) acquire(id, channelID, revision int64, cancel context.CancelFunc) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[id] != nil {
		return false, nil
	}
	select {
	case s.slots <- struct{}{}:
		s.busy[id] = &activeQuery{channelID: channelID, revision: revision, cancel: cancel}
		return true, nil
	default:
		return false, statusError(429, "额度查询并发已满，请稍后重试")
	}
}
func (s *Service) release(id int64) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.busy, id); <-s.slots }

// CancelChannel stops current queries after a channel transaction has committed.
func (s *Service) CancelChannel(channelID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.busy {
		if task.channelID == channelID {
			task.cancel()
		}
	}
}
func (s *Service) cancelSource(id, revision int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task := s.busy[id]; task != nil && (revision == 0 || (task.revision > 0 && task.revision <= revision)) {
		task.cancel()
	}
}
func (s *Service) bindChannel(id, channelID, revision int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task := s.busy[id]; task != nil {
		task.channelID = channelID
		task.revision = revision
	}
}
func (s *Service) load(ctx context.Context, id int64) (src Source, st State, key string, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.First(&src, id).Error; e != nil {
			return e
		}
		var e error
		key, e = s.loader(tx, src.ChannelID)
		if e != nil {
			return e
		}
		st, e = readState(tx, src)
		return e
	})
	err = publicDBError(err)
	return
}
func (s *Service) Create(ctx context.Context, channelID int64, in SourceInput) (out SourceView, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 一个渠道只对应一个上游账户，余额查询配置是单例；已有配置时要求编辑原记录。
		var existing int64
		if e := tx.Model(&Source{}).Where("channel_id = ?", channelID).Count(&existing).Error; e != nil {
			return e
		}
		if existing > 0 {
			return statusError(409, "该渠道已配置查询来源，请编辑现有配置")
		}
		key, e := s.loader(tx, channelID)
		if e != nil {
			return e
		}
		src, _, e := sourceFromInput(Source{ChannelID: channelID, ConfigRevision: 1}, in, key, false)
		if e != nil {
			return e
		}
		if e = tx.Create(&src).Error; e != nil {
			return e
		}
		out = sourceView(src, State{ConfigRevision: 1}, key, s.now(), false)
		return nil
	})
	return out, publicDBError(err)
}
func (s *Service) Update(ctx context.Context, id int64, in SourceInput) (out SourceView, err error) {
	changed := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old Source
		if e := tx.First(&old, id).Error; e != nil {
			return e
		}
		if in.ExpectedRevision <= 0 || old.ConfigRevision != in.ExpectedRevision {
			return statusError(409, "查询配置已变更，请重新读取")
		}
		key, e := s.loader(tx, old.ChannelID)
		if e != nil {
			return e
		}
		src, _, e := sourceFromInput(old, in, key, false)
		if e != nil {
			return e
		}
		if reflect.DeepEqual(src, old) {
			st, e := readState(tx, old)
			if e != nil {
				return e
			}
			out = sourceView(old, st, key, s.now(), s.inProgress(id))
			return nil
		}
		changed = true
		src.ConfigRevision++
		result := tx.Model(&Source{}).Where("id = ? AND config_revision = ?", id, in.ExpectedRevision).Select("*").Updates(&src)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return statusError(409, "查询配置已变更，请重新读取")
		}
		if e = tx.Where("source_id = ?", id).Delete(&State{}).Error; e != nil {
			return e
		}
		out = sourceView(src, State{ConfigRevision: src.ConfigRevision}, key, s.now(), s.inProgress(id))
		return nil
	})
	if err == nil && changed {
		s.cancelSource(id, in.ExpectedRevision)
	}
	return out, publicDBError(err)
}
func (s *Service) Delete(ctx context.Context, id int64) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var src Source
		if err := tx.First(&src, id).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id = ?", id).Delete(&State{}).Error; err != nil {
			return err
		}
		return tx.Delete(&src).Error
	})
	if err == nil {
		s.cancelSource(id, 0)
	}
	return publicDBError(err)
}
func (s *Service) List(ctx context.Context, channelID int64) (out []SourceView, err error) {
	out = []SourceView{}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		key, e := s.loader(tx, channelID)
		if e != nil {
			return e
		}
		var sources []Source
		if e = tx.Where("channel_id = ?", channelID).Order("id").Find(&sources).Error; e != nil {
			return e
		}
		states, e := readStates(tx, sources)
		if e != nil {
			return e
		}
		for _, src := range sources {
			out = append(out, sourceView(src, states[src.ID], key, s.now(), s.inProgress(src.ID)))
		}
		return nil
	})
	return out, publicDBError(err)
}
func (s *Service) Summaries(ctx context.Context, ids []int64) ([]ChannelSummary, error) {
	if len(ids) > 100 {
		return nil, statusError(400, "一次最多读取 100 个渠道")
	}
	out := make([]ChannelSummary, 0, len(ids))
	positions := map[int64]int{}
	for _, id := range ids {
		if _, ok := positions[id]; ok {
			continue
		}
		positions[id] = len(out)
		out = append(out, ChannelSummary{ChannelID: id, Sources: []SourceView{}})
	}
	if len(ids) == 0 {
		return out, nil
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sources []Source
		if err := tx.Where("channel_id IN ?", ids).Order("id").Find(&sources).Error; err != nil {
			return err
		}
		states, err := readStates(tx, sources)
		if err != nil {
			return err
		}
		for _, src := range sources {
			key := ""
			if src.CredentialMode == CredentialInherit {
				key = "inherited"
			}
			i := positions[src.ChannelID]
			out[i].Sources = append(out[i].Sources, sourceView(src, states[src.ID], key, s.now(), s.inProgress(src.ID)))
		}
		return nil
	})
	return out, err
}
func (s *Service) Preview(ctx context.Context, in PreviewInput) (View, error) {
	var src Source
	var key string
	var adapter Adapter
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		key, e = s.loader(tx, in.ChannelID)
		if e != nil {
			return e
		}
		if in.SourceID != 0 {
			if e = tx.First(&src, in.SourceID).Error; e != nil {
				return e
			}
			if src.ChannelID != in.ChannelID {
				return statusError(400, "查询来源不属于此渠道")
			}
		}
		src, adapter, e = sourceFromInput(src, in.SourceInput, key, true)
		return e
	})
	if err != nil {
		return View{}, publicDBError(err)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	taskID := in.SourceID
	if taskID == 0 {
		s.mu.Lock()
		s.nextPreviewID--
		taskID = s.nextPreviewID
		s.mu.Unlock()
	}
	acquired, err := s.acquire(taskID, in.ChannelID, src.ConfigRevision, cancel)
	if err != nil {
		return View{}, err
	}
	if !acquired {
		return View{Status: "never", InProgress: true}, nil
	}
	defer s.release(taskID)
	now := s.now()
	snapshot, failure := s.execute(queryCtx, s.client, adapter, resolvedSource(src, key))
	src.Enabled = true
	st := State{ConfigRevision: src.ConfigRevision, LastAttemptAt: &now}
	s.applyResult(&st, src, snapshot, failure, s.now())
	return makeView(src, st, s.now(), false), nil
}
func (s *Service) Refresh(ctx context.Context, id int64) (SourceView, error) {
	return s.refresh(ctx, id, false)
}
func (s *Service) refresh(ctx context.Context, id int64, automatic bool) (SourceView, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	acquired, err := s.acquire(id, 0, 0, cancel)
	if err != nil {
		return SourceView{}, err
	}
	if !acquired {
		src, st, key, e := s.load(ctx, id)
		if e != nil {
			return SourceView{}, e
		}
		return sourceView(src, st, key, s.now(), true), nil
	}
	defer s.release(id)
	src, st, key, err := s.load(queryCtx, id)
	if err != nil {
		return SourceView{}, err
	}
	s.bindChannel(id, src.ChannelID, src.ConfigRevision)
	if !src.Enabled {
		return SourceView{}, statusError(400, "查询来源尚未启用")
	}
	now := s.now()
	if st.NextAllowedAt != nil && now.Before(*st.NextAllowedAt) {
		return SourceView{}, statusError(429, "额度查询处于冷却期")
	}
	if automatic && !due(src, st, now) {
		return sourceView(src, st, key, now, false), nil
	}
	adapter, ok := LookupAdapter(src.AdapterID)
	if !ok {
		return SourceView{}, statusError(400, "不支持的查询适配器")
	}
	if src.CredentialMode != CredentialNone && resolvedSource(src, key).Token == "" {
		return SourceView{}, statusError(400, "查询凭据不能为空")
	}
	st.LastAttemptAt = &now
	// Persist the attempt only while the configuration remains current.
	if err = s.commitState(queryCtx, src, st, false); err != nil {
		return SourceView{}, err
	}
	snapshot, failure := s.execute(queryCtx, s.client, adapter, resolvedSource(src, key))
	s.applyResult(&st, src, snapshot, failure, s.now())
	// Application cancellation must not prevent clearing an otherwise current attempt.
	commitCtx := ctx
	var finishCancel context.CancelFunc
	if ctx.Err() != nil {
		commitCtx, finishCancel = context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer finishCancel()
	}
	if err = s.commitState(commitCtx, src, st, true); err != nil {
		return SourceView{}, err
	}
	return sourceView(src, st, key, s.now(), false), nil
}
func (s *Service) commitState(ctx context.Context, src Source, st State, requireEnabled bool) error {
	return publicDBError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Source
		if e := tx.First(&current, src.ID).Error; e != nil {
			return e
		}
		if current.ConfigRevision != src.ConfigRevision || (requireEnabled && !current.Enabled) {
			return statusError(409, "查询期间配置已变更，已丢弃旧结果")
		}
		if _, e := s.loader(tx, current.ChannelID); e != nil {
			return e
		}
		// The conditional write acquires the DB write lock before the state upsert.
		result := tx.Model(&Source{}).Where("id = ? AND config_revision = ? AND enabled = ?", src.ID, src.ConfigRevision, true).UpdateColumn("config_revision", src.ConfigRevision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return statusError(409, "查询期间配置已变更")
		}
		return tx.Save(&st).Error
	}))
}
func (s *Service) applyResult(st *State, src Source, snapshot *Snapshot, failure *Failure, now time.Time) {
	st.LastCompletedAt = &now
	st.ConfigRevision = src.ConfigRevision
	if failure == nil && snapshot != nil {
		st.Snapshot = snapshot
		st.SnapshotUsable = true
		st.SnapshotSchemaVersion = 1
		st.LastSuccessAt = &now
		st.LastOutcome = "success"
		st.Failure = nil
		st.ConsecutiveFailures = 0
		st.NextAllowedAt = nil
		st.NextScheduledAt = nextRegular(src, now)
		return
	}
	if failure == nil {
		failure = &Failure{Code: "invalid_response", Category: "invalid_response", Message: "查询未返回有效结果"}
	}
	st.Failure = failure
	st.LastOutcome = failure.Category
	st.NextScheduledAt = nil
	switch failure.Category {
	case "authentication", "permission", "configuration":
		st.SnapshotUsable = false
	}
	if failure.Category == "cancelled" {
		st.NextScheduledAt = nextRegular(src, now)
		return
	}
	st.ConsecutiveFailures++
	if failure.Category == "rate_limited" {
		seconds := failure.RetryAfterSeconds
		if seconds <= 0 {
			seconds = 60
		}
		if seconds > 3600 {
			seconds = 3600
		}
		next := now.Add(time.Duration(seconds) * time.Second)
		st.NextAllowedAt = &next
		if src.AutoRefreshMinutes > 0 {
			st.NextScheduledAt = &next
		}
		return
	}
	st.NextAllowedAt = nil
	if failure.Category == "temporary" {
		delays := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute}
		i := st.ConsecutiveFailures - 1
		if i >= len(delays) {
			i = len(delays) - 1
		}
		next := now.Add(delays[i])
		if src.AutoRefreshMinutes > 0 {
			st.NextScheduledAt = &next
		}
	}
}
