package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// registerChannelRoutes 注册渠道 CRUD 路由。
func registerChannelRoutes(r *gin.RouterGroup) {
	g := r.Group("/channels")
	g.GET("", listChannels)
	g.GET("/:id", getChannel)
	g.POST("", createChannel)
	g.POST("/preview-models", previewChannelModels)
	g.PUT("/:id", updateChannel)
	g.DELETE("/:id", deleteChannel)
	g.POST("/:id/sync", syncChannelModels)
}

func listChannels(c *gin.Context) {
	cs, err := channel.List()
	if err != nil {
		respondError(c, err)
		return
	}
	resp := make([]channel.Response, 0, len(cs))
	for i := range cs {
		resp = append(resp, cs[i].ToResponse())
	}
	c.JSON(http.StatusOK, resp)
}

func getChannel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	ch, err := channel.Get(id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, ch.ToResponse())
}

func createChannel(c *gin.Context) {
	var req channel.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	ch, err := channel.Create(req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, ch.ToResponse())
}

func updateChannel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	var req channel.UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	ch, err := channel.Update(id, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, ch.ToResponse())
}

func deleteChannel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := channel.Delete(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// previewChannelModels 只读预览某协议下的上游模型列表，不落库、不要求渠道已存在。
func previewChannelModels(c *gin.Context) {
	var req struct {
		Type    protocol.Provider `json:"type" binding:"required"`
		BaseURL string            `json:"base_url" binding:"required"`
		Key     string            `json:"key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	if !req.Type.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的协议类型"})
		return
	}
	names, err := model.FetchModelNames(req.Type, req.BaseURL, req.Key)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": names})
}

// syncChannelModels 一键拉取指定渠道的上游模型并同步到模型表。
func syncChannelModels(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	ch, err := channel.Get(id)
	if err != nil {
		respondError(c, err)
		return
	}
	added, err := model.SyncFromChannel(ch)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"added": added})
}
