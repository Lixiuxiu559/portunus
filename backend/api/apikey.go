package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// registerAPIKeyRoutes 注册 API Key 管理路由。
func registerAPIKeyRoutes(r *gin.RouterGroup) {
	g := r.Group("/apikeys")
	g.GET("", listAPIKeys)
	g.POST("", createAPIKey)
	g.PUT("/:id", updateAPIKey)
	g.DELETE("/:id", deleteAPIKey)
}

// apiKeyResponse 是 API Key 的对外响应，key 默认脱敏。
type apiKeyResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// toAPIKeyResponse 构造响应；full 为 true 时返回完整 key（仅创建时）。
func toAPIKeyResponse(k *shared.APIKey, full bool) apiKeyResponse {
	key := shared.MaskAPIKey(k.Key)
	if full {
		key = k.Key
	}
	return apiKeyResponse{ID: k.ID, Name: k.Name, Key: key, Enabled: k.Enabled, CreatedAt: k.CreatedAt}
}

func listAPIKeys(c *gin.Context) {
	ks, err := shared.ListAPIKeys()
	if err != nil {
		respondError(c, err)
		return
	}
	resp := make([]apiKeyResponse, 0, len(ks))
	for i := range ks {
		// 本地个人使用场景：列表直接返回完整 key，便于随时复制。
		resp = append(resp, toAPIKeyResponse(&ks[i], true))
	}
	c.JSON(http.StatusOK, resp)
}

func createAPIKey(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	k, err := shared.CreateAPIKey(req.Name)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toAPIKeyResponse(k, true))
}

func updateAPIKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	var req struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	k, err := shared.UpdateAPIKey(id, req.Name, req.Enabled)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAPIKeyResponse(k, false))
}

func deleteAPIKey(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := shared.DeleteAPIKey(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
