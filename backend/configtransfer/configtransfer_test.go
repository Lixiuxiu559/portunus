package configtransfer

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "synthetic.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&channel.Channel{}, &model.Model{}, &group.Group{}, &group.GroupItem{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	return db
}
func testService(t *testing.T, db *gorm.DB) *Service {
	t.Helper()
	s := NewService(db, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
func samplePayload() Payload {
	active := "i2"
	return Payload{FormatVersion: 1, AppVersion: "0.1.older", Channels: []Channel{
		{Ref: "c1", Name: "A", Icon: "unknown-preset", ProviderID: "future-display-only", ProfileID: "", AdapterID: "", Type: "openai", BaseURL: "https://synthetic.invalid/legacy?path=a#fragment", Key: "", ConnectionOptions: upstream.Options{}, AutoSync: false, ThinkingCompat: false, MaxConcurrentRequests: 0, RateLimitScope: ""},
		{Ref: "c2", Name: "A（导入）", Icon: "", ProviderID: "", AdapterID: "generic", Type: "anthropic", BaseURL: "https://synthetic.invalid/v1", Key: "synthetic-only-key", ConnectionOptions: upstream.Options{SchemaVersion: 1, ModelDiscovery: "manual"}, AutoSync: true, ThinkingCompat: true, MaxConcurrentRequests: MaxSafeID, RateLimitScope: "channel"},
	}, Models: []Model{{"m1", "c1", "same"}, {"m2", "c2", "same"}, {"m3", "c2", "unused"}}, Groups: []Group{
		{Ref: "g1", Name: "coding", Strategy: "manual", ActiveItemRef: &active, Items: []Item{{"i1", "m1", "-9223372036854775808"}, {"i2", "m2", "-9223372036854775808"}}},
		{Ref: "g2", Name: "empty", Strategy: "manual", Items: []Item{}},
		{Ref: "g3", Name: "standby", Strategy: "failover", Items: []Item{{"i3", "m2", "9223372036854775807"}}},
	}}
}
func sampleFile(t *testing.T) []byte {
	t.Helper()
	p := samplePayload()
	if err := Validate(&p); err != nil {
		t.Fatal(err)
	}
	b, err := payloadBytes(&p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Encrypt(b, []byte(" pass\x00组合é "))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func samplePreview(t *testing.T, s *Service) PreviewDTO {
	t.Helper()
	d, err := s.Preview(context.Background(), sampleFile(t), []byte(" pass\x00组合é "))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func confirmation(d PreviewDTO) CommitRequest {
	return CommitRequest{d.PreviewID, d.FileSHA256, d.PlanSHA256}
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}

func TestCodecAuthenticationAndPasswordBytes(t *testing.T) {
	plain := []byte(`{"test":"synthetic"}`)
	password := []byte(" x\x00e\u0301 ")
	file, err := Encrypt(plain, password)
	if err != nil {
		t.Fatal(err)
	}
	if len(file) != len(plain)+64 || binary.BigEndian.Uint32(file[44:48]) != uint32(len(plain)) {
		t.Fatal("envelope length")
	}
	decoded, err := Decrypt(file, password)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("round trip: %v", err)
	}
	next, _ := Encrypt(plain, password)
	if bytes.Equal(file[16:44], next[16:44]) {
		t.Fatal("salt/nonce reused")
	}
	for _, wrong := range [][]byte{[]byte("x\x00e\u0301"), []byte(" x\x00é "), []byte("wrong")} {
		_, err := Decrypt(file, wrong)
		assertCode(t, err, "DECRYPT_FAILED")
	}
	for _, offset := range []int{16, 32, 48, len(file) - 1} {
		mutated := append([]byte{}, file...)
		mutated[offset] ^= 1
		_, err := Decrypt(mutated, password)
		assertCode(t, err, "DECRYPT_FAILED")
	}
	for _, tc := range []struct {
		offset int
		value  byte
		code   string
	}{{8, 2, "FORMAT_UNSUPPORTED"}, {10, 2, "FORMAT_UNSUPPORTED"}, {12, 2, "CRYPTO_PROFILE_UNSUPPORTED"}, {13, 2, "CRYPTO_PROFILE_UNSUPPORTED"}, {14, 1, "CRYPTO_PROFILE_UNSUPPORTED"}, {15, 1, "CRYPTO_PROFILE_UNSUPPORTED"}} {
		mutated := append([]byte{}, file...)
		mutated[tc.offset] = tc.value
		_, err := Decrypt(mutated, password)
		assertCode(t, err, tc.code)
	}
	for _, b := range [][]byte{file[:len(file)-1], append(append([]byte{}, file...), 0), []byte("not an envelope")} {
		_, err := Decrypt(b, password)
		assertCode(t, err, "FILE_CORRUPT")
	}
	_, err = Encrypt(plain, nil)
	assertCode(t, err, "INVALID_INPUT")
	_, err = Encrypt(plain, []byte{0xff})
	assertCode(t, err, "INVALID_INPUT")
}

func TestStrictJSONAndRelations(t *testing.T) {
	p := samplePayload()
	source, _ := json.Marshal(p)
	mutations := map[string][]byte{
		"duplicate":     bytes.Replace(source, []byte(`"format_version":1`), []byte(`"format_version":1,"format_version":1`), 1),
		"case":          bytes.Replace(source, []byte(`"format_version"`), []byte(`"Format_Version"`), 1),
		"unknown":       bytes.Replace(source, []byte(`"format_version":1`), []byte(`"format_version":1,"extra":0`), 1),
		"missing":       bytes.Replace(source, []byte(`"auto_sync":false,`), nil, 1),
		"null bool":     bytes.Replace(source, []byte(`"auto_sync":false`), []byte(`"auto_sync":null`), 1),
		"null array":    bytes.Replace(source, []byte(`"items":[]`), []byte(`"items":null`), 1),
		"fraction":      bytes.Replace(source, []byte(`"format_version":1`), []byte(`"format_version":1.0`), 1),
		"exponent":      bytes.Replace(source, []byte(`"format_version":1`), []byte(`"format_version":1e0`), 1),
		"surrogate":     bytes.Replace(source, []byte(`"name":"A"`), []byte(`"name":"\ud800"`), 1),
		"low surrogate": bytes.Replace(source, []byte(`"name":"A"`), []byte(`"name":"\udc00"`), 1),
		"invalid utf8":  append(append([]byte{}, source...), 0xff),
		"trailing":      append(append([]byte{}, source...), []byte(` {}`)...),
	}
	for name, b := range mutations {
		t.Run(name, func(t *testing.T) {
			var decoded Payload
			if err := DecodeStrict(b, &decoded); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
	var decoded Payload
	if err := DecodeStrict(source, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := Validate(&decoded); err != nil {
		t.Fatal(err)
	}
	var password struct {
		Password string `json:"password"`
	}
	if err := DecodeStrict([]byte(`{"password":"a\u0000b"}`), &password); err != nil || password.Password != "a\x00b" {
		t.Fatal("raw UTF-8 password semantics lost")
	}
	for _, mutate := range []func(*Payload){func(p *Payload) { p.Models[0].ChannelRef = "absent" }, func(p *Payload) { p.Models = append(p.Models, p.Models[0]) }, func(p *Payload) { p.Groups[0].Items[1].ModelRef = "m1" }, func(p *Payload) { v := "i3"; p.Groups[0].ActiveItemRef = &v }, func(p *Payload) { p.Groups[0].Items[0].Priority = "-0" }, func(p *Payload) { p.Channels[0].Name = "A\x00B" }, func(p *Payload) { p.Channels[0].AdapterID = "unknown" }, func(p *Payload) { p.Channels[0].ConnectionOptions.SchemaVersion = 2 }} {
		p := samplePayload()
		mutate(&p)
		if Validate(&p) == nil {
			t.Fatal("bad reference or capability accepted")
		}
	}
}

func TestFullRoundTripPreservesLegacyAndOrder(t *testing.T) {
	db := testDB(t)
	s := testService(t, db)
	d := samplePreview(t, s)
	if strings.Contains(fmt.Sprint(d), "synthetic-only-key") || d.RenameCount != 0 {
		t.Fatal("unsafe preview")
	}
	var before int64
	db.Table("channels").Count(&before)
	if before != 0 {
		t.Fatal("preview wrote business data")
	}
	value, status, err := s.Commit(context.Background(), d.OperationID, confirmation(d))
	if err != nil || status != 200 {
		t.Fatalf("commit=%v %d", err, status)
	}
	result := value.(ResultDTO)
	if result.Counts != (Counts{2, 3, 3}) || result.Replayed {
		t.Fatal(result)
	}
	proof, proofErr := s.receipt(context.Background(), d.OperationID)
	if proofErr != nil || proof.ClaimStartedAtMs == nil || proof.CommittedAtMs == nil {
		t.Fatal("commit proof missing", proof, proofErr)
	}
	var c channel.Channel
	if err := db.Where("name = ?", "A").First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.AutoSync || c.ThinkingCompat || c.Key != "" || c.AdapterID != "" || c.ConnectionOptions != (upstream.Options{}) || c.RateLimitScope != "" || c.ProviderID != "future-display-only" {
		t.Fatalf("legacy fields changed: %+v", c)
	}
	file, counts, err := s.Export(context.Background(), []byte("\x00 password "), "newer-version")
	if err != nil || counts != (Counts{2, 3, 3}) {
		t.Fatal(err, counts)
	}
	plain, err := Decrypt(file, []byte("\x00 password "))
	if err != nil {
		t.Fatal(err)
	}
	var actual Payload
	if err := DecodeStrict(plain, &actual); err != nil {
		t.Fatal(err)
	}
	expected := samplePayload()
	expected.AppVersion = "newer-version"
	b1, _ := json.Marshal(expected)
	b2, _ := json.Marshal(actual)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("payload changed\n%s\n%s", b1, b2)
	}
	value, _, err = s.Commit(context.Background(), d.OperationID, confirmation(d))
	if err != nil || !value.(ResultDTO).Replayed {
		t.Fatal("same operation did not replay", err)
	}
	request := confirmation(d)
	request.PlanSHA256 = strings.Repeat("0", 64)
	_, _, err = s.Commit(context.Background(), d.OperationID, request)
	assertCode(t, err, "OPERATION_MISMATCH")
	list, err := s.RecoveryList(context.Background(), "", 20)
	if err != nil || len(list.Data) != 1 {
		t.Fatal(err, list)
	}
	if err := s.Ack(context.Background(), d.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(context.Background(), d.OperationID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.RecoveryList(context.Background(), "", 20)
	if len(list.Data) != 0 {
		t.Fatal("ACK did not suppress recovery")
	}
	d2 := samplePreview(t, s)
	if d2.OperationID == d.OperationID || d2.RenameCount != 5 || d2.Channels[0].Name != "A（导入 2）" || d2.Channels[1].Name != "A（导入）（导入）" {
		t.Fatal("new import/name reservations incorrect", d2)
	}
	_, _, err = s.Commit(context.Background(), d2.OperationID, confirmation(d2))
	if err != nil {
		t.Fatal(err)
	}
	db.Table("channels").Count(&before)
	if before != 4 {
		t.Fatal("new import did not add all objects")
	}
}

func TestTransactionRollbackAtFivePoints(t *testing.T) {
	file := sampleFile(t)
	for _, stage := range []string{"channels", "models", "groups", "items", "receipt"} {
		t.Run(stage, func(t *testing.T) {
			db := testDB(t)
			s := testService(t, db)
			d, err := s.Preview(context.Background(), file, []byte(" pass\x00组合é "))
			if err != nil {
				t.Fatal(err)
			}
			s.fault = func(at string) error {
				if at == stage {
					return errors.New("synthetic write failure")
				}
				return nil
			}
			value, status, err := s.Commit(context.Background(), d.OperationID, confirmation(d))
			if err != nil || status != 200 || value.(StateDTO).State != "aborted" {
				t.Fatal(value, status, err)
			}
			for _, table := range []string{"channels", "models", "groups", "group_items"} {
				var n int64
				db.Table(table).Count(&n)
				if n != 0 {
					t.Fatalf("%s retained %d rows", table, n)
				}
			}
			r, err := s.receipt(context.Background(), d.OperationID)
			if err != nil || r.ConfirmedAtMs == nil || r.Status != "aborted" || r.ClaimStartedAtMs != nil || r.counts() != (Counts{}) {
				t.Fatalf("receipt %+v %v", r, err)
			}
			_, _, err = s.Commit(context.Background(), d.OperationID, confirmation(d))
			assertCode(t, err, "OPERATION_ABORTED")
		})
	}
}

func TestSameOperationConcurrentAndResponseLoss(t *testing.T) {
	s := testService(t, testDB(t))
	d := samplePreview(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	s.fault = func(stage string) error {
		if stage == "channels" {
			close(entered)
			<-release
		}
		return nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var first any
	var firstErr error
	go func() {
		defer wg.Done()
		first, _, firstErr = s.Commit(context.Background(), d.OperationID, confirmation(d))
	}()
	<-entered
	value, status, err := s.Commit(context.Background(), d.OperationID, confirmation(d))
	if err != nil || status != 202 || value.(StateDTO).State != "prepared" {
		t.Fatal(value, status, err)
	}
	_, status, err = s.Recover(context.Background(), d.OperationID)
	if err != nil || status != 202 {
		t.Fatal("recover fenced running operation", status, err)
	}
	close(release)
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	// Discard the first response, then retrieve the persisted terminal proof.
	value, status, err = s.Result(context.Background(), d.OperationID)
	if err != nil || status != 200 || value.(ResultDTO).Counts != first.(ResultDTO).Counts {
		t.Fatal(value, status, err)
	}
}

func TestNameChangeRequiresNewOperationAndConfirmation(t *testing.T) {
	db := testDB(t)
	s := testService(t, db)
	d := samplePreview(t, s)
	if err := db.Table("groups").Create(map[string]any{"name": "coding", "strategy": "manual"}).Error; err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Commit(context.Background(), d.OperationID, confirmation(d))
	assertCode(t, err, "PREVIEW_CHANGED")
	replacement := err.(*Error).ReplacementPreview
	if replacement == nil || replacement.OperationID == d.OperationID || replacement.Groups[0].Name != "coding（导入）" {
		t.Fatal("missing replacement", err)
	}
	var n int64
	db.Table("channels").Count(&n)
	if n != 0 {
		t.Fatal("old plan wrote configuration")
	}
	r, _ := s.receipt(context.Background(), d.OperationID)
	if r.Status != "aborted" {
		t.Fatal(r)
	}
	_, _, err = s.Commit(context.Background(), replacement.OperationID, confirmation(*replacement))
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartupFenceExpiryAndEmptyRules(t *testing.T) {
	db := testDB(t)
	s := testService(t, db)
	_, _, err := s.Export(context.Background(), []byte("x"), "0.5.4")
	assertCode(t, err, "EMPTY_CONFIGURATION")
	empty := Payload{1, "x", []Channel{}, []Model{}, []Group{}}
	b, _ := json.Marshal(empty)
	file, _ := Encrypt(b, []byte("x"))
	_, err = s.Preview(context.Background(), file, []byte("x"))
	assertCode(t, err, "EMPTY_CONFIGURATION")
	d := samplePreview(t, s)
	now := time.Now().UnixMilli()
	db.Exec("UPDATE config_import_receipts SET confirmed_at_ms=? WHERE operation_id=?", now, d.OperationID)
	newOwner := testService(t, db)
	value, _, err := newOwner.Recover(context.Background(), d.OperationID)
	if err != nil || value.(StateDTO).State != "aborted" || value.(StateDTO).FailureCode != "SESSION_LOST" {
		t.Fatal(value, err)
	}
	_, _, err = s.Commit(context.Background(), d.OperationID, confirmation(d))
	assertCode(t, err, "OPERATION_ABORTED")
	d2 := samplePreview(t, newOwner)
	newOwner.mu.Lock()
	newOwner.previews[d2.PreviewID].deadline = time.Now().Add(-time.Second)
	newOwner.mu.Unlock()
	newOwner.expire(context.Background())
	_, _, err = newOwner.Commit(context.Background(), d2.OperationID, confirmation(d2))
	assertCode(t, err, "OPERATION_ABORTED")
}

func TestSchemaIsExactAndBusinessNamesAreBinary(t *testing.T) {
	db := testDB(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP INDEX idx_ct_unack_confirmed").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE INDEX idx_ct_unack_confirmed ON config_import_receipts(status)")
	assertCode(t, Migrate(db), "UNSUPPORTED_DATABASE_SCHEMA")
	db2 := testDB(t)
	db2.Exec("CREATE UNIQUE INDEX bad_name_nocase ON channels(name COLLATE NOCASE)")
	assertCode(t, Migrate(db2), "UNSUPPORTED_DATABASE_SCHEMA")
}

func TestAdmissionAndPreviewRetention(t *testing.T) {
	s := testService(t, testDB(t))
	_, release, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Export(context.Background(), []byte("x"), "x")
	assertCode(t, err, "TRANSFER_BUSY")
	release()
	d1 := samplePreview(t, s)
	_ = samplePreview(t, s)
	_, err = s.Preview(context.Background(), sampleFile(t), []byte(" pass\x00组合é "))
	assertCode(t, err, "LIMIT_EXCEEDED")
	if _, err := s.Discard(context.Background(), d1.PreviewID); err != nil {
		t.Fatal(err)
	}
	_ = samplePreview(t, s)
}

func TestServiceCancellationDoesNotReleaseRunningPayload(t *testing.T) {
	db := testDB(t)
	s := NewService(db, "")
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(lifetime); err != nil {
		t.Fatal(err)
	}
	d := samplePreview(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	s.fault = func(stage string) error {
		if stage == "channels" {
			close(entered)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, _, err := s.Commit(context.Background(), d.OperationID, confirmation(d)); done <- err }()
	<-entered
	cancel()
	deadline := time.After(time.Second)
	for !s.stopping.Load() {
		select {
		case <-deadline:
			t.Fatal("service did not stop")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	s.mu.Lock()
	retained := s.previews[d.PreviewID]
	if retained == nil || retained.payload == nil {
		s.mu.Unlock()
		t.Fatal("released active transaction payload")
	}
	s.mu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	n := len(s.previews)
	s.mu.Unlock()
	if n != 0 {
		t.Fatal("running preview retained after completion")
	}
}

func TestIndependentPoolsStartupFencingKeepsCommittedWinner(t *testing.T) {
	for _, journal := range []string{"DELETE", "WAL"} {
		t.Run(journal, func(t *testing.T) {
			db := testDB(t)
			db.Exec("PRAGMA journal_mode=" + journal)
			s := testService(t, db)
			d := samplePreview(t, s)
			sqlDB, _ := db.DB()
			var path string
			rows, err := sqlDB.Query("PRAGMA database_list")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var seq int
				var name, filename string
				rows.Scan(&seq, &name, &filename)
				if name == "main" {
					path = filename
				}
			}
			rows.Close()
			other, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			otherSQL, _ := other.DB()
			defer otherSQL.Close()
			entered, release := make(chan struct{}), make(chan struct{})
			s.fault = func(stage string) error {
				if stage == "channels" {
					close(entered)
					<-release
				}
				return nil
			}
			committed := make(chan error, 1)
			go func() { _, _, err := s.Commit(context.Background(), d.OperationID, confirmation(d)); committed <- err }()
			<-entered
			newOwner := NewService(other, "")
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan error, 1)
			go func() { started <- newOwner.Start(lifetime) }()
			time.Sleep(40 * time.Millisecond)
			close(release)
			if err := <-committed; err != nil {
				t.Fatal(err)
			}
			if err := <-started; err != nil {
				t.Fatal(err)
			}
			r, err := newOwner.receipt(context.Background(), d.OperationID)
			if err != nil || r.Status != "committed" {
				t.Fatal("startup overwrote committed proof", r, err)
			}
		})
	}
}

func TestSourceIntegrityAndBudgetsFailClosed(t *testing.T) {
	for _, kind := range []string{"orphan model", "orphan item", "missing active", "unknown option", "invalid unicode"} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			s := testService(t, db)
			switch kind {
			case "orphan model":
				db.Table("models").Create(map[string]any{"channel_id": 999, "name": "synthetic"})
			case "orphan item":
				db.Exec("PRAGMA foreign_keys=OFF")
				db.Table("group_items").Create(map[string]any{"group_id": 999, "model_id": 999, "priority": 0})
			case "missing active":
				db.Table("groups").Create(map[string]any{"name": "synthetic", "strategy": "manual", "active_item_id": 999})
			case "unknown option":
				db.Table("channels").Create(map[string]any{"name": "synthetic", "type": "openai", "base_url": "https://synthetic.invalid", "connection_options": `{"schema_version":1,"model_discovery":"api","extra":true}`})
			case "invalid unicode":
				db.Table("channels").Create(map[string]any{"name": string([]byte{0xff}), "type": "openai", "base_url": "https://synthetic.invalid"})
			}
			_, _, err := s.Export(context.Background(), []byte("x"), "x")
			assertCode(t, err, "SOURCE_INTEGRITY_ERROR")
		})
	}
	s := testService(t, testDB(t))
	p := samplePayload()
	p.Channels = append(p.Channels, make([]Channel, MaxChannels)...)
	assertCode(t, Validate(&p), "LIMIT_EXCEEDED")
	// Exact display budgets fail as a whole; no truncated preview is emitted.
	p = samplePayload()
	p.Models = []Model{}
	longName := strings.Repeat("x", MaxNameBytes)
	for i := 0; i < 600; i++ {
		p.Models = append(p.Models, Model{Ref: fmt.Sprintf("m%d", i), ChannelRef: "c1", Name: fmt.Sprintf("%04d", i) + longName[4:]})
	}
	assertCode(t, checkBudgets(&p), "LIMIT_EXCEEDED")
	plan, err := planNamespace([]rename{{Ref: "c", From: strings.Repeat("中", MaxNameBytes/3)}}, map[string]bool{strings.Repeat("中", MaxNameBytes/3): true})
	if err != nil || len(plan[0].To) > MaxNameBytes || !validString(plan[0].To, MaxNameBytes, true) {
		t.Fatal("name truncation broke UTF-8", err)
	}
	_, _, err = s.Result(context.Background(), newUUID())
	assertCode(t, err, "NOT_FOUND")
}

func TestFencingBeforeOldClaimPreventsAllBusinessWrites(t *testing.T) {
	for _, journal := range []string{"DELETE", "WAL"} {
		t.Run(journal, func(t *testing.T) {
			db := testDB(t)
			db.Exec("PRAGMA journal_mode=" + journal)
			old := testService(t, db)
			d := samplePreview(t, old)
			newOwner := testService(t, db)
			r, err := newOwner.receipt(context.Background(), d.OperationID)
			if err != nil || r.Status != "aborted" {
				t.Fatal(r, err)
			}
			_, _, err = old.Commit(context.Background(), d.OperationID, confirmation(d))
			assertCode(t, err, "OPERATION_ABORTED")
			for _, table := range []string{"channels", "models", "groups", "group_items"} {
				var n int64
				db.Table(table).Count(&n)
				if n != 0 {
					t.Fatal("fenced old owner wrote", table, n)
				}
			}
		})
	}
}

func TestResponseScopeHoldsHeavyLeaseUntilEncodingFinishes(t *testing.T) {
	s := testService(t, testDB(t))
	d := samplePreview(t, s)
	ctx, finish := s.ResponseScope(context.Background())
	defer finish()
	if _, _, err := s.Commit(ctx, d.OperationID, confirmation(d)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Acquire(context.Background()); err == nil {
		t.Fatal("heavy lease released before response encoding")
	}
	finish()
	_, release, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func FuzzDecodeStrictPayload(f *testing.F) {
	b, _ := json.Marshal(samplePayload())
	f.Add(b)
	f.Add([]byte(`{"format_version":1,"format_version":1}`))
	f.Add([]byte(`"\ud800"`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		var p Payload
		if DecodeStrict(b, &p) == nil {
			_ = Validate(&p)
		}
	})
}
