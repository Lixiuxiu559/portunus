package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/gin-gonic/gin"
)

func TestChannelIconAPIRoundTrip(t *testing.T) {
	setupCascadeTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerChannelRoutes(router.Group("/api"))
	send := func(method, path string, value any, want int) channel.Response {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var response channel.Response
		if want < 400 {
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return response
	}
	c := send(http.MethodPost, "/api/channels", map[string]any{"name": "icon-api", "provider_id": "deepseek", "key": "original-secret", "icon": "deepseek"}, http.StatusCreated)
	if c.Icon != "deepseek" {
		t.Fatalf("create icon = %q", c.Icon)
	}
	path := "/api/channels/" + strconv.FormatInt(c.ID, 10)
	if got := send(http.MethodPut, path, map[string]any{"name": "renamed"}, http.StatusOK); got.Icon != "deepseek" {
		t.Fatalf("omitted icon lost: %+v", got)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 96, 96))); err != nil {
		t.Fatal(err)
	}
	icon := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())
	if got := send(http.MethodPut, path, map[string]any{"icon": icon}, http.StatusOK); got.Icon != icon {
		t.Fatal("uploaded icon lost")
	}
	if got := send(http.MethodGet, path, nil, http.StatusOK); got.Icon != icon {
		t.Fatal("get icon lost")
	}
	send(http.MethodPut, path, map[string]any{"icon": "https://example.test/icon.png"}, http.StatusBadRequest)
	if got := send(http.MethodGet, path, nil, http.StatusOK); got.Icon != icon {
		t.Fatal("rejected icon replaced saved icon")
	}
	if got := send(http.MethodPut, path, map[string]any{"icon": ""}, http.StatusOK); got.Icon != "" {
		t.Fatal("icon not cleared")
	}
	saved, err := channel.Get(c.ID)
	if err != nil || saved.Key != "original-secret" || saved.ProviderID != "deepseek" {
		t.Fatalf("icon edit changed connection: %+v %v", saved, err)
	}
}
