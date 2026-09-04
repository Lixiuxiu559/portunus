package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// registerAPIKeyRoutes 注册 API Key 管理路由。
// 单 key 场景：仅提供「读」与「重新生成」两个动作。
func registerAPIKeyRoutes(r *gin.RouterGroup) {
	g := r.Group("/apikey")
	g.GET("", getAPIKey)
	g.POST("/regenerate", regenerateAPIKey)
}

// apiKeyResponse 是 API Key 的对外响应，返回完整 key 便于复制。
type apiKeyResponse struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
}

func toAPIKeyResponse(k *shared.APIKey) apiKeyResponse {
	return apiKeyResponse{ID: k.ID, Key: k.Key, CreatedAt: k.CreatedAt}
}

func getAPIKey(c *gin.Context) {
	k, err := shared.GetAPIKey()
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAPIKeyResponse(k))
}

func regenerateAPIKey(c *gin.Context) {
	k, err := shared.RegenerateAPIKey()
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAPIKeyResponse(k))
}