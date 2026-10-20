package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/gateway"
)

func TestManagementAccessBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"))
	for _, tc := range []struct {
		name, peer, host, origin, marker string
		want                             int
	}{
		{"IPv4", "127.0.0.1:1234", "127.0.0.1:3060", "", "", 200},
		{"IPv4 loopback subnet", "127.0.1.2:1234", "localhost:3060", "", "", 200},
		{"IPv6", "[::1]:1234", "[::1]:3060", "", "", 200},
		{"mapped IPv4", "[::ffff:127.0.0.1]:1234", "localhost:3060", "", "", 200},
		{"LAN", "192.168.1.20:1234", "127.0.0.1:3060", "", "", 403},
		{"public", "8.8.8.8:1234", "localhost:3060", "", "", 403},
		{"IPv6 LAN", "[fd00::1]:1234", "[::1]:3060", "", "", 403},
		{"mapped LAN", "[::ffff:192.168.1.20]:1234", "localhost:3060", "", "", 403},
		{"malformed peer", "127.0.0.1", "localhost:3060", "", "", 403},
		{"DNS rebinding", "127.0.0.1:1234", "evil.example:3060", "", "", 403},
		{"LAN authority", "127.0.0.1:1234", "192.168.1.20:3060", "", "", 403},
		{"cross site", "127.0.0.1:1234", "localhost:3060", "https://evil.example", "1", 403},
		{"malformed origin", "127.0.0.1:1234", "localhost:3060", "http://localhost:3060/path", "1", 403},
		{"origin credentials", "127.0.0.1:1234", "localhost:3060", "http://evil@localhost:3060", "1", 403},
		{"same origin", "127.0.0.1:1234", "localhost:3060", "http://localhost:3060", "", 200},
		{"Vite", "127.0.0.1:1234", "127.0.0.1:3060", "http://localhost:5173", "1", 200},
		{"Vite IPv6", "[::1]:1234", "127.0.0.1:3060", "http://[::1]:5173", "1", 200},
		{"other loopback origin", "127.0.0.1:1234", "localhost:3060", "http://localhost:9000", "1", 403},
		{"file Electron", "127.0.0.1:1234", "127.0.0.1:13060", "null", "1", 200},
		{"opaque browser form", "127.0.0.1:1234", "127.0.0.1:13060", "null", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
			req.RemoteAddr, req.Host = tc.peer, tc.host
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Portunus-Local", tc.marker)
			// 任何转发头都不应改变真实 TCP 对端判断。
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			req.Header.Set("X-Real-IP", "127.0.0.1")
			req.Header.Set("Forwarded", "for=127.0.0.1;host=localhost")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("管理 API 不应开放跨站读取")
			}
		})
	}
}

func TestManagementRemoteCannotReachAnyRegisteredHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"))
	// 无数据库：若边界失效，管理 handler 会访问数据库而不是返回 403。
	for _, route := range r.Routes() {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			req := httptest.NewRequest(route.Method, route.Path, nil)
			req.RemoteAddr, req.Host = "192.168.1.20:54321", "localhost:3060"
			req.Header.Set("Authorization", "Bearer existing-service-key")
			req.Header.Set("X-Portunus-Local", "1")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("远端管理请求状态=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestManagementBoundaryDoesNotAffectGatewayAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"))
	accepted := false
	gateway.Register(r, gateway.Deps{Auth: func(key string) (int64, bool) {
		accepted = key == "existing-service-key"
		return 1, accepted
	}})
	// 不触发真实模型调用：无效 JSON 应在鉴权通过后由网关返回 400。
	for _, key := range []string{"", "existing-service-key"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.RemoteAddr, req.Host = "192.168.1.20:1234", "192.168.1.10:3060"
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		want := http.StatusUnauthorized
		if key != "" {
			want = http.StatusBadRequest
			if !accepted {
				t.Fatal("远端有效服务令牌未被接受")
			}
		}
		if w.Code != want {
			t.Fatalf("远端 /v1 status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
	}
}
