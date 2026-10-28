package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/quota"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// deletionImpactResponse 是删除影响预览响应。
// 模型预览只含 groups；渠道预览另含 model_count 与 has_quota_source。
type deletionImpactResponse struct {
	Groups         []group.DeletionImpact `json:"groups"`
	ModelCount     *int                   `json:"model_count,omitempty"`
	HasQuotaSource *bool                  `json:"has_quota_source,omitempty"`
}

// previewModelDeletionImpact 只读预览删除单个模型的影响：受影响分组、将移除成员、
// 激活项是否清除、删除后是否空组。目标不存在返回 404，非法 ID 返回 400。
func previewModelDeletionImpact(c *gin.Context) {
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
	impacts, err := group.PreviewDeletionImpact(shared.DB.WithContext(c.Request.Context()), []int64{m.ID})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, deletionImpactResponse{Groups: impacts})
}

// previewChannelDeletionImpact 只读预览删除渠道的影响：受影响分组、将移除成员、
// 激活项是否清除、删除后是否空组，以及渠道模型数量与余额来源提示。
func previewChannelDeletionImpact(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if _, err := channel.Get(id); err != nil {
		respondError(c, err)
		return
	}
	db := shared.DB.WithContext(c.Request.Context())
	modelIDs := make([]int64, 0)
	if err := db.Model(&model.Model{}).Where("channel_id = ?", id).Pluck("id", &modelIDs).Error; err != nil {
		respondError(c, err)
		return
	}
	impacts, err := group.PreviewDeletionImpact(db, modelIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	// 与渠道删除使用同一判断：只有注入了 quota_service 时删除才会清理余额来源，
	// 预览不能承诺一个删除路径不会执行的动作。
	hasQuotaSource := false
	if _, configured := c.Get("quota_service"); configured && db.Migrator().HasTable(&quota.Source{}) {
		var count int64
		if err := db.Model(&quota.Source{}).Where("channel_id = ?", id).Count(&count).Error; err != nil {
			respondError(c, err)
			return
		}
		hasQuotaSource = count > 0
	}
	modelCount := len(modelIDs)
	c.JSON(http.StatusOK, deletionImpactResponse{
		Groups:         impacts,
		ModelCount:     &modelCount,
		HasQuotaSource: &hasQuotaSource,
	})
}
