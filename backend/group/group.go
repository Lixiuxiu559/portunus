package group

import "time"

// Strategy 是分组的请求路由策略，每个分组必配其一。
type Strategy string

const (
	StrategyManual     Strategy = "manual"      // 手动指定：只打激活项
	StrategyRoundRobin Strategy = "round_robin" // 轮询：组内轮流
	StrategyFailover   Strategy = "failover"    // 故障转移：按 priority 顺序，失败换下一个
)

// Valid 校验策略枚举。
func (s Strategy) Valid() bool {
	switch s {
	case StrategyManual, StrategyRoundRobin, StrategyFailover:
		return true
	default:
		return false
	}
}

// Group 是客户端模型名称（对外暴露的名字），内部聚合多个渠道模型。
type Group struct {
	ID           int64       `gorm:"primaryKey" json:"id"`
	Name         string      `gorm:"unique;not null" json:"name"` // 对外模型名
	Strategy     Strategy    `gorm:"not null;default:manual" json:"strategy"`
	ActiveItemID int64       `json:"active_item_id"` // manual 策略下当前激活项 ID
	Items        []GroupItem `gorm:"foreignKey:GroupID" json:"items,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// GroupItem 是分组内的一个模型项，引用 model 包中的模型（用 ModelID 关联）。
type GroupItem struct {
	ID       int64 `gorm:"primaryKey" json:"id"`
	GroupID  int64 `gorm:"not null;uniqueIndex:idx_group_model" json:"group_id"`
	ModelID  int64 `gorm:"not null;uniqueIndex:idx_group_model" json:"model_id"` // 指向 model.Model
	Priority int   `json:"priority"`                                             // failover 按升序尝试；round_robin 无意义
}
