package group

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/model"
)

// openImpactTestDB 打开独立的临时 SQLite，不触碰全局 shared.DB。
func openImpactTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "impact.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := db.AutoMigrate(&Group{}, &GroupItem{}, &model.Model{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

func TestPreviewDeletionImpactAggregatesGroupsWithFixedQueries(t *testing.T) {
	db := openImpactTestDB(t)
	m1 := model.Model{ChannelID: 1, Name: "m1"}
	m2 := model.Model{ChannelID: 1, Name: "m2"}
	m4 := model.Model{ChannelID: 2, Name: "m4"}
	for _, m := range []*model.Model{&m1, &m2, &m4} {
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("建模型失败: %v", err)
		}
	}

	// g1：manual，激活项也是被删成员，删除后为空组。
	g1 := Group{Name: "g1", Strategy: StrategyManual}
	g2 := Group{Name: "g2", Strategy: StrategyFailover}
	g3 := Group{Name: "g3", Strategy: StrategyFailover}
	g4 := Group{Name: "g4", Strategy: StrategyManual}
	for _, g := range []*Group{&g1, &g2, &g3, &g4} {
		if err := db.Create(g).Error; err != nil {
			t.Fatalf("建分组失败: %v", err)
		}
	}
	a := GroupItem{GroupID: g1.ID, ModelID: m1.ID, Priority: 0}
	b := GroupItem{GroupID: g1.ID, ModelID: m2.ID, Priority: 1}
	c := GroupItem{GroupID: g2.ID, ModelID: m2.ID, Priority: 0}
	dangling := GroupItem{GroupID: g2.ID, ModelID: 99999, Priority: 1} // 引用已缺失模型的历史项
	e := GroupItem{GroupID: g3.ID, ModelID: m1.ID, Priority: 0}
	f := GroupItem{GroupID: g3.ID, ModelID: m4.ID, Priority: 1}
	unaffected := GroupItem{GroupID: g4.ID, ModelID: m4.ID, Priority: 0}
	for _, it := range []*GroupItem{&a, &b, &c, &dangling, &e, &f, &unaffected} {
		if err := db.Create(it).Error; err != nil {
			t.Fatalf("建分组项失败: %v", err)
		}
	}
	if err := db.Model(&g1).Update("active_item_id", a.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&g3).Update("active_item_id", f.ID).Error; err != nil {
		t.Fatal(err)
	}

	queryCount := 0
	callbackName := "test:preview_deletion_impact_queries"
	if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(*gorm.DB) { queryCount++ }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(callbackName) })

	impacts, err := PreviewDeletionImpact(db, []int64{m1.ID, m2.ID})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if queryCount != 3 {
		t.Fatalf("预览应固定执行 3 次查询，实际 %d 次", queryCount)
	}
	if len(impacts) != 3 {
		t.Fatalf("受影响分组数 = %d, want 3: %+v", len(impacts), impacts)
	}

	if impacts[0].GroupID != g1.ID || impacts[0].Name != "g1" || impacts[0].Strategy != StrategyManual {
		t.Fatalf("g1 元数据不正确: %+v", impacts[0])
	}
	if len(impacts[0].RemovedItemIDs) != 2 || impacts[0].RemovedItemIDs[0] != a.ID || impacts[0].RemovedItemIDs[1] != b.ID {
		t.Fatalf("g1 移除成员不正确: %+v", impacts[0].RemovedItemIDs)
	}
	if !impacts[0].ClearsActive || !impacts[0].BecomesEmpty {
		t.Fatalf("g1 应为清除激活且删除后为空: %+v", impacts[0])
	}

	if impacts[1].GroupID != g2.ID || len(impacts[1].RemovedItemIDs) != 1 || impacts[1].RemovedItemIDs[0] != c.ID {
		t.Fatalf("g2 移除成员不正确: %+v", impacts[1])
	}
	if impacts[1].ClearsActive || impacts[1].BecomesEmpty {
		t.Fatalf("g2 仍有 dangling 历史项，不应为空组也不清除激活: %+v", impacts[1])
	}

	if impacts[2].GroupID != g3.ID || len(impacts[2].RemovedItemIDs) != 1 || impacts[2].RemovedItemIDs[0] != e.ID {
		t.Fatalf("g3 移除成员不正确: %+v", impacts[2])
	}
	if impacts[2].ClearsActive || impacts[2].BecomesEmpty {
		t.Fatalf("g3 激活项不在移除集合、仍有保留成员: %+v", impacts[2])
	}

	// 未引用目标模型的分组不应出现在结果中。
	for _, impact := range impacts {
		if impact.GroupID == g4.ID {
			t.Fatalf("未受影响分组 g4 不应出现: %+v", impact)
		}
	}
	// 预览不得产生任何写入。
	var items int64
	if err := db.Model(&GroupItem{}).Count(&items).Error; err != nil {
		t.Fatal(err)
	}
	if items != 7 {
		t.Fatalf("预览后分组项数 = %d, want 7", items)
	}
	var g1After Group
	if err := db.First(&g1After, g1.ID).Error; err != nil {
		t.Fatal(err)
	}
	if g1After.ActiveItemID != a.ID {
		t.Fatalf("预览后 ActiveItemID 被修改: %d", g1After.ActiveItemID)
	}
}

func TestPreviewDeletionImpactReturnsEmptySlice(t *testing.T) {
	db := openImpactTestDB(t)
	impacts, err := PreviewDeletionImpact(db, nil)
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if impacts == nil || len(impacts) != 0 {
		t.Fatalf("无模型 ID 时应返回空切片, got %#v", impacts)
	}
	impacts, err = PreviewDeletionImpact(db, []int64{12345})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if impacts == nil || len(impacts) != 0 {
		t.Fatalf("无引用时应返回空切片, got %#v", impacts)
	}
}
