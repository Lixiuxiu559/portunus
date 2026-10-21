package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/quota"
)

// quotaAPI 仅做管理 HTTP 适配；网络执行、缓存与并发都归 quota.Service。
type quotaAPI struct{ service *quota.Service }

func registerQuotaRoutes(r *gin.RouterGroup, service *quota.Service) {
	a := quotaAPI{service: service}
	r.GET("/quota/adapters", func(c *gin.Context) { c.JSON(http.StatusOK, quota.Descriptors()) })
	r.GET("/quota/summaries", a.summaries)
	r.GET("/channels/:id/quota-sources", a.list)
	r.POST("/channels/:id/quota-sources", a.create)
	r.PUT("/quota-sources/:id", a.update)
	r.DELETE("/quota-sources/:id", a.delete)
	r.POST("/quota-sources/preview", a.preview)
	r.POST("/quota-sources/:id/refresh", a.refresh)
}

func quotaID(c *gin.Context) (int64, bool) {
	id, ok := parseID(c)
	if !ok || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return 0, false
	}
	return id, true
}

func bindQuota(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(target); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "余额查询配置格式不合法或超出大小限制"})
		return false
	}
	return true
}

func (a quotaAPI) list(c *gin.Context) {
	id, ok := quotaID(c)
	if !ok {
		return
	}
	result, err := a.service.List(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (a quotaAPI) create(c *gin.Context) {
	id, ok := quotaID(c)
	if !ok {
		return
	}
	var req quota.SourceInput
	if !bindQuota(c, &req) {
		return
	}
	result, err := a.service.Create(c.Request.Context(), id, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (a quotaAPI) update(c *gin.Context) {
	id, ok := quotaID(c)
	if !ok {
		return
	}
	var req quota.SourceInput
	if !bindQuota(c, &req) {
		return
	}
	result, err := a.service.Update(c.Request.Context(), id, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (a quotaAPI) delete(c *gin.Context) {
	id, ok := quotaID(c)
	if !ok {
		return
	}
	if err := a.service.Delete(c.Request.Context(), id); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (a quotaAPI) preview(c *gin.Context) {
	var req quota.PreviewInput
	if !bindQuota(c, &req) {
		return
	}
	result, err := a.service.Preview(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	status := http.StatusOK
	if result.InProgress {
		status = http.StatusAccepted
		c.Header("Retry-After", "2")
	}
	c.JSON(status, result)
}

func (a quotaAPI) refresh(c *gin.Context) {
	id, ok := quotaID(c)
	if !ok {
		return
	}
	result, err := a.service.Refresh(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	status := http.StatusOK
	if result.State.InProgress {
		status = http.StatusAccepted
		c.Header("Retry-After", "2")
	}
	c.JSON(status, result)
}

func (a quotaAPI) summaries(c *gin.Context) {
	parts := strings.Split(c.Query("channel_ids"), ",")
	if len(parts) > 100 || len(parts) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "每次须指定 1–100 个渠道 ID"})
		return
	}
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "渠道 ID 不合法"})
			return
		}
		ids = append(ids, id)
	}
	result, err := a.service.Summaries(c.Request.Context(), ids)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
