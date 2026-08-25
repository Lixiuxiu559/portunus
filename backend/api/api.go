package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
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
	registerAPIKeyRoutes(r)  // API Key CRUD
	registerLogRoutes(r)     // 日志查询与统计
}

// parseID 解析路径参数中的 id。
func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil
}

// statusForErr 根据错误类型映射 HTTP 状态码。
func statusForErr(err error) int {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return http.StatusNotFound
	case errors.Is(err, channel.ErrInvalid),
		errors.Is(err, model.ErrInvalid),
		errors.Is(err, model.ErrChannelNotFound),
		errors.Is(err, group.ErrInvalid),
		errors.Is(err, group.ErrModelNotFound),
		errors.Is(err, shared.ErrAPIKeyInvalid):
		return http.StatusBadRequest
	case isUniqueViolation(err):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// isUniqueViolation 判断是否为唯一约束冲突（如渠道名 / 模型名重复）。
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
