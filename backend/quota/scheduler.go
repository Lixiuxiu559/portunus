package quota

import (
	"context"
	"sync"
	"time"
)

func nextRegular(src Source, now time.Time) *time.Time {
	if !src.Enabled || src.AutoRefreshMinutes == 0 {
		return nil
	}
	next := now.Add(time.Duration(src.AutoRefreshMinutes)*time.Minute + time.Duration(src.ID%11)*time.Second)
	return &next
}
func due(src Source, st State, now time.Time) bool {
	if !src.Enabled || src.AutoRefreshMinutes <= 0 {
		return false
	}
	if st.NextAllowedAt != nil && now.Before(*st.NextAllowedAt) {
		return false
	}
	if st.NextScheduledAt != nil {
		return !now.Before(*st.NextScheduledAt)
	}
	if st.Failure != nil && st.Failure.Category != "cancelled" {
		return false
	}
	if st.LastAttemptAt == nil {
		return true
	}
	return !now.Before(st.LastAttemptAt.Add(time.Duration(src.AutoRefreshMinutes) * time.Minute))
}

// RunScheduler uses a single ticker and two bounded workers, never a goroutine per source.
func (s *Service) RunScheduler(ctx context.Context) {
	jobs := make(chan int64, 2)
	done := make(chan struct{}, 2)
	var queued sync.Map
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-ctx.Done():
					return
				case id, ok := <-jobs:
					if !ok {
						return
					}
					_, _ = s.refresh(ctx, id, true)
					queued.Delete(id)
				}
			}
		}()
	}
	defer func() { close(jobs); <-done; <-done }()
	interval := s.schedulerInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var sources []Source
			if s.db.WithContext(ctx).Where("enabled = ? AND auto_refresh_minutes > 0", true).Order("id").Find(&sources).Error != nil {
				continue
			}
			states, err := readStates(s.db.WithContext(ctx), sources)
			if err != nil {
				continue
			}
			for _, src := range sources {
				if !due(src, states[src.ID], s.now()) || s.inProgress(src.ID) {
					continue
				}
				if _, loaded := queued.LoadOrStore(src.ID, true); loaded {
					continue
				}
				select {
				case jobs <- src.ID:
				default:
					queued.Delete(src.ID)
				}
			}
		}
	}
}
