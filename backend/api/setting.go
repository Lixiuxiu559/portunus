package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// registerSettingRoutes 注册全局设置路由。
func registerSettingRoutes(r *gin.RouterGroup) {
	g := r.Group("/settings")
	g.GET("", getSettings)
	g.PUT("/currency", setCurrency)
	g.PUT("/sync-interval", setSyncInterval)
	g.PUT("/log-retention-days", setLogRetentionDays)
	g.POST("/sync-now", syncAllChannels)
}

func getSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"currency":           shared.GetCurrency(),
		"sync_interval":      shared.GetSyncInterval(),
		"last_sync_at":       shared.GetLastSyncAt(),
		"log_retention_days": shared.GetLogRetentionDays(),
	})
}

func setCurrency(c *gin.Context) {
	var req struct {
		Currency shared.Currency `json:"currency" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	if err := shared.SetCurrency(req.Currency); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"currency": shared.GetCurrency()})
}

func setSyncInterval(c *gin.Context) {
	var req struct {
		SyncInterval *int `json:"sync_interval" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	if err := shared.SetSyncInterval(*req.SyncInterval); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sync_interval": shared.GetSyncInterval()})
}

// setLogRetentionDays 更新日志保留天数（0 = 禁用自动清理）。
func setLogRetentionDays(c *gin.Context) {
	var req struct {
		LogRetentionDays *int `json:"log_retention_days" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	if err := shared.SetLogRetentionDays(*req.LogRetentionDays); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"log_retention_days": shared.GetLogRetentionDays()})
}

// syncAllChannels 立即同步所有开启自动同步的渠道。
func syncAllChannels(c *gin.Context) {
	synced, added := model.SyncAutoChannels()
	now := time.Now()
	if err := shared.SetLastSyncAt(now); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"synced":       synced,
		"added":        added,
		"last_sync_at": now.Unix(),
	})
}
