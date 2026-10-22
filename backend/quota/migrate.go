package quota

import (
	"gorm.io/gorm"
)

// DedupeSources 把历史遗留的「一个渠道多个查询来源」收敛为一条。
//
// 业务含义：渠道就是一个上游账户，余额查询配置是单例。早期版本允许同一
// 渠道并列多个来源，现改为渠道级 1:1；升级时需在建立唯一索引前清理存量。
//
// 保留规则：最近成功查询的一条；都没有成功记录时保留最近更新的一条；
// 再相同则保留 ID 最大（最后创建）的一条。其余来源及其状态一并删除。
func DedupeSources(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Source{}) {
		return nil
	}
	var channelIDs []int64
	if err := db.Model(&Source{}).
		Select("channel_id").
		Group("channel_id").
		Having("COUNT(*) > 1").
		Pluck("channel_id", &channelIDs).Error; err != nil {
		return err
	}
	if len(channelIDs) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, channelID := range channelIDs {
			var sources []Source
			if err := tx.Where("channel_id = ?", channelID).Order("id").Find(&sources).Error; err != nil {
				return err
			}
			if len(sources) < 2 {
				continue
			}
			ids := make([]int64, 0, len(sources))
			for _, src := range sources {
				ids = append(ids, src.ID)
			}
			var states []State
			if err := tx.Where("source_id IN ?", ids).Find(&states).Error; err != nil {
				return err
			}
			stateByID := make(map[int64]State, len(states))
			for _, st := range states {
				stateByID[st.SourceID] = st
			}

			keep := sources[0]
			for _, candidate := range sources[1:] {
				if preferredSource(candidate, stateByID[candidate.ID], keep, stateByID[keep.ID]) {
					keep = candidate
				}
			}

			dropIDs := make([]int64, 0, len(sources)-1)
			for _, src := range sources {
				if src.ID != keep.ID {
					dropIDs = append(dropIDs, src.ID)
				}
			}
			if err := tx.Where("source_id IN ?", dropIDs).Delete(&State{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", dropIDs).Delete(&Source{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// preferredSource 判断 candidate 是否比 current 更值得保留。
func preferredSource(candidate Source, candidateState State, current Source, currentState State) bool {
	candidateAt, currentAt := candidateState.LastSuccessAt, currentState.LastSuccessAt
	if candidateAt != nil || currentAt != nil {
		if candidateAt == nil {
			return false
		}
		if currentAt == nil {
			return true
		}
		if !candidateAt.Equal(*currentAt) {
			return candidateAt.After(*currentAt)
		}
	}
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	return candidate.ID > current.ID
}
