package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// localManagementOnly 以 TCP 对端为管理边界，不使用可由调用方伪造的代理头。
// Host 防止 DNS rebinding，Origin 防止浏览器从其他站点发起管理写操作。
func localManagementOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		peer, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil || !isLoopbackIP(peer) || !isLocalAuthority(c.Request.Host) || !isManagementOrigin(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "管理接口仅允许本机访问"})
			return
		}
		c.Next()
	}
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalAuthority(authority string) bool {
	host := authority
	if strings.Contains(authority, ":") {
		var err error
		host, _, err = net.SplitHostPort(authority)
		if err != nil {
			// 无端口的 IPv6 authority 必须保留方括号。
			if !strings.HasPrefix(authority, "[") || !strings.HasSuffix(authority, "]") {
				return false
			}
			host = strings.TrimSuffix(strings.TrimPrefix(authority, "["), "]")
		}
	}
	return strings.EqualFold(host, "localhost") || isLoopbackIP(host)
}

func isManagementOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // 本机 CLI、健康探测以及同源浏览器 GET。
	}
	if origin == "null" {
		// 打包版 Electron 从 file:// 加载。自定义头阻止其他站点通过
		// opaque/sandbox Origin 发起无预检的表单写操作；管理 API 不开放 CORS。
		return r.Header.Get("X-Portunus-Local") == "1"
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !isLocalAuthority(u.Host) {
		return false
	}
	// 同源管理请求，以及固定端口的本机 Vite 开发页面。
	return strings.EqualFold(u.Host, r.Host) || (u.Scheme == "http" && u.Port() == "5173")
}
