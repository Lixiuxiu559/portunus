package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/group"
)

// registerGroupRoutes 注册分组 CRUD 及分组项管理路由。
func registerGroupRoutes(r *gin.RouterGroup) {
	g := r.Group("/groups")
	g.GET("", listGroups)
	g.GET("/:id", getGroup)
	g.POST("", createGroup)
	g.PUT("/:id", updateGroup)
	g.DELETE("/:id", deleteGroup)
	g.POST("/:id/items", addGroupItem)
	g.PUT("/:id/items/:item_id", updateGroupItem)
	g.DELETE("/:id/items/:item_id", deleteGroupItem)
}

func listGroups(c *gin.Context) {
	gs, err := group.List()
	if err != nil {
		respondError(c, err)
		return
	}
	resp := make([]group.Response, 0, len(gs))
	for i := range gs {
		resp = append(resp, gs[i].ToResponse())
	}
	c.JSON(http.StatusOK, resp)
}

func getGroup(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	g, err := group.Get(id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, g.ToResponse())
}

func createGroup(c *gin.Context) {
	var req group.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	g, err := group.Create(req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, g.ToResponse())
}

func updateGroup(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	var req group.UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	g, err := group.Update(id, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, g.ToResponse())
}

func deleteGroup(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := group.Delete(id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func addGroupItem(c *gin.Context) {
	groupID, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	var req group.ItemAddRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	item, err := group.AddItem(groupID, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func updateGroupItem(c *gin.Context) {
	groupID, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	itemID, err := strconv.ParseInt(c.Param("item_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 item_id"})
		return
	}
	var req group.ItemUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不合法: " + err.Error()})
		return
	}
	item, err := group.UpdateItem(groupID, itemID, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func deleteGroupItem(c *gin.Context) {
	groupID, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	itemID, err := strconv.ParseInt(c.Param("item_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 item_id"})
		return
	}
	if err := group.DeleteItem(groupID, itemID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
