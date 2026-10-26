package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/api"
	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/cron"
	"github.com/Lixiuxiu559/portunus/backend/gateway"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/quota"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"gorm.io/gorm"
)

func main() {
	cfg, err := shared.Load("config.json")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	if _, err := shared.InitDB(cfg); err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}

	// 渠道级 1:1：先收敛历史多来源，再由 AutoMigrate 建立唯一索引。
	if err := quota.DedupeSources(shared.DB); err != nil {
		log.Fatalf("收敛余额查询来源失败: %v", err)
	}

	if err := shared.AutoMigrate(
		&channel.Channel{},
		&model.Model{},
		&group.Group{},
		&group.GroupItem{},
		&shared.APIKey{},
		&shared.Setting{},
		&quota.Source{},
		&quota.State{},
	); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	if err := group.MigrateLegacyStrategies(shared.DB); err != nil {
		log.Fatalf("迁移分组路由策略失败: %v", err)
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
		Cfg:          cfg.Proxy,
		Client:       gateway.NewHTTPClient(cfg.Proxy),
		Breakers:     gateway.NewBreakerStore(cfg.Proxy),
		Cooldowns:    gateway.NewRateLimitStore(cfg.Proxy),
		Concurrency:  gateway.NewChannelConcurrencyStore(),
		AuthIdentity: shared.AuthenticateAPIKeyIdentity,
		LogWrite:     func(e *shared.Log) { shared.LogDB.Create(e) },
		AttemptWrite: shared.WriteUpstreamAttempt,
		RoutingWrite: shared.WriteRoutingTrace,
	})
	quotaService := quota.NewService(shared.DB, quota.NewHTTPClient(), func(tx *gorm.DB, id int64) (string, error) {
		var ch channel.Channel
		if err := tx.Select("id", "key").First(&ch, id).Error; err != nil {
			return "", err
		}
		return ch.Key, nil
	})
	api.Register(r.Group("/api"), quotaService)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go quotaService.RunScheduler(ctx)

	cron.Start() // 定时自动同步渠道模型

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("portunus 启动于 %s", addr)
	server := &http.Server{Addr: addr, Handler: r, BaseContext: func(net.Listener) context.Context { return ctx }}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("关闭管理与网关服务失败: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("启动失败: %v", err)
	}
	<-shutdownDone
}
