package shared

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// DB 是全局数据库句柄，由 InitDB 填充。
var DB *gorm.DB

// InitDB 打开数据库并执行自动迁移。目前仅支持 SQLite，接口已为换库预留。
func InitDB(cfg *Config) (*gorm.DB, error) {
	var (
		db  *gorm.DB
		err error
	)

	switch cfg.Database.Type {
	case "sqlite":
		dir := filepath.Dir(cfg.Database.Path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("创建数据库目录失败: %w", err)
			}
		}
		db, err = gorm.Open(sqlite.Open(cfg.Database.Path), &gorm.Config{})
	default:
		return nil, fmt.Errorf("暂不支持的数据库类型: %s", cfg.Database.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	DB = db
	return db, nil
}

// AutoMigrate 迁移所有实体，由各模块注册自己的模型。
func AutoMigrate(entities ...any) error {
	if DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	return DB.AutoMigrate(entities...)
}
