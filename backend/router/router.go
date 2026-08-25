package router

import (
	"errors"
	"sync"
	"sync/atomic"

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

// roundRobinCounters 记录每个分组的轮询游标（内存态，重启归零）。
var roundRobinCounters sync.Map // groupID(int64) -> *atomic.Uint64

// Resolve 按分组策略返回有序目标列表。
// manual / round_robin 返回单个目标；failover 返回全部目标（按 priority 升序）。
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
	case group.StrategyRoundRobin:
		idx := nextRoundRobin(g.ID, len(g.Items))
		t, err := resolveTarget(g.Items[idx])
		if err != nil {
			return nil, err
		}
		return []Target{t}, nil
	case group.StrategyFailover:
		targets := make([]Target, 0, len(g.Items))
		for _, it := range g.Items {
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

// nextRoundRobin 返回下一个轮询下标（原子自增，取模）。
func nextRoundRobin(groupID int64, n int) int {
	c, _ := roundRobinCounters.LoadOrStore(groupID, &atomic.Uint64{})
	counter := c.(*atomic.Uint64)
	return int(counter.Add(1)-1) % n
}
