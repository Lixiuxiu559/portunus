package gateway

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// Register 注册对外 /v1 LLM 接口，供 claude code / codex 等客户端调用。
func Register(r *gin.Engine) {
	v1 := r.Group("/v1")
	v1.Use(authMiddleware())

	v1.GET("/models", listModels) // OpenAI 兼容模型列表
	v1.POST("/chat/completions", handleRelay(protocol.ProviderOpenAI))
	v1.POST("/responses", handleRelay(protocol.ProviderOpenAIResponses))
	v1.POST("/messages", handleRelay(protocol.ProviderAnthropic))
}

// authMiddleware 校验 API Key，并把 api_key_id 注入上下文。
// 兼容 Authorization: Bearer <key> 与 x-api-key 两种携带方式。
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractKey(c.GetHeader("Authorization"))
		if key == "" {
			key = c.GetHeader("x-api-key")
		}
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			return
		}

		var k shared.APIKey
		if err := shared.DB.Where("key = ? AND enabled = ?", key, true).First(&k).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}

		c.Set("api_key_id", k.ID)
		c.Next()
	}
}

// extractKey 从 Authorization 头提取 Bearer token。
func extractKey(header string) string {
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}

// modelEntry 是 /v1/models 返回的单个模型条目（OpenAI 格式）。
type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// listModels 返回分组名作为对外可用模型（OpenAI 格式），跳过空分组。
func listModels(c *gin.Context) {
	groups, err := group.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	data := make([]modelEntry, 0, len(groups))
	for _, g := range groups {
		if len(g.Items) == 0 {
			continue
		}
		data = append(data, modelEntry{
			ID:      g.Name,
			Object:  "model",
			Created: g.CreatedAt.Unix(),
			OwnedBy: "portunus",
		})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}
