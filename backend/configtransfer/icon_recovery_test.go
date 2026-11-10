package configtransfer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"gorm.io/gorm"
)

func syntheticPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 16), G: uint8(y % 16), B: 160, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func seedIconChannel(t *testing.T, db *gorm.DB, icon string) {
	t.Helper()
	c := samplePayload().Channels[0]
	options, err := json.Marshal(c.ConnectionOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("channels").Create(map[string]any{
		"name": c.Name, "icon": icon, "provider_id": c.ProviderID,
		"profile_id": c.ProfileID, "adapter_id": c.AdapterID, "type": c.Type,
		"base_url": c.BaseURL, "key": c.Key, "connection_options": string(options),
		"auto_sync": c.AutoSync, "thinking_compat": c.ThinkingCompat,
		"max_concurrent_requests": c.MaxConcurrentRequests, "rate_limit_scope": c.RateLimitScope,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestCustomPNGIconTransferRoundTrip(t *testing.T) {
	ctx := context.Background()
	icon := "data:image/png;base64," + base64.StdEncoding.EncodeToString(syntheticPNG(t, 256, 256))
	sourceDB := testDB(t)
	seedIconChannel(t, sourceDB, icon)
	source := testService(t, sourceDB)
	password := []byte("synthetic-icon-password")
	file, counts, err := source.Export(ctx, password, "synthetic-icon-version")
	if err != nil || counts != (Counts{Channels: 1}) {
		t.Fatal(counts, err)
	}
	plain, err := Decrypt(file, password)
	if err != nil {
		t.Fatal(err)
	}
	var payload Payload
	if err := DecodeStrict(plain, &payload); err != nil || len(payload.Channels) != 1 || payload.Channels[0].Icon != icon {
		t.Fatal("export changed custom PNG", err)
	}
	if bytes.Contains(file, []byte(icon)) {
		t.Fatal("encrypted export exposed the original image data URI")
	}
	targetDB := testDB(t)
	target := testService(t, targetDB)
	preview, err := target.Preview(ctx, file, password)
	if err != nil || preview.Counts != counts {
		t.Fatal(preview.Counts, err)
	}
	publicPreview, err := json.Marshal(preview)
	if err != nil || bytes.Contains(publicPreview, []byte("data:image/png")) || bytes.Contains(publicPreview, []byte(icon)) {
		t.Fatal("preview exposed image bytes", err)
	}
	value, status, err := target.Commit(ctx, preview.OperationID, confirmation(preview))
	if err != nil || status != 200 || value.(ResultDTO).Counts != counts {
		t.Fatal(value, status, err)
	}
	var restored channel.Channel
	if err := targetDB.Where("name = ?", payload.Channels[0].Name).First(&restored).Error; err != nil {
		t.Fatal(err)
	}
	if restored.Icon != icon {
		t.Fatal("import changed the custom PNG data URI")
	}
	var sourceCount int64
	if err := sourceDB.Table("channels").Count(&sourceCount).Error; err != nil || sourceCount != 1 {
		t.Fatal("export changed source configuration", sourceCount, err)
	}
}

func TestInvalidCustomPNGIconsRejectWholeTransfer(t *testing.T) {
	valid := syntheticPNG(t, 16, 16)
	// The header remains readable; full decoding must still reject missing IEND.
	truncated := valid[:len(valid)-12]
	if _, err := png.DecodeConfig(bytes.NewReader(truncated)); err != nil {
		t.Fatal("synthetic corrupt image does not reach full-image validation", err)
	}
	for _, tc := range []struct {
		name string
		icon string
	}{
		{"invalid base64", "data:image/png;base64,!"},
		{"invalid PNG header", "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("synthetic invalid PNG"))},
		{"truncated PNG body", "data:image/png;base64," + base64.StdEncoding.EncodeToString(truncated)},
		{"width exceeds 256", "data:image/png;base64," + base64.StdEncoding.EncodeToString(syntheticPNG(t, 257, 1))},
		{"height exceeds 256", "data:image/png;base64," + base64.StdEncoding.EncodeToString(syntheticPNG(t, 1, 257))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			payload := samplePayload()
			payload.Channels[0].Icon = tc.icon
			plain, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			password := []byte("synthetic-invalid-icon")
			file, err := Encrypt(plain, password)
			if err != nil {
				t.Fatal(err)
			}
			targetDB := testDB(t)
			target := testService(t, targetDB)
			_, err = target.Preview(ctx, file, password)
			assertCode(t, err, "CONFIG_INVALID")
			if err.(*Error).Field != "channels[0].icon" {
				t.Fatal("invalid image did not identify the safe field path", err)
			}
			for _, table := range []string{"channels", "models", "groups", "group_items", "config_import_receipts"} {
				var count int64
				if err := targetDB.Table(table).Count(&count).Error; err != nil || count != 0 {
					t.Fatal("invalid image left partial configuration or a valid preview", table, count, err)
				}
			}
			sourceDB := testDB(t)
			seedIconChannel(t, sourceDB, tc.icon)
			source := testService(t, sourceDB)
			_, _, err = source.Export(ctx, password, "synthetic-invalid-icon")
			assertCode(t, err, "SOURCE_INTEGRITY_ERROR")
		})
	}
}

func TestRecoverPreparedPreviewLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		confirmed bool
		lost      bool
		wantState string
	}{
		{"live unconfirmed preview stays usable", false, false, "prepared"},
		{"confirmed idle preview is fenced", true, false, "aborted"},
		{"missing unconfirmed preview is fenced", false, true, "aborted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t)
			s := testService(t, db)
			preview := samplePreview(t, s)
			if tc.confirmed {
				if err := db.Exec("UPDATE config_import_receipts SET confirmed_at_ms=? WHERE operation_id=?", time.Now().UnixMilli(), preview.OperationID).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.lost {
				s.mu.Lock()
				s.releasePreviewLocked(preview.PreviewID)
				s.mu.Unlock()
			}
			value, status, err := s.Recover(ctx, preview.OperationID)
			if err != nil || status != 200 {
				t.Fatal(value, status, err)
			}
			state := value.(StateDTO)
			if state.State != tc.wantState || state.OperationID != preview.OperationID || state.Counts != (Counts{}) {
				t.Fatal("wrong recovered state", state)
			}
			proof, err := s.receipt(ctx, preview.OperationID)
			if err != nil || proof.Status != tc.wantState || proof.ClaimStartedAtMs != nil {
				t.Fatal("recovery changed transaction proof", proof, err)
			}
			if tc.wantState == "prepared" {
				assertPreviewRetention(t, s, 1)
				if proof.ConfirmedAtMs != nil || state.FailureCode != "" {
					t.Fatal("read-only recovery confirmed or aborted a live preview", proof, state)
				}
				result, status, err := s.Commit(ctx, preview.OperationID, confirmation(preview))
				if err != nil || status != 200 || result.(ResultDTO).State != "committed" {
					t.Fatal("live preview became unusable after recovery", result, status, err)
				}
			} else {
				assertPreviewRetention(t, s, 0)
				if state.FailureCode != "SESSION_LOST" || state.Stage != "recovery" {
					t.Fatal("recovery did not report a definite fence", state)
				}
				_, _, err := s.Commit(ctx, preview.OperationID, confirmation(preview))
				assertCode(t, err, "OPERATION_ABORTED")
				for _, table := range []string{"channels", "models", "groups", "group_items"} {
					var count int64
					if err := db.Table(table).Count(&count).Error; err != nil || count != 0 {
						t.Fatal("recovery wrote business configuration", table, count, err)
					}
				}
			}
		})
	}
}

func assertPreviewRetention(t *testing.T, s *Service, want int) {
	t.Helper()
	s.mu.Lock()
	count, retainedBytes := len(s.previews), s.retainedBytes
	complete := true
	for _, p := range s.previews {
		complete = complete && p.payload != nil && len(p.raw) > 0
	}
	s.mu.Unlock()
	if count != want || (want == 0 && retainedBytes != 0) || (want > 0 && (retainedBytes <= 0 || !complete)) {
		t.Fatalf("preview retention: count=%d bytes=%d complete=%t, want count=%d", count, retainedBytes, complete, want)
	}
}

func TestRecoverConfirmedPreviewsReleaseBothSlots(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := testService(t, db)
	previews := []PreviewDTO{samplePreview(t, s), samplePreview(t, s)}
	assertPreviewRetention(t, s, 2)
	for _, preview := range previews {
		if err := db.Exec("UPDATE config_import_receipts SET confirmed_at_ms=? WHERE operation_id=?", time.Now().UnixMilli(), preview.OperationID).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, preview := range previews {
		value, status, err := s.Recover(ctx, preview.OperationID)
		if err != nil || status != 200 || value.(StateDTO).State != "aborted" {
			t.Fatal(value, status, err)
		}
		assertPreviewRetention(t, s, len(previews)-i-1)
	}
	// Two completed recoveries must not consume the service's two-preview budget.
	next := samplePreview(t, s)
	assertPreviewRetention(t, s, 1)
	if _, err := s.Discard(ctx, next.PreviewID); err != nil {
		t.Fatal(err)
	}
	assertPreviewRetention(t, s, 0)
}

func TestRecoverTerminalReceiptReleasesAllResidualPreviews(t *testing.T) {
	for _, terminal := range []string{"aborted", "committed"} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			s := testService(t, testDB(t))
			preview := samplePreview(t, s)
			s.mu.Lock()
			original := s.previews[preview.PreviewID]
			payload := original.payload
			backup := append([]byte{}, original.raw...)
			s.mu.Unlock()
			if terminal == "committed" {
				if _, _, err := s.Commit(ctx, preview.OperationID, confirmation(preview)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.fence(ctx, preview.OperationID, "SESSION_LOST", "recovery"); err != nil {
					t.Fatal(err)
				}
				s.mu.Lock()
				s.releasePreviewLocked(preview.PreviewID)
				s.mu.Unlock()
			}
			// Simulate retained copies from an interrupted earlier cleanup. Both
			// refer to the same proven terminal operation, with separate raw buffers.
			var residuals []*retainedPreview
			var buffers [][]byte
			s.mu.Lock()
			for i := 0; i < 2; i++ {
				dto := preview
				dto.PreviewID = newPreviewID()
				raw := append([]byte{}, backup...)
				residual := &retainedPreview{dto: dto, payload: payload, raw: raw, deadline: time.Now().Add(PreviewTTL)}
				s.previews[dto.PreviewID] = residual
				s.retainedBytes += len(raw)
				residuals = append(residuals, residual)
				buffers = append(buffers, raw)
			}
			s.mu.Unlock()
			clear(backup)
			assertPreviewRetention(t, s, 2)
			value, status, err := s.Recover(ctx, preview.OperationID)
			if err != nil || status != 200 {
				t.Fatal(value, status, err)
			}
			if terminal == "committed" {
				if result, ok := value.(ResultDTO); !ok || result.State != terminal || !result.Replayed {
					t.Fatal("committed recovery lost its terminal result", value)
				}
			} else if state, ok := value.(StateDTO); !ok || state.State != terminal {
				t.Fatal("aborted recovery lost its terminal state", value)
			}
			assertPreviewRetention(t, s, 0)
			for i, residual := range residuals {
				if residual.payload != nil || residual.raw != nil || !bytes.Equal(buffers[i], make([]byte, len(buffers[i]))) {
					t.Fatal("terminal recovery kept a secret reference or uncleared buffer")
				}
			}
		})
	}
}

func TestRecoverCommittedKeepsHistoricalResult(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := testService(t, db)
	preview := samplePreview(t, s)
	value, status, err := s.Commit(ctx, preview.OperationID, confirmation(preview))
	if err != nil || status != 200 {
		t.Fatal(value, status, err)
	}
	want := value.(ResultDTO)
	if err := db.Table("groups").Where("id=?", want.Groups[0].ID).Update("name", "synthetic-name-changed-after-import").Error; err != nil {
		t.Fatal(err)
	}
	value, status, err = s.Recover(ctx, preview.OperationID)
	if err != nil || status != 200 {
		t.Fatal(value, status, err)
	}
	want.Replayed = true
	if got, ok := value.(ResultDTO); !ok || !reflect.DeepEqual(got, want) {
		t.Fatal("recover did not return the original committed result", value, want)
	}
	var actualName string
	if err := db.Table("groups").Where("id=?", want.Groups[0].ID).Pluck("name", &actualName).Error; err != nil || actualName != "synthetic-name-changed-after-import" {
		t.Fatal("recover rewrote current configuration", actualName, err)
	}
	proof, err := s.receipt(ctx, preview.OperationID)
	if err != nil || proof.Status != "committed" || proof.counts() != want.Counts || proof.FailureCode != nil {
		t.Fatal("recover changed committed proof", proof, err)
	}
}

func TestRecoverDoesNotFenceRunningCommit(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := testService(t, db)
	preview := samplePreview(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	s.fault = func(stage string) error {
		if stage == "channels" {
			close(entered)
			<-release
		}
		return nil
	}
	type completion struct {
		value  any
		status int
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		value, status, err := s.Commit(ctx, preview.OperationID, confirmation(preview))
		done <- completion{value, status, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("commit did not reach its synthetic write barrier")
	}
	value, status, err := s.Recover(ctx, preview.OperationID)
	if err != nil || status != 202 || value.(StateDTO).State != "prepared" {
		t.Fatal("recover interrupted a running commit", value, status, err)
	}
	proof, err := s.receipt(ctx, preview.OperationID)
	if err != nil || proof.Status != "prepared" || proof.ConfirmedAtMs == nil || proof.FailureCode != nil {
		t.Fatal("recover fenced a live transaction", proof, err)
	}
	assertPreviewRetention(t, s, 1)
	unblock()
	var committed completion
	select {
	case committed = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("commit did not finish after its barrier was released")
	}
	if committed.err != nil || committed.status != 200 || committed.value.(ResultDTO).State != "committed" {
		t.Fatal(committed)
	}
	value, status, err = s.Recover(ctx, preview.OperationID)
	if err != nil || status != 200 || value.(ResultDTO).Counts != committed.value.(ResultDTO).Counts {
		t.Fatal("recover lost the committed outcome", value, status, err)
	}
	assertPreviewRetention(t, s, 0)
	for table, want := range map[string]int64{"channels": 2, "models": 3, "groups": 3, "group_items": 3} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil || count != want {
			t.Fatal("commit/recover race duplicated or lost configuration", table, count, want, err)
		}
	}
	if encoded, err := json.Marshal(value); err != nil || strings.Contains(string(encoded), "synthetic-only-key") {
		t.Fatal("recovery exposed credentials", err)
	}
}
