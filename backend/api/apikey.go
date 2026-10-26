package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func registerAPIKeyRoutes(r *gin.RouterGroup) {
	// 兼容旧客户端，返回/重置最早的有效 Key。
	r.GET("/apikey", getAPIKey)
	r.POST("/apikey/regenerate", regenerateAPIKey)
	g := r.Group("/apikeys")
	g.GET("", listAPIKeys)
	g.POST("", createAPIKey)
	g.PUT("/:id", renameAPIKey)
	g.POST("/:id/regenerate", regenerateNamedAPIKey)
	g.PUT("/:id/status", setAPIKeyStatus)
	g.DELETE("/:id", deleteAPIKey)
}

func getAPIKey(c *gin.Context) {
	k, err := shared.GetAPIKey()
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, k)
}

func regenerateAPIKey(c *gin.Context) {
	k, err := shared.RegenerateAPIKey()
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, k)
}

func listAPIKeys(c *gin.Context) {
	var keys []shared.APIKey
	var err error
	if c.Query("include_deleted") == "true" {
		keys, err = shared.ListHistoricalAPIKeys()
	} else {
		keys, err = shared.ListAPIKeys()
	}
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, keys)
}

func bindAPIKeyName(c *gin.Context) (string, bool) {
	var req struct {
		Name string `json:"name"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 API Key 名称"})
		return "", false
	}
	return req.Name, true
}

func apiKeyID(c *gin.Context) (int64, bool) {
	id, ok := parseID(c)
	if !ok || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 API Key ID"})
		return 0, false
	}
	return id, true
}

func createAPIKey(c *gin.Context) {
	name, ok := bindAPIKeyName(c)
	if !ok {
		return
	}
	k, err := shared.CreateAPIKey(name)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, k)
}

func renameAPIKey(c *gin.Context) {
	id, ok := apiKeyID(c)
	if !ok {
		return
	}
	name, ok := bindAPIKeyName(c)
	if !ok {
		return
	}
	k, err := shared.RenameAPIKey(id, name)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, k)
}

func regenerateNamedAPIKey(c *gin.Context) {
	id, ok := apiKeyID(c)
	if !ok {
		return
	}
	k, err := shared.RegenerateAPIKeyByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, k)
}

func deleteAPIKey(c *gin.Context) {
	id, ok := apiKeyID(c)
	if !ok {
		return
	}
	if err := shared.DeleteAPIKey(id); err != nil {
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func setAPIKeyStatus(c *gin.Context) {
	id, ok := apiKeyID(c)
	if !ok {
		return
	}
	var req struct {
		Revoked *bool `json:"revoked"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := c.ShouldBindJSON(&req); err != nil || req.Revoked == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供禁用状态 revoked（布尔值）"})
		return
	}
	k, err := shared.SetAPIKeyRevoked(id, *req.Revoked)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, k)
}
