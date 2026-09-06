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
	if _, err := shared.EnsureDefaultAPIKey(); err != nil {
		log.Fatalf("初始化默认 API Key 失败: %v", err)
	}

	// 网关依赖装配：转发配置、上游 HTTP 客户端、熔断 store、鉴权与日志写入
	//（gateway 据此与数据库直连解耦，见 gateway.Deps）。
	r := gin.Default()

	gateway.Register(r, gateway.Deps{
		Cfg:      cfg.Proxy,
		Client:   gateway.NewHTTPClient(cfg.Proxy),
		Breakers: gateway.NewBreakerStore(cfg.Proxy),
		Auth: func(key string) (int64, bool) {
			var k shared.APIKey
			if err := shared.DB.Where("key = ?", key).First(&k).Error; err != nil {
				return 0, false
			}
			return k.ID, true
		},
		LogWrite: func(e *shared.Log) { shared.LogDB.Create(e) },
	})
	api.Register(r.Group("/api")) // 管理 API

	cron.Start() // 定时自动同步渠道模型

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("portunus 启动于 %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}
