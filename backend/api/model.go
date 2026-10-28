package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// registerModelRoutes 注册模型 CRUD 路由。
func registerModelRoutes(r *gin.RouterGroup) {
	g := r.Group("/models")
	g.GET("", listModels)
	g.GET("/:id", getModel)
	g.GET("/:id/deletion-impact", previewModelDeletionImpact)
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
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	ms, total, err := model.List(channelID, c.Query("name"), page, pageSize)
	if err != nil {
		respondError(c, err)
		return
	}
	resp := make([]model.Response, 0, len(ms))
	for i := range ms {
		resp = append(resp, ms[i].ToResponse())
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "data": resp})
}

func getModel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	m, err := model.Get(id)
	if err != nil {
		respondError(c, err)
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
		respondError(c, err)
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
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, m.ToResponse())
}

// deleteModel 在事务中删除模型及其全部分组成员，并清除指向这些成员的激活项。
// 允许删除被引用模型，不自动补位；分组与历史日志保留。
func deleteModel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	err := shared.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var target model.Model
		if err := tx.Select("id").First(&target, id).Error; err != nil {
			return err
		}
		if err := group.DeleteItemsByModelIDsTx(tx, []int64{id}); err != nil {
			return err
		}
		return model.DeleteTx(tx, id)
	})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
