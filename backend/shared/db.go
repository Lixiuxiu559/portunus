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

// LogDB 是日志库句柄，由 InitLogDB 填充；未配置独立日志库时复用 DB。
var LogDB *gorm.DB

// openSQLite 打开指定路径的 SQLite，自动创建目录。
func openSQLite(path string) (*gorm.DB, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}
	return gorm.Open(sqlite.Open(path), &gorm.Config{})
}

// InitDB 打开数据库并执行自动迁移。目前仅支持 SQLite，接口已为换库预留。
func InitDB(cfg *Config) (*gorm.DB, error) {
	var (
		db  *gorm.DB
		err error
	)

	switch cfg.Database.Type {
	case "sqlite":
		db, err = openSQLite(cfg.Database.Path)
	default:
		return nil, fmt.Errorf("暂不支持的数据库类型: %s", cfg.Database.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	DB = db
	return db, nil
}

// InitLogDB 打开日志库并迁移日志表。配置了 Database.LogPath 时用独立 SQLite，
// 否则复用主库，让日志膨胀与主业务数据隔离。
func InitLogDB(cfg *Config) error {
	if cfg.Database.LogPath != "" {
		db, err := openSQLite(cfg.Database.LogPath)
		if err != nil {
			return fmt.Errorf("打开日志库失败: %w", err)
		}
		LogDB = db
	} else {
		LogDB = DB
	}
	return LogDB.AutoMigrate(&Log{})
}

// AutoMigrate 迁移所有实体，由各模块注册自己的模型。
func AutoMigrate(entities ...any) error {
	if DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	return DB.AutoMigrate(entities...)
}
