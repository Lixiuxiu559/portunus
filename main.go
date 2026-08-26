package main

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/api"
	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/cron"
	"github.com/Lixiuxiu559/portunus/backend/gateway"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func main() {
	cfg, err := shared.Load("config.json")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	if _, err := shared.InitDB(cfg); err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}

	if err := shared.AutoMigrate(
		&channel.Channel{},
		&model.Model{},
		&group.Group{},
		&group.GroupItem{},
		&shared.APIKey{},
		&shared.Setting{},
	); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	if err := shared.InitLogDB(cfg); err != nil {
		log.Fatalf("初始化日志库失败: %v", err)
	}
	if err := shared.EnsureDefaultSettings(); err != nil {
		log.Fatalf("初始化默认设置失败: %v", err)
	}

	r := gin.Default()

	gateway.Register(r)          // 对外 /v1 LLM 接口
	api.Register(r.Group("/api")) // 管理 API

	cron.Start() // 定时自动同步渠道模型

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("portunus 启动于 %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}
