package group

import "gorm.io/gorm"

// MigrateLegacyStrategies 将已移除的轮询策略收敛为固定顺序的故障转移。
// 仅改策略列，保留成员优先级、激活项和时间戳；重复执行不产生额外变更。
func MigrateLegacyStrategies(db *gorm.DB) error {
	return db.Model(&Group{}).Where("strategy = ?", "round_robin").
		UpdateColumn("strategy", StrategyFailover).Error
}
