package api

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestPingInstanceIdentity(t *testing.T) {
	for _, instance := range []string{"", "dev-instance"} {
		t.Run(instance, func(t *testing.T) {
			t.Setenv("PORTUNUS_INSTANCE_ID", instance)
			r := gin.New()
			Register(r.Group("/api"))
			request := httptest.NewRequest("GET", "http://127.0.0.1:13061/api/ping", nil)
			request.RemoteAddr = "127.0.0.1:12345"
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			if response.Code != 200 || response.Header().Get("X-Portunus-Instance") != instance || response.Body.String() != `{"message":"pong"}` {
				t.Fatalf("unexpected ping: %d %v %s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}
