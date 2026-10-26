package gateway

import (
	"net/http"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// Deps 是网关的外部依赖集：由 main 装配生产实现，测试装配内存替身——gateway
// 据此与包级全局和数据库直连解耦（鉴权、日志写入显式传入，不再摸 shared.DB）。
type Deps struct {
	// Cfg 是转发配置（重试 / 超时 / 熔断阈值）。启动时定值、无热更新，值注入安全。
	Cfg shared.ProxyConfig
	// Client 是上游 HTTP 客户端（连接 / 等响应头超时由 Transport 决定），见 NewHTTPClient。
	Client *http.Client
	// Breakers 是熔断状态 store：进程内唯一实例跨请求共享健康度。
	Breakers *BreakerStore
	// Cooldowns 按模型或渠道共享限流冷却，独立于故障熔断。
	Cooldowns *RateLimitStore
	// Concurrency 按渠道ID跨模型/分组共享在途调用数；0上限不限制。
	Concurrency *ChannelConcurrencyStore
	// Auth 校验 API Key，通过返回 api_key_id。生产实现查 shared.DB 的 api_keys 表。
	Auth func(key string) (apiKeyID int64, ok bool)
	// AuthIdentity 优先于旧 Auth，一次鉴权同时获取稳定 ID 与调用时名称。
	AuthIdentity func(key string) (shared.APIKeyIdentity, bool)
	// LogWrite 写一条调用日志。生产实现写 shared.LogDB；测试收集到内存断言。
	LogWrite func(*shared.Log)
	// AttemptWrite 独立保存每次显式 Client.Do 尝试；写入失败不重试上游。
	// nil 用于不需要记录尝试的测试/嵌入调用方；生产由 main 显式装配。
	AttemptWrite func(*shared.UpstreamAttempt) error
	// RoutingWrite 保存独立的请求级路由诊断，不参与调用/HTTP尝试统计。
	// nil 保持测试与嵌入调用兼容；写入失败不影响转发或触发重试。
	RoutingWrite func(*shared.RoutingTrace) error
}

// relayServer 持有依赖，承载 /v1 转发的全部编排（鉴权 / 路由 / 重试 / 熔断 / 归因）。
type relayServer struct {
	deps Deps
	// 管理验证仅返回安全的分阶段诊断，不输出可能回显凭据的上游原文。
	quiet bool
}

// NewRelayServer 用给定依赖构建 relayServer。
func NewRelayServer(deps Deps) *relayServer {
	if deps.Concurrency == nil {
		deps.Concurrency = NewChannelConcurrencyStore()
	}
	if deps.Cooldowns == nil {
		deps.Cooldowns = NewRateLimitStore(deps.Cfg)
	}
	return &relayServer{deps: deps}
}
