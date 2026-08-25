package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// registerSettingRoutes 注册全局设置路由。
func registerSettingRoutes(r *gin.RouterGroup) {
	g := r.Group("/settings")
	g.GET("", getSettings)
	g.PUT("/currency", setCurrency)
}

func getSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"currency": shared.GetCurrency()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"currency": shared.GetCurrency()})
}
