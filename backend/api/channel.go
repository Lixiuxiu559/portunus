package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/quota"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// registerChannelRoutes 注册渠道 CRUD 路由。
func registerChannelRoutes(r *gin.RouterGroup) {
	g := r.Group("/channels")
	g.GET("", listChannels)
	g.GET("/:id", getChannel)
	g.GET("/:id/deletion-impact", previewChannelDeletionImpact)
	g.POST("", createChannel)
	g.POST("/preview-models", previewChannelModels)
	g.POST("/resolve", resolveChannelConnection)
	g.POST("/test", testChannelConnection)
	r.GET("/upstream-presets", listUpstreamPresets)
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
	var req struct {
		channel.CreateRequest
		Models []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	var ch *channel.Channel
	err := shared.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		ch, err = channel.CreateTx(tx, &req.CreateRequest)
		if err != nil {
			return err
		}
		_, err = model.ImportNamesTx(tx, ch.ID, req.Models)
		return err
	})
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
	var req struct {
		channel.UpdateRequest
		Models []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	var ch *channel.Channel
	keyChanged := false
	err := shared.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var previous channel.Channel
		if err := tx.Select("id", "key").First(&previous, id).Error; err != nil {
			return err
		}
		var err error
		ch, err = channel.UpdateTx(tx, id, req.UpdateRequest)
		if err != nil {
			return err
		}
		keyChanged = previous.Key != ch.Key
		if _, configured := c.Get("quota_service"); configured && keyChanged {
			if err := quota.InvalidateInheritedSourcesTx(tx, id); err != nil {
				return err
			}
		}
		_, err = model.ImportNamesTx(tx, ch.ID, req.Models)
		return err
	})
	if err != nil {
		respondError(c, err)
		return
	}
	if keyChanged {
		cancelChannelQuota(c, id)
	}
	c.JSON(http.StatusOK, ch.ToResponse())
}

func deleteChannel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	// 级联清理：先收集该渠道模型 ID，删引用它们的分组项，再删模型，最后删渠道。
	err := shared.DB.Transaction(func(tx *gorm.DB) error {
		if _, configured := c.Get("quota_service"); configured {
			if err := quota.DeleteByChannelTx(tx, id); err != nil {
				return err
			}
		}
		var modelIDs []int64
		if err := tx.Model(&model.Model{}).Where("channel_id = ?", id).Pluck("id", &modelIDs).Error; err != nil {
			return err
		}
		if err := group.DeleteItemsByModelIDsTx(tx, modelIDs); err != nil {
			return err
		}
		if err := model.DeleteByChannelTx(tx, id); err != nil {
			return err
		}
		return channel.DeleteTx(tx, id)
	})
	if err != nil {
		respondError(c, err)
		return
	}
	cancelChannelQuota(c, id)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func cancelChannelQuota(c *gin.Context, channelID int64) {
	if value, ok := c.Get("quota_service"); ok {
		value.(*quota.Service).CancelChannel(channelID)
	}
}

// previewChannelModels 只读预览某协议下的上游模型列表，不落库、不要求渠道已存在。
func previewChannelModels(c *gin.Context) {
	var req connectionDraft
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	connection, _, err := req.open(true)
	if err != nil {
		respondError(c, err)
		return
	}
	names, err := connection.FetchModels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "stage": "model_discovery"})
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
	added, err := model.SyncFromChannelContext(c.Request.Context(), ch)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"added": added})
}
