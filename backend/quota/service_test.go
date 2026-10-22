package quota

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type quotaTestChannel struct {
	ID  int64 `gorm:"primaryKey"`
	Key string
}

func quotaServiceTest(t *testing.T) *Service {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { sql.Close() })
	if e = db.AutoMigrate(&Source{}, &State{}, &quotaTestChannel{}); e != nil {
		t.Fatal(e)
	}
	db.Create(&quotaTestChannel{ID: 1, Key: "channel-secret"})
	s := NewService(db, nil, func(tx *gorm.DB, id int64) (string, error) {
		var ch quotaTestChannel
		e := tx.First(&ch, id).Error
		return ch.Key, e
	})
	s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
		v := "0"
		return &Snapshot{Items: []Item{{ID: "balance", Remaining: &v}}}, nil
	}
	return s
}
func quotaInput() SourceInput {
	return SourceInput{Name: "余额", AdapterID: "deepseek.balance", CredentialMode: CredentialInherit}
}
func quotaStatus(t *testing.T, err error, want int) {
	t.Helper()
	var status *shared.StatusError
	if !errors.As(err, &status) || status.Status != want {
		t.Fatalf("want status %d, got %v", want, err)
	}
}
func TestServiceSecretsDefaultsAndRevision(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	in := quotaInput()
	src, e := s.Create(ctx, 1, in)
	if e != nil {
		t.Fatal(e)
	}
	if src.Enabled || src.AutoRefreshMinutes != 0 || src.ConfigRevision != 1 || !src.HasToken {
		t.Fatalf("defaults %+v", src)
	}
	_, e = s.Refresh(ctx, src.ID)
	quotaStatus(t, e, 400)
	token := "dedicated-secret"
	in.CredentialMode = CredentialDedicated
	in.Credentials = &CredentialPatch{Token: &token}
	in.ExpectedRevision = 1
	src, e = s.Update(ctx, src.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(src)
	if strings.Contains(string(encoded), token) {
		t.Fatal("secret exposed")
	}
	in.ExpectedRevision = 1
	_, e = s.Update(ctx, src.ID, in)
	quotaStatus(t, e, 409)
	in.ExpectedRevision = src.ConfigRevision
	in.Credentials = &CredentialPatch{}
	src, e = s.Update(ctx, src.ID, in)
	if e != nil || !src.HasToken {
		t.Fatalf("missing token must preserve %v", e)
	}
	in.ExpectedRevision = src.ConfigRevision
	in.Credentials = &CredentialPatch{Clear: true}
	src, e = s.Update(ctx, src.ID, in)
	if e != nil || src.HasToken {
		t.Fatalf("clear %v", e)
	}
	in.ExpectedRevision = src.ConfigRevision
	in.Enabled = true
	_, e = s.Update(ctx, src.ID, in)
	quotaStatus(t, e, 400)
	_, e = s.Preview(ctx, PreviewInput{SourceInput: in, ChannelID: 1, SourceID: src.ID})
	quotaStatus(t, e, 400)
}
func TestServiceSnapshotFailureTTLAndInvalidation(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	in := quotaInput()
	in.Enabled = true
	src, e := s.Create(ctx, 1, in)
	if e != nil {
		t.Fatal(e)
	}
	src, e = s.Refresh(ctx, src.ID)
	if e != nil || src.State.Status != "fresh" {
		t.Fatalf("success %+v %v", src, e)
	}
	success := *src.State.LastSuccessAt
	s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
		return nil, &Failure{Category: "temporary"}
	}
	src, e = s.Refresh(ctx, src.ID)
	if e != nil || src.State.Status != "stale" || !src.State.LastSuccessAt.Equal(success) {
		t.Fatal("temporary must preserve success", e)
	}
	now = now.Add(11 * time.Minute)
	srcs, e := s.List(ctx, 1)
	if e != nil || srcs[0].State.Snapshot != nil || srcs[0].State.Status != "expired" {
		t.Fatal("TTL", e)
	}
	now = success
	s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
		return nil, &Failure{Category: "authentication"}
	}
	src, e = s.Refresh(ctx, src.ID)
	if e != nil || src.State.Snapshot != nil {
		t.Fatal("auth stale snapshot", e)
	}
	s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
		return nil, &Failure{Category: "temporary"}
	}
	src, e = s.Refresh(ctx, src.ID)
	if e != nil || src.State.Snapshot != nil {
		t.Fatal("auth snapshot revived", e)
	}
	e = s.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(&quotaTestChannel{}).Where("id = ?", 1).Update("key", "new-secret").Error; e != nil {
			return e
		}
		return InvalidateInheritedSourcesTx(tx, 1)
	})
	if e != nil {
		t.Fatal(e)
	}
	srcs, e = s.List(ctx, 1)
	if e != nil || srcs[0].ConfigRevision != 2 || srcs[0].State.LastSnapshot != nil {
		t.Fatal("key invalidation", e)
	}
}
func TestServiceConcurrentOldResultAndDeletedChannel(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	in := quotaInput()
	in.Enabled = true
	src, e := s.Create(ctx, 1, in)
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
		close(started)
		<-release
		return &Snapshot{}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(ctx, src.ID); done <- err }()
	<-started
	view, e := s.Refresh(ctx, src.ID)
	if e != nil || !view.State.InProgress {
		t.Fatalf("source mutex %v", e)
	}
	in.ExpectedRevision = 1
	in.Enabled = false
	_, e = s.Update(ctx, src.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	quotaStatus(t, <-done, 409)
	var count int64
	s.db.Model(&State{}).Count(&count)
	if count != 0 {
		t.Fatal("old result recreated state")
	}
	if e = s.db.Transaction(func(tx *gorm.DB) error { return DeleteByChannelTx(tx, 1) }); e != nil {
		t.Fatal(e)
	}
	_, e = s.Refresh(ctx, src.ID)
	quotaStatus(t, e, 404)
}
func TestServiceCooldownPreviewAndSchedulerCancellation(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	in := quotaInput()
	in.Enabled = true
	src, e := s.Create(ctx, 1, in)
	if e != nil {
		t.Fatal(e)
	}
	s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
		return nil, &Failure{Category: "rate_limited", RetryAfterSeconds: 120}
	}
	view, e := s.Refresh(ctx, src.ID)
	if e != nil || view.State.NextAllowedAt == nil {
		t.Fatal("cooldown missing", e)
	}
	_, e = s.Refresh(ctx, src.ID)
	quotaStatus(t, e, 429)
	s.db.Create(&quotaTestChannel{ID: 2, Key: "other"})
	_, e = s.Preview(ctx, PreviewInput{SourceInput: in, SourceID: src.ID, ChannelID: 2})
	quotaStatus(t, e, 400)
	cancelCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.RunScheduler(cancelCtx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}
func TestServiceGlobalConcurrencyAndCancelNoBackoff(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	in := quotaInput()
	in.Enabled = true
	var ids []int64
	for i := 0; i < 3; i++ {
		// 每个渠道只允许一个查询来源；并发限制跨渠道验证。
		db := s.db
		if e := db.Create(&quotaTestChannel{ID: int64(i + 2), Key: "channel-secret"}).Error; e != nil {
			t.Fatal(e)
		}
		src, e := s.Create(ctx, int64(i+1), in)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, src.ID)
	}
	started := make(chan struct{}, 2)
	s.execute = func(ctx context.Context, _ HTTPDoer, _ Adapter, _ ResolvedSource) (*Snapshot, *Failure) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, &Failure{Category: "cancelled"}
	}
	queryCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 2)
	for _, id := range ids[:2] {
		go func(id int64) { _, e := s.Refresh(queryCtx, id); done <- e }(id)
		<-started
	}
	_, e := s.Refresh(ctx, ids[2])
	quotaStatus(t, e, 429)
	cancel()
	for range 2 {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	var states []State
	s.db.Find(&states)
	for _, st := range states {
		if st.ConsecutiveFailures != 0 {
			t.Fatal("cancel increased failures")
		}
	}
}

func TestServiceInFlightKeyChangeAndDeletion(t *testing.T) {
	for _, change := range []string{"key", "delete"} {
		t.Run(change, func(t *testing.T) {
			s := quotaServiceTest(t)
			ctx := context.Background()
			in := quotaInput()
			in.Enabled = true
			src, e := s.Create(ctx, 1, in)
			if e != nil {
				t.Fatal(e)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
				close(started)
				<-release
				return &Snapshot{}, nil
			}
			done := make(chan error, 1)
			go func() { _, err := s.Refresh(ctx, src.ID); done <- err }()
			<-started
			if change == "key" {
				e = s.db.Transaction(func(tx *gorm.DB) error {
					if e := tx.Model(&quotaTestChannel{}).Where("id = ?", 1).Update("key", "changed").Error; e != nil {
						return e
					}
					return InvalidateInheritedSourcesTx(tx, 1)
				})
			} else {
				e = s.db.Transaction(func(tx *gorm.DB) error {
					if e := DeleteByChannelTx(tx, 1); e != nil {
						return e
					}
					return tx.Delete(&quotaTestChannel{}, 1).Error
				})
			}
			if e != nil {
				t.Fatal(e)
			}
			close(release)
			want := 409
			if change == "delete" {
				want = 404
			}
			quotaStatus(t, <-done, want)
			var n int64
			s.db.Model(&State{}).Count(&n)
			if n != 0 {
				t.Fatal("late query recreated state")
			}
		})
	}
}
func TestServiceSchedulerExecutesAndCancels(t *testing.T) {
	s := quotaServiceTest(t)
	s.schedulerInterval = time.Millisecond
	in := quotaInput()
	in.Enabled = true
	in.AutoRefreshMinutes = 1
	src, e := s.Create(context.Background(), 1, in)
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{}, 4)
	s.execute = func(ctx context.Context, _ HTTPDoer, _ Adapter, _ ResolvedSource) (*Snapshot, *Failure) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, &Failure{Category: "cancelled"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.RunScheduler(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no automatic query")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler workers did not stop")
	}
	var st State
	if e = s.db.First(&st, "source_id = ?", src.ID).Error; e != nil {
		t.Fatal(e)
	}
	if st.ConsecutiveFailures != 0 || st.LastOutcome != "cancelled" {
		t.Fatalf("cancel outcome %+v", st)
	}
	if len(started) != 0 {
		t.Fatal("duplicate scheduler requests")
	}
}
func TestServiceReadSummaryNeverQueriesAndUnchangedSaveKeepsSnapshot(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	in := quotaInput()
	in.Enabled = true
	src, e := s.Create(ctx, 1, in)
	if e != nil {
		t.Fatal(e)
	}
	src, e = s.Refresh(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedRevision = src.ConfigRevision
	src, e = s.Update(ctx, src.ID, in)
	if e != nil || src.ConfigRevision != 1 || src.State.Snapshot == nil {
		t.Fatalf("unchanged save discarded result %+v %v", src, e)
	}
	s.execute = func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure) {
		t.Fatal("read performed query")
		return nil, nil
	}
	summary, e := s.Summaries(ctx, []int64{1, 1, 99})
	if e != nil || len(summary) != 2 || len(summary[0].Sources) != 1 || len(summary[1].Sources) != 0 {
		t.Fatalf("summary %+v %v", summary, e)
	}
}

func TestServiceAdapterChangeRequiresExplicitSecret(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	token := "management-secret"
	in := SourceInput{Name: "管理额度", AdapterID: "openrouter.account_balance", CredentialMode: CredentialDedicated, Credentials: &CredentialPatch{Token: &token}}
	src, err := s.Create(ctx, 1, in)
	if err != nil {
		t.Fatal(err)
	}
	in.AdapterID = "newapi.account"
	in.QueryBaseURL = "http://localhost:9999"
	in.Options = QueryOptions{UserID: "1"}
	in.Credentials = nil
	in.ExpectedRevision = src.ConfigRevision
	_, err = s.Preview(ctx, PreviewInput{SourceInput: in, ChannelID: 1, SourceID: src.ID})
	quotaStatus(t, err, 400)
	in.Enabled = true
	_, err = s.Update(ctx, src.ID, in)
	quotaStatus(t, err, 400)
	in.Enabled = false
	src, err = s.Update(ctx, src.ID, in)
	if err != nil || src.HasToken {
		t.Fatalf("adapter change reused previous secret: %+v %v", src, err)
	}
	fresh := "explicit-new-token"
	in.Enabled = true
	in.ExpectedRevision = src.ConfigRevision
	in.Credentials = &CredentialPatch{Token: &fresh}
	src, err = s.Update(ctx, src.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	s.execute = func(_ context.Context, _ HTTPDoer, _ Adapter, resolved ResolvedSource) (*Snapshot, *Failure) {
		if resolved.Token != fresh {
			t.Fatal("wrong adapter credential")
		}
		return &Snapshot{}, nil
	}
	if _, err = s.Refresh(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
}
func TestServiceMutationsCancelActiveRequestsAfterCommit(t *testing.T) {
	for _, kind := range []string{"update", "disable", "delete", "channel"} {
		t.Run(kind, func(t *testing.T) {
			s := quotaServiceTest(t)
			ctx := context.Background()
			in := quotaInput()
			in.Enabled = true
			src, err := s.Create(ctx, 1, in)
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			cancelled := make(chan struct{})
			s.execute = func(ctx context.Context, _ HTTPDoer, _ Adapter, _ ResolvedSource) (*Snapshot, *Failure) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return nil, &Failure{Category: "cancelled"}
			}
			done := make(chan error, 1)
			go func() { _, e := s.Refresh(ctx, src.ID); done <- e }()
			<-started
			switch kind {
			case "update", "disable":
				in.ExpectedRevision = src.ConfigRevision
				if kind == "disable" {
					in.Enabled = false
				} else {
					in.Name = "更名"
				}
				_, err = s.Update(ctx, src.ID, in)
			case "delete":
				err = s.Delete(ctx, src.ID)
			case "channel":
				err = s.db.Transaction(func(tx *gorm.DB) error { return InvalidateInheritedSourcesTx(tx, 1) })
				if err == nil {
					s.CancelChannel(1)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("mutation did not cancel old request")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancelled task did not finish")
			}
			if s.inProgress(src.ID) || len(s.slots) != 0 {
				t.Fatal("cancelled request retained slot")
			}
		})
	}
}
func TestServiceNoopRollbackAndOtherCallerDoNotCancelTask(t *testing.T) {
	s := quotaServiceTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := quotaInput()
	in.Enabled = true
	src, err := s.Create(ctx, 1, in)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	s.execute = func(ctx context.Context, _ HTTPDoer, _ Adapter, _ ResolvedSource) (*Snapshot, *Failure) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, &Failure{Category: "cancelled"}
	}
	done := make(chan error, 1)
	go func() { _, e := s.Refresh(ctx, src.ID); done <- e }()
	<-started
	other, stopOther := context.WithCancel(context.Background())
	view, err := s.Refresh(other, src.ID)
	stopOther()
	if err != nil || !view.State.InProgress {
		t.Fatal("busy view", err)
	}
	in.ExpectedRevision = src.ConfigRevision
	if _, err = s.Update(context.Background(), src.ID, in); err != nil {
		t.Fatal(err)
	}
	s.db.Callback().Delete().Before("gorm:delete").Register("test_state_rollback", func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_quota_states" {
			tx.AddError(errors.New("forced rollback"))
		}
	})
	in.Name = "rollback"
	_, err = s.Update(context.Background(), src.ID, in)
	s.db.Callback().Delete().Remove("test_state_rollback")
	if err == nil {
		t.Fatal("expected rollback")
	}
	select {
	case <-cancelled:
		t.Fatal("no-op, rollback or other caller cancelled active query")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("caller cancel did not finish")
	}
}

func TestServiceRejectsSecondSourcePerChannel(t *testing.T) {
	s := quotaServiceTest(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, 1, quotaInput()); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create(ctx, 1, quotaInput())
	quotaStatus(t, err, 409)
}

func TestDedupeSourcesKeepsMostRecentSuccess(t *testing.T) {
	s := quotaServiceTest(t)
	db := s.db
	if err := db.Migrator().DropIndex(&Source{}, "uniq_channel_quota_sources_channel_id"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	older := now.Add(-time.Hour)

	newSource := func(channelID int64, name string, updatedAt time.Time) Source {
		src := Source{ChannelID: channelID, Name: name, AdapterID: "deepseek.balance", CredentialMode: CredentialInherit, SchemaVersion: 1}
		if err := db.Create(&src).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&Source{}).Where("id = ?", src.ID).UpdateColumn("updated_at", updatedAt).Error; err != nil {
			t.Fatal(err)
		}
		return src
	}

	// 渠道 1：有成功记录的来源优先，即使它不是最后更新的。
	stale := newSource(1, "stale", now)
	succeeded := newSource(1, "succeeded", older)
	if err := db.Create(&State{SourceID: succeeded.ID, LastSuccessAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	// 渠道 2：都没有成功记录时保留最近更新的一条。
	oldUpdated := newSource(2, "old-updated", older)
	newUpdated := newSource(2, "new-updated", now)
	// 渠道 3：单来源不受影响。
	single := newSource(3, "single", older)

	if err := DedupeSources(db); err != nil {
		t.Fatal(err)
	}

	assertKept := func(channelID, wantID int64) {
		t.Helper()
		var sources []Source
		if err := db.Where("channel_id = ?", channelID).Find(&sources).Error; err != nil {
			t.Fatal(err)
		}
		if len(sources) != 1 || sources[0].ID != wantID {
			t.Fatalf("channel %d: want source %d, got %+v", channelID, wantID, sources)
		}
	}
	assertKept(1, succeeded.ID)
	assertKept(2, newUpdated.ID)
	assertKept(3, single.ID)

	for _, dropped := range []int64{stale.ID, oldUpdated.ID} {
		var states int64
		if err := db.Model(&State{}).Where("source_id = ?", dropped).Count(&states).Error; err != nil {
			t.Fatal(err)
		}
		if states != 0 {
			t.Fatalf("source %d state not cleaned", dropped)
		}
	}
}
