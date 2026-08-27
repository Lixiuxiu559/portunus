package gateway

import (
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// proxyCfg 是网关转发层的只读配置，默认取 DefaultProxyConfig()，启动时由 Configure 覆盖。
var proxyCfg = shared.DefaultProxyConfig()

// Configure 由 main 启动时调用一次，注入 config.json 中解析出的转发配置；之后只读。
// 同步重建 httpClient，使超时配置即时生效。
func Configure(pc shared.ProxyConfig) {
	proxyCfg = pc
	httpClient = newHTTPClient(pc)
}
