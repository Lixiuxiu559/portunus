package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/gateway"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
)

func TestNamedAPIKeyManagementAndAuth(t *testing.T) {
	setupCascadeTest(t)
	if err := shared.AutoMigrate(&shared.APIKey{}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	Register(r.Group("/api"))
	gateway.Register(r, gateway.Deps{AuthIdentity: shared.AuthenticateAPIKeyIdentity})
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr, req.Host = "127.0.0.1:12345", "localhost:3060"
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	decode := func(w *httptest.ResponseRecorder) shared.APIKey {
		t.Helper()
		var k shared.APIKey
		if err := json.Unmarshal(w.Body.Bytes(), &k); err != nil {
			t.Fatal(err)
		}
		return k
	}
	claude := decode(request("POST", "/api/apikeys", `{"name":"Claude Code"}`, 201))
	codex := decode(request("POST", "/api/apikeys", `{"name":"Codex"}`, 201))
	checkAuth := func(key, header string, status int) {
		t.Helper()
		req := httptest.NewRequest("POST", "/v1/messages", nil)
		if header == "Authorization" {
			key = "Bearer " + key
		}
		req.Header.Set(header, key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		// 有效 Key 到达 JSON 校验返回400；无效 Key 在鉴权返回401。
		if w.Code != status {
			t.Fatalf("gateway status=%d want=%d", w.Code, status)
		}
	}
	checkAuth(claude.Key, "x-api-key", 400)
	checkAuth(codex.Key, "Authorization", 400)
	path := fmt.Sprintf("/api/apikeys/%d", claude.ID)
	renamed := decode(request("PUT", path, `{"name":"工作 Claude"}`, 200))
	if renamed.ID != claude.ID || renamed.Key != claude.Key || renamed.Name != "工作 Claude" {
		t.Fatal("重命名未保留身份")
	}
	rotated := decode(request("POST", path+"/regenerate", "", 200))
	checkAuth(claude.Key, "x-api-key", 401)
	checkAuth(rotated.Key, "x-api-key", 400)
	checkAuth(codex.Key, "Authorization", 400)
	disabled := decode(request("PUT", path+"/status", `{"revoked":true}`, 200))
	if !disabled.Revoked {
		t.Fatal("未禁用")
	}
	checkAuth(rotated.Key, "Authorization", 401)
	enabled := decode(request("PUT", path+"/status", `{"revoked":false}`, 200))
	if enabled.Revoked || enabled.ID != claude.ID || enabled.Key != rotated.Key {
		t.Fatal("启用改变身份或密钥")
	}
	checkAuth(rotated.Key, "Authorization", 400)
	request("DELETE", path, "", 204)
	request("DELETE", path, "", 404)
	request("PUT", path+"/status", `{"revoked":false}`, 404)
	checkAuth(rotated.Key, "Authorization", 401)
	checkAuth(codex.Key, "Authorization", 400)
	request("POST", path+"/regenerate", "", 404)
	request("PUT", path, `{"name":"复活"}`, 404)
	var keys []shared.APIKey
	if err := json.Unmarshal(request("GET", "/api/apikeys", "", 200).Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].ID != codex.ID {
		t.Fatal("删除 Key 仍在管理列表")
	}
	if err := json.Unmarshal(request("GET", "/api/apikeys?include_deleted=true", "", 200).Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].Name != "工作 Claude" || !keys[0].DeletedAt.Valid {
		t.Fatal("删除 Key 历史身份丢失")
	}
	for _, key := range keys {
		if key.Key != "" {
			t.Fatal("历史筛选响应泄露明文凭据")
		}
	}
	legacy := decode(request("GET", "/api/apikey", "", 200))
	if legacy.ID != codex.ID {
		t.Fatal("旧接口应选取最早的有效 Key")
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/apikeys", `{}`, 400},
		{"POST", "/api/apikeys", `{"name":"  "}`, 400},
		{"POST", "/api/apikeys", `{"name":123}`, 400},
		{"POST", "/api/apikeys", `{"name":"` + strings.Repeat("a", 81) + `"}`, 400},
		{"POST", "/api/apikeys", `{`, 400},
		{"POST", "/api/apikeys", `{"name":"` + strings.Repeat("a", 4100) + `"}`, 400},
		{"PUT", fmt.Sprintf("/api/apikeys/%d/status", codex.ID), `{}`, 400},
		{"PUT", fmt.Sprintf("/api/apikeys/%d/status", codex.ID), `{"revoked":null}`, 400},
		{"PUT", fmt.Sprintf("/api/apikeys/%d/status", codex.ID), `{"revoked":"false"}`, 400},
		{"PUT", "/api/apikeys/-1", `{"name":"x"}`, 400},
		{"DELETE", "/api/apikeys/invalid", "", 400},
		{"DELETE", "/api/apikeys/999999", "", 404},
	} {
		request(tc.method, tc.path, tc.body, tc.status)
	}
}

func TestAPIKeyUsageFilterPreservesHistory(t *testing.T) {
	setupCascadeTest(t)
	if err := shared.AutoMigrate(&shared.APIKey{}); err != nil {
		t.Fatal(err)
	}
	if err := shared.InitLogDB(&shared.Config{}); err != nil {
		t.Fatal(err)
	}
	claude, err := shared.CreateAPIKey("Claude Code")
	if err != nil {
		t.Fatal(err)
	}
	codex, err := shared.CreateAPIKey("Claude Code")
	if err != nil {
		t.Fatal(err)
	}
	rows := []shared.Log{
		{APIKeyID: claude.ID, APIKeyName: "Claude Code", ModelName: "model-a", InputToken: 10, OutputToken: 3, CacheReadToken: 4, CacheWriteToken: 5},
		{APIKeyID: claude.ID, APIKeyName: "Claude Code", ModelName: "model-b", InputToken: 20, OutputToken: 7},
		{APIKeyID: codex.ID, APIKeyName: "Claude Code", ModelName: "model-a", InputToken: 99, OutputToken: 88},
	}
	if err := shared.LogDB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := shared.RenameAPIKey(claude.ID, "工作 Claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.RegenerateAPIKeyByID(claude.ID); err != nil {
		t.Fatal(err)
	}
	if err := shared.DeleteAPIKey(claude.ID); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerLogRoutes(r.Group("/api"))
	for _, tc := range []struct {
		suffix               string
		input, output, count int64
	}{
		{"", 30, 10, 2}, {"&model_name=model-a", 10, 3, 1},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/logs/stat?api_key_id=%d%s", claude.ID, tc.suffix), nil))
		var stats shared.LogStats
		if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || stats.InputTokens != tc.input || stats.OutputTokens != tc.output || stats.TotalRequests != tc.count || stats.CacheReadTokens != 4 || stats.CacheWriteTokens != 5 {
			t.Fatalf("Key 用量隔离错误: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/logs?api_key_id=%d", claude.ID), nil))
	var list struct {
		Total int64
		Data  []shared.Log
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || list.Total != 2 || len(list.Data) != 2 {
		t.Fatalf("Key 日志筛选错误: %s", w.Body.String())
	}
	for _, row := range list.Data {
		if row.APIKeyID != claude.ID || row.APIKeyName != "Claude Code" {
			t.Fatal("日志混入其他 Key")
		}
	}
}
