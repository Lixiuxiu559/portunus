package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/configtransfer"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTransferAPI(t *testing.T) (*gin.Engine, *configtransfer.Service, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "transfer-api.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&channel.Channel{}, &model.Model{}, &group.Group{}, &group.GroupItem{}); err != nil {
		t.Fatal(err)
	}
	s := configtransfer.NewService(db, "synthetic-instance")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	RegisterConfigTransfer(r.Group("/api"), s)
	return r, s, db
}
func transferRequest(r *gin.Engine, method, path, contentType string, body io.Reader, instance string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://127.0.0.1"+path, body)
	request.RemoteAddr = "127.0.0.1:52000"
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if instance != "" {
		request.Header.Set("X-Portunus-Instance", instance)
	}
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	return recorder
}
func assertTransferCode(t *testing.T, r *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status %d, want %d: %s", r.Code, status, r.Body.String())
	}
	var e configtransfer.Error
	if err := json.Unmarshal(r.Body.Bytes(), &e); err != nil || e.Code != code {
		t.Fatalf("response %s, want %s", r.Body.String(), code)
	}
	if r.Header().Get("X-Portunus-Instance") != "synthetic-instance" {
		t.Fatal("instance header omitted")
	}
}
func TestConfigTransferHTTPRoundTrip(t *testing.T) {
	r, _, db := setupTransferAPI(t)
	values := map[string]any{"name": "synthetic", "type": "openai", "base_url": "https://synthetic.invalid/legacy?a=1#fragment", "key": "synthetic-never-display-key", "connection_options": `{"schema_version":0,"model_discovery":""}`, "auto_sync": false, "thinking_compat": false, "adapter_id": "", "provider_id": "", "rate_limit_scope": ""}
	if err := db.Table("channels").Create(values).Error; err != nil {
		t.Fatal(err)
	}
	summary := transferRequest(r, "GET", "/api/config-transfer/summary", "", nil, "")
	if summary.Code != 200 {
		t.Fatal(summary.Body.String())
	}
	var sd configtransfer.Summary
	if err := json.Unmarshal(summary.Body.Bytes(), &sd); err != nil || !sd.Ready || !sd.CanExport || sd.Counts.Channels != 1 {
		t.Fatal(sd, err)
	}
	exported := transferRequest(r, "POST", "/api/config-transfer/exports", "application/json", strings.NewReader(`{"password":" a\u0000é ","app_version":"synthetic-app"}`), "synthetic-instance")
	if exported.Code != 200 {
		t.Fatal(exported.Body.String())
	}
	if exported.Header().Get("Content-Type") != "application/octet-stream" || exported.Header().Get("X-Portunus-Channels") != "1" || bytes.Contains(exported.Body.Bytes(), []byte("synthetic-never-display-key")) {
		t.Fatal("unsafe export response")
	}
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	writer.WriteField("password", " a\x00é ")
	part, _ := writer.CreateFormFile("file", "ignored.portunus")
	part.Write(exported.Body.Bytes())
	writer.Close()
	preview := transferRequest(r, "POST", "/api/config-transfer/previews", writer.FormDataContentType(), &b, "synthetic-instance")
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	if strings.Contains(preview.Body.String(), "synthetic.invalid") || strings.Contains(preview.Body.String(), "synthetic-never-display-key") {
		t.Fatal("preview leaked connection")
	}
	var dto configtransfer.PreviewDTO
	if err := json.Unmarshal(preview.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	commitBody, _ := json.Marshal(configtransfer.CommitRequest{PreviewID: dto.PreviewID, FileSHA256: dto.FileSHA256, PlanSHA256: dto.PlanSHA256})
	path := "/api/config-transfer/operations/" + dto.OperationID
	committed := transferRequest(r, "POST", path+"/commit", "application/json", bytes.NewReader(commitBody), "synthetic-instance")
	if committed.Code != 200 {
		t.Fatal(committed.Body.String())
	}
	var result configtransfer.ResultDTO
	json.Unmarshal(committed.Body.Bytes(), &result)
	if result.State != "committed" || result.Counts.Channels != 1 {
		t.Fatal(committed.Body.String())
	}
	queried := transferRequest(r, "GET", path, "", nil, "synthetic-instance")
	if queried.Code != 200 {
		t.Fatal(queried.Body.String())
	}
	json.Unmarshal(queried.Body.Bytes(), &result)
	if !result.Replayed {
		t.Fatal("result did not come from receipt")
	}
	list := transferRequest(r, "GET", "/api/config-transfer/operations?unacknowledged=1", "", nil, "synthetic-instance")
	if list.Code != 200 || !strings.Contains(list.Body.String(), dto.OperationID) || strings.Contains(list.Body.String(), "original_name") {
		t.Fatal(list.Body.String())
	}
	ack := transferRequest(r, "POST", path+"/ack", "", nil, "synthetic-instance")
	if ack.Code != 204 {
		t.Fatal(ack.Body.String())
	}
	var n int64
	db.Table("channels").Count(&n)
	if n != 2 {
		t.Fatal("unexpected object count", n)
	}
}

type observedBody struct{ reads int }

func (b *observedBody) Read(p []byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *observedBody) Close() error               { return nil }
func TestConfigTransferHTTPBoundaryAndAdmission(t *testing.T) {
	r, s, _ := setupTransferAPI(t)
	for _, entry := range []struct{ peer, host, origin string }{{"192.0.2.2:123", "127.0.0.1", ""}, {"127.0.0.1:123", "attacker.example", ""}, {"127.0.0.1:123", "127.0.0.1", "https://attacker.example"}} {
		request := httptest.NewRequest("GET", "http://"+entry.host+"/api/config-transfer/summary", nil)
		request.RemoteAddr = entry.peer
		request.Header.Set("Origin", entry.origin)
		recorder := httptest.NewRecorder()
		r.ServeHTTP(recorder, request)
		if recorder.Code != 403 || recorder.Header().Get("X-Portunus-Instance") != "synthetic-instance" {
			t.Fatal("management boundary failed", recorder.Code)
		}
	}
	body := &observedBody{}
	mismatch := transferRequest(r, "POST", "/api/config-transfer/previews", "multipart/form-data; boundary=a", body, "wrong-instance")
	assertTransferCode(t, mismatch, 409, "BACKEND_CHANGED")
	if body.reads != 0 {
		t.Fatal("read body before instance check")
	}
	_, release, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body = &observedBody{}
	busy := transferRequest(r, "POST", "/api/config-transfer/previews", "multipart/form-data; boundary=a", body, "synthetic-instance")
	release()
	assertTransferCode(t, busy, 423, "TRANSFER_BUSY")
	if body.reads != 0 {
		t.Fatal("read body before heavy admission")
	}
	tooLarge := transferRequest(r, "POST", "/api/config-transfer/exports", "application/json", strings.NewReader(strings.Repeat("x", configtransfer.MaxSmallBodyBytes+1)), "synthetic-instance")
	assertTransferCode(t, tooLarge, 413, "LIMIT_EXCEEDED")
	for _, invalid := range []string{`{"password":"x","Password":"x","app_version":"x"}`, `{"password":"x","app_version":null}`, `{"password":"\ud800","app_version":"x"}`, `{"password":"x","app_version":"x","app_version":"y"}`} {
		response := transferRequest(r, "POST", "/api/config-transfer/exports", "application/json", strings.NewReader(invalid), "synthetic-instance")
		assertTransferCode(t, response, 400, "INVALID_INPUT")
	}
	badList := transferRequest(r, "GET", "/api/config-transfer/operations?unacknowledged=1&limit=101", "", nil, "synthetic-instance")
	assertTransferCode(t, badList, 400, "INVALID_INPUT")
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	writer.WriteField("password", "x")
	writer.WriteField("password", "again")
	writer.Close()
	response := transferRequest(r, "POST", "/api/config-transfer/previews", writer.FormDataContentType(), &b, "synthetic-instance")
	assertTransferCode(t, response, 400, "INVALID_INPUT")
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/config-transfer/exports", strings.NewReader(`{}`))
	request.RemoteAddr = "127.0.0.1:123"
	request.Header.Set("X-Portunus-Instance", "synthetic-instance")
	request.Header.Set("Origin", "null")
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	if recorder.Code != 403 {
		t.Fatal("opaque browser origin accepted without local header")
	}
}

func TestConfigTransferListQueryContract(t *testing.T) {
	r, _, _ := setupTransferAPI(t)
	for _, query := range []string{
		"unacknowledged=1&unexpected=1", "unacknowledged=1&limit=bad",
		"unacknowledged=1&cursor=!", "unacknowledged=1&limit=1&limit=2",
	} {
		response := transferRequest(r, "GET", "/api/config-transfer/operations?"+query, "", nil, "synthetic-instance")
		assertTransferCode(t, response, 400, "INVALID_INPUT")
	}
	for _, query := range []string{"unacknowledged=1&limit=1", "unacknowledged=1&limit=100", "unacknowledged=1&cursor="} {
		response := transferRequest(r, "GET", "/api/config-transfer/operations?"+query, "", nil, "synthetic-instance")
		if response.Code != 200 {
			t.Fatal("documented list query rejected", query, response.Body.String())
		}
	}
}

func TestConfigTransferMultipartInclusivePartLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name          string
		passwordBytes int
		fileBytes     int
		wantLimit     bool
	}{
		{"maximum password", configtransfer.MaxPasswordBytes, 64, false},
		{"oversized password", configtransfer.MaxPasswordBytes + 1, 64, true},
		{"maximum file", 1, configtransfer.MaxFileBytes, false},
		{"oversized file", 1, configtransfer.MaxFileBytes + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			writer.WriteField("password", strings.Repeat("x", tc.passwordBytes))
			part, _ := writer.CreateFormFile("file", "synthetic.portunus")
			part.Write(make([]byte, tc.fileBytes))
			writer.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "http://127.0.0.1/synthetic", &body)
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			password, file, err := ctMultipart(c)
			if tc.wantLimit {
				var e *configtransfer.Error
				if !errors.As(err, &e) || e.Code != "LIMIT_EXCEEDED" {
					t.Fatal("oversized part accepted", err)
				}
			} else if err != nil || len(password) != tc.passwordBytes || len(file) != tc.fileBytes {
				t.Fatal("maximum valid part truncated or rejected", len(password), len(file), err)
			}
		})
	}
}
