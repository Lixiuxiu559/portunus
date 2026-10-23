package router

import (
	"path/filepath"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// setupRouter 建临时 DB + 种子（1 渠道 + 2 模型 + 1 分组带 2 项），返回带 items 的分组。
func setupRouter(t *testing.T) *group.Group {
	t.Helper()
	if shared.DB != nil {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	}
	cfg := &shared.Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if _, err := shared.InitDB(cfg); err != nil {
		t.Fatalf("初始化 DB 失败: %v", err)
	}
	if err := shared.AutoMigrate(&channel.Channel{}, &model.Model{}, &group.Group{}, &group.GroupItem{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	})

	ch := channel.Channel{Name: "c1", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	m1 := model.Model{ChannelID: ch.ID, Name: "m1"}
	m2 := model.Model{ChannelID: ch.ID, Name: "m2"}
	if err := shared.DB.Create(&m1).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	if err := shared.DB.Create(&m2).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	g := group.Group{Name: "g1", Strategy: group.StrategyFailover}
	if err := shared.DB.Create(&g).Error; err != nil {
		t.Fatalf("建分组失败: %v", err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m1.ID, Priority: 0}).Error; err != nil {
		t.Fatalf("建分组项失败: %v", err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m2.ID, Priority: 1}).Error; err != nil {
		t.Fatalf("建分组项失败: %v", err)
	}

	loaded, err := group.GetByName("g1")
	if err != nil {
		t.Fatalf("加载分组失败: %v", err)
	}
	return loaded
}

func TestResolveFailover(t *testing.T) {
	g := setupRouter(t)
	g.Strategy = group.StrategyFailover

	targets, err := Resolve(g)
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("failover 应返回 2 个 target, got %d", len(targets))
	}
	if targets[0].Model.Name != "m1" || targets[1].Model.Name != "m2" {
		t.Errorf("failover 应按 priority 顺序: %s, %s", targets[0].Model.Name, targets[1].Model.Name)
	}
	if targets[0].Channel.Name != "c1" {
		t.Errorf("target 的 channel 解析不对: %+v", targets[0].Channel)
	}
}

func TestResolveManual(t *testing.T) {
	g := setupRouter(t)
	g.Strategy = group.StrategyManual
	g.ActiveItemID = g.Items[1].ID // 激活 m2

	targets, err := Resolve(g)
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if len(targets) != 1 || targets[0].Model.Name != "m2" {
		t.Errorf("manual 应只返回激活项 m2: %+v", targets)
	}

	g.ActiveItemID = 0
	if _, err := Resolve(g); err != ErrNoActiveItem {
		t.Errorf("未指定激活项应返回 ErrNoActiveItem, got %v", err)
	}
}

func TestResolveMigratedFailoverStableOrder(t *testing.T) {
	g := setupRouter(t)
	if err := shared.DB.Model(&group.Group{}).Where("id = ?", g.ID).UpdateColumn("strategy", "round_robin").Error; err != nil {
		t.Fatal(err)
	}
	// 相同优先级时明确按成员 ID；输入逆序不改变路由顺序。
	if err := shared.DB.Model(&group.GroupItem{}).Where("group_id = ?", g.ID).Update("priority", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := group.MigrateLegacyStrategies(shared.DB); err != nil {
		t.Fatal(err)
	}
	loaded, err := group.GetByName(g.Name)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Strategy != group.StrategyFailover || loaded.Items[0].ID >= loaded.Items[1].ID {
		t.Fatalf("迁移加载顺序错误: %+v", loaded)
	}
	loaded.Items[0], loaded.Items[1] = loaded.Items[1], loaded.Items[0]
	for i := 0; i < 5; i++ {
		targets, err := Resolve(loaded)
		if err != nil {
			t.Fatal(err)
		}
		if len(targets) != 2 || targets[0].Model.Name != "m1" || targets[1].Model.Name != "m2" {
			t.Fatalf("故障转移应保持主备顺序: %+v", targets)
		}
	}
	if loaded.Items[0].ModelName != "m2" {
		t.Fatal("路由不应修改调用方的成员切片")
	}
	// 优先级高于 ID 决胜规则，即使切片本身未按优先级排序也应正确。
	loaded.Items[0].Priority = -1
	targets, err := Resolve(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Model.Name != "m2" || targets[1].Model.Name != "m1" {
		t.Fatalf("priority 必须先于成员 ID 决定顺序: %+v", targets)
	}
	loaded.Strategy = group.Strategy("round_robin")
	if _, err := Resolve(loaded); err == nil {
		t.Fatal("废弃策略不应继续轮询")
	}
}

func TestResolveEmptyGroup(t *testing.T) {
	if _, err := Resolve(&group.Group{Strategy: group.StrategyManual}); err != ErrEmptyGroup {
		t.Errorf("空分组应返回 ErrEmptyGroup, got %v", err)
	}
}
