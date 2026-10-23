package group

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func migrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "groups.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&Group{}, &GroupItem{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestStrategyValid(t *testing.T) {
	for _, strategy := range []Strategy{StrategyManual, StrategyFailover} {
		if !strategy.Valid() {
			t.Fatalf("合法策略被拒绝: %q", strategy)
		}
	}
	for _, strategy := range []Strategy{"", "round_robin", "Round_robin", " round_robin", "adaptive"} {
		if strategy.Valid() {
			t.Fatalf("非法策略被接受: %q", strategy)
		}
	}
}

func TestMigrateLegacyStrategiesPreservesDataAndIsIdempotent(t *testing.T) {
	db := migrationDB(t)
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, strategy := range []Strategy{"round_robin", StrategyManual, StrategyFailover, "other", "Round_robin", "round_robin "} {
		g := Group{ID: int64(i + 1), Name: string(strategy), Strategy: strategy, ActiveItemID: 51, CreatedAt: stamp, UpdatedAt: stamp}
		if err := db.Create(&g).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&GroupItem{ID: 51, GroupID: 1, ModelID: 99, Priority: -3}).Error; err != nil {
		t.Fatal(err)
	}
	var want []Group
	if err := db.Order("id").Find(&want).Error; err != nil {
		t.Fatal(err)
	}
	want[0].Strategy = StrategyFailover
	var wantItems []GroupItem
	if err := db.Order("id").Find(&wantItems).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := MigrateLegacyStrategies(db); err != nil {
			t.Fatal(err)
		}
		var got []Group
		var gotItems []GroupItem
		if err := db.Order("id").Find(&got).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Order("id").Find(&gotItems).Error; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotItems, wantItems) {
			t.Fatalf("迁移修改了其他字段: got=%+v want=%+v items=%+v", got, want, gotItems)
		}
	}
}

func TestMigrateLegacyStrategiesPropagatesFailure(t *testing.T) {
	db := migrationDB(t)
	if err := db.Create(&Group{Name: "legacy", Strategy: "round_robin"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_strategy BEFORE UPDATE OF strategy ON groups BEGIN SELECT RAISE(ABORT, 'strategy migration failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyStrategies(db); err == nil {
		t.Fatal("迁移失败必须传播")
	}
	var got Group
	if err := db.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Strategy != "round_robin" {
		t.Fatal("失败迁移不应修改数据")
	}
}
