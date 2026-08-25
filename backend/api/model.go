package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/model"
)

// registerModelRoutes 注册模型 CRUD 路由。
func registerModelRoutes(r *gin.RouterGroup) {
	g := r.Group("/models")
	g.GET("", listModels)
	g.GET("/:id", getModel)
	g.POST("", createModel)
	g.PUT("/:id", updateModel)
	g.DELETE("/:id", deleteModel)
}

func listModels(c *gin.Context) {
	channelID := int64(0)
	if s := c.Query("channel_id"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			channelID = id
		}
	}
	ms, err := model.List(channelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := make([]model.Response, 0, len(ms))
	for i := range ms {
		resp = append(resp, ms[i].ToResponse())
	}
	c.JSON(http.StatusOK, resp)
}

func getModel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	m, err := model.Get(id)
	if err != nil {
		c.JSON(statusForErr(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m.ToResponse())
}

func createModel(c *gin.Context) {
	var req model.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	m, err := model.Create(req)
	if err != nil {
		c.JSON(statusForErr(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, m.ToResponse())
}

func updateModel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	var req model.UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	m, err := model.Update(id, req)
	if err != nil {
		c.JSON(statusForErr(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m.ToResponse())
}

func deleteModel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := model.Delete(id); err != nil {
		c.JSON(statusForErr(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
