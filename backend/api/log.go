package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// registerLogRoutes 注册日志查询与统计路由。
func registerLogRoutes(r *gin.RouterGroup) {
	g := r.Group("/logs")
	g.GET("", listLogs)
	g.GET("/stat", getLogStats)
}

// parseLogFilter 从 query string 解析筛选条件。
func parseLogFilter(c *gin.Context) shared.LogFilter {
	f := shared.LogFilter{}
	if v := c.Query("api_key_id"); v != "" {
		f.APIKeyID, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := c.Query("channel_id"); v != "" {
		f.ChannelID, _ = strconv.ParseInt(v, 10, 64)
	}
	f.GroupName = c.Query("group_name")
	f.ModelName = c.Query("model_name")
	if v := c.Query("success"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			f.Success = &b
		}
	}
	if v := c.Query("start_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.StartTime = &t
		}
	}
	if v := c.Query("end_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.EndTime = &t
		}
	}
	f.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	f.PageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
	return f
}

func listLogs(c *gin.Context) {
	logs, total, err := shared.ListLogs(parseLogFilter(c))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "data": logs})
}

func getLogStats(c *gin.Context) {
	s, err := shared.LogStatsBy(parseLogFilter(c))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, s)
}
