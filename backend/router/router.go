package router

import (
	"errors"
	"sort"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
)

// Target 是路由命中的一个具体上游（模型 + 渠道）。
type Target struct {
	Model   model.Model
	Channel channel.Channel
}

// ErrNoActiveItem 表示 manual 策略下未指定激活项。
var ErrNoActiveItem = errors.New("分组未指定激活项")

// ErrEmptyGroup 表示分组没有可用模型。
var ErrEmptyGroup = errors.New("分组没有可用模型")

// Resolve 按分组策略返回有序目标列表。
// manual 返回单个目标；failover 返回全部目标（按 priority、分组项 ID 升序）。
func Resolve(g *group.Group) ([]Target, error) {
	if len(g.Items) == 0 {
		return nil, ErrEmptyGroup
	}
	switch g.Strategy {
	case group.StrategyManual:
		for _, it := range g.Items {
			if it.ID == g.ActiveItemID {
				t, err := resolveTarget(it)
				if err != nil {
					return nil, err
				}
				return []Target{t}, nil
			}
		}
		return nil, ErrNoActiveItem
	case group.StrategyFailover:
		items := append([]group.GroupItem(nil), g.Items...)
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Priority != items[j].Priority {
				return items[i].Priority < items[j].Priority
			}
			return items[i].ID < items[j].ID
		})
		targets := make([]Target, 0, len(g.Items))
		for _, it := range items {
			t, err := resolveTarget(it)
			if err != nil {
				return nil, err
			}
			targets = append(targets, t)
		}
		return targets, nil
	}
	return nil, errors.New("未知的路由策略: " + string(g.Strategy))
}

// resolveTarget 将分组项解析为 Target（加载模型与渠道）。
func resolveTarget(it group.GroupItem) (Target, error) {
	m, err := model.Get(it.ModelID)
	if err != nil {
		return Target{}, err
	}
	ch, err := channel.Get(m.ChannelID)
	if err != nil {
		return Target{}, err
	}
	return Target{Model: *m, Channel: *ch}, nil
}
