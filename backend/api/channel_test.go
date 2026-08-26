package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPreviewChannelModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"gpt-4o","object":"model"},{"id":"gpt-4o-mini","object":"model"}]}`)
	}))
	defer srv.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/channels/preview-models", previewChannelModels)

	body := `{"type":"openai","base_url":"` + srv.URL + `","key":"test-key"}`
	req := httptest.NewRequest(http.MethodPost, "/channels/preview-models", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Models) != 2 || resp.Models[0] != "gpt-4o" || resp.Models[1] != "gpt-4o-mini" {
		t.Errorf("models = %v, want [gpt-4o gpt-4o-mini]", resp.Models)
	}
}

func TestPreviewChannelModelsInvalidType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/channels/preview-models", previewChannelModels)

	body := `{"type":"unknown","base_url":"https://example.com","key":"k"}`
	req := httptest.NewRequest(http.MethodPost, "/channels/preview-models", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}