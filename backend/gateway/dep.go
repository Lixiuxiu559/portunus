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
	// Auth 校验 API Key，通过返回 api_key_id。生产实现查 shared.DB 的 api_keys 表。
	Auth func(key string) (apiKeyID int64, ok bool)
	// LogWrite 写一条调用日志。生产实现写 shared.LogDB；测试收集到内存断言。
	LogWrite func(*shared.Log)
}

// relayServer 持有依赖，承载 /v1 转发的全部编排（鉴权 / 路由 / 重试 / 熔断 / 归因）。
type relayServer struct {
	deps Deps
}

// NewRelayServer 用给定依赖构建 relayServer。
func NewRelayServer(deps Deps) *relayServer {
	return &relayServer{deps: deps}
}
