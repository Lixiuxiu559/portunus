package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// Register 注册管理 API 路由，供前端调用。
func Register(r *gin.RouterGroup) {
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	registerChannelRoutes(r) // 渠道 CRUD
	registerModelRoutes(r)   // 模型 CRUD
	registerGroupRoutes(r)   // 分组 CRUD
	registerSettingRoutes(r) // 全局设置
	registerAPIKeyRoutes(r)  // API Key（单 key：读 + 重新生成）
	registerLogRoutes(r)     // 日志查询与统计
}

// parseID 解析路径参数中的 id。
func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil
}

// classifyError 把错误归类为状态码 + 对用户安全的消息，避免把 DB 报错暴露给客户端。
func classifyError(err error) (int, string) {
	var se *shared.StatusError
	if errors.As(err, &se) {
		return se.Status, se.Message
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound, "记录不存在"
	}
	if isUniqueViolation(err) {
		return http.StatusConflict, "记录已存在"
	}
	return http.StatusInternalServerError, "内部错误"
}

// respondError 统一写出错误响应。
func respondError(c *gin.Context, err error) {
	status, msg := classifyError(err)
	c.JSON(status, gin.H{"error": msg})
}

// isUniqueViolation 判断是否为唯一约束冲突（如渠道名 / 模型名重复）。
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
