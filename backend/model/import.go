package model

import (
	"strings"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxImportNames 限制一次模型导入的输入大小。
const MaxImportNames = 10000
const MaxModelNameBytes = 512

var ErrManualDiscovery = &shared.StatusError{Status: 400, Message: "该渠道使用手动模型管理，不支持自动发现"}

// ImportNamesTx 仅新增模型。冲突不更新，保留既有 ID、分组引用及历史数据。
// names 先整体校验再写入，调用方负责事务提交与回滚。
func ImportNamesTx(tx *gorm.DB, channelID int64, names []string) (int, error) {
	if channelID <= 0 || len(names) > MaxImportNames {
		return 0, ErrInvalid
	}
	rows := make([]Model, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > MaxModelNameBytes {
			return 0, ErrInvalid
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		rows = append(rows, Model{ChannelID: channelID, Name: name})
	}
	var count int64
	if err := tx.Model(&channel.Channel{}).Where("id = ?", channelID).Count(&count).Error; err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, ErrChannelNotFound
	}
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "channel_id"}, {Name: "name"}}, DoNothing: true}).CreateInBatches(&rows, 100)
	return int(result.RowsAffected), result.Error
}
