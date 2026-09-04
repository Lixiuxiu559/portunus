package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Config 是服务的全局配置，来源为 JSON 文件 + 环境变量覆盖。
type Config struct {
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"server"`
	Database struct {
		Type    string `json:"type"` // sqlite / mysql / postgres
		Path    string `json:"path"`
		LogPath string `json:"log_path"` // 可选：日志库独立路径，空则复用主库
	} `json:"database"`
	Proxy ProxyConfig `json:"proxy"` // 网关转发：重试 / 超时 / 熔断
}

// ProxyConfig 是网关转发层的失败处理配置。
type ProxyConfig struct {
	RetryCount               int `json:"retry_count"`                 // 每个上游失败后重试次数（即最多尝试 retry_count+1 次）
	ConnectTimeoutSeconds    int `json:"connect_timeout_seconds"`     // 连接超时
	FirstByteTimeoutSeconds  int `json:"first_byte_timeout_seconds"`  // 等响应头 / 流式首包（首个数据事件）超时
	StreamIdleTimeoutSeconds int `json:"stream_idle_timeout_seconds"` // 流式首包后，上游静默多久掐断（0 禁用）
	NonStreamTimeoutSeconds  int `json:"non_stream_timeout_seconds"`  // 非流式读完整响应体的总超时
	CircuitFailureThreshold  int `json:"circuit_failure_threshold"`   // 熔断：连续失败 N 次开路
	CircuitSuccessThreshold  int `json:"circuit_success_threshold"`   // 熔断：半开后连续成功 N 次转关闭
	CircuitResetSeconds      int `json:"circuit_reset_seconds"`       // 熔断：开路冷却时长
}

// DefaultProxyConfig 返回网关转发的默认配置。
func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		RetryCount:               2,
		ConnectTimeoutSeconds:    30,
		FirstByteTimeoutSeconds:  60,
		StreamIdleTimeoutSeconds: 120,
		NonStreamTimeoutSeconds:  600,
		CircuitFailureThreshold:  4,
		CircuitSuccessThreshold:  2,
		CircuitResetSeconds:      60,
	}
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *Config {
	c := &Config{}
	c.Server.Host = "0.0.0.0"
	c.Server.Port = 3060
	c.Database.Type = "sqlite"
	c.Database.Path = "data/portunus.db"
	c.Proxy = DefaultProxyConfig()
	return c
}

// Load 从指定路径加载配置；文件不存在时用默认值。环境变量 PORTUNUS_* 覆盖对应字段。
func Load(path string) (*Config, error) {
	c := DefaultConfig()

	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	applyEnv(c)
	return c, nil
}

// applyEnv 用 PORTUNUS_SERVER_PORT 形式的环境变量覆盖配置。
func applyEnv(c *Config) {
	if v := os.Getenv("PORTUNUS_SERVER_HOST"); v != "" {
		c.Server.Host = v
	}
	if v := os.Getenv("PORTUNUS_SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Server.Port = n
		}
	}
	if v := os.Getenv("PORTUNUS_DATABASE_TYPE"); v != "" {
		c.Database.Type = v
	}
	if v := os.Getenv("PORTUNUS_DATABASE_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("PORTUNUS_DATABASE_LOG_PATH"); v != "" {
		c.Database.LogPath = v
	}
	applyProxyEnv(c)
}

// applyProxyEnv 用 PORTUNUS_PROXY_* 环境变量覆盖网关转发配置。
func applyProxyEnv(c *Config) {
	if v := os.Getenv("PORTUNUS_PROXY_RETRY_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.RetryCount = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_CONNECT_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.ConnectTimeoutSeconds = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_FIRST_BYTE_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.FirstByteTimeoutSeconds = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_STREAM_IDLE_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.StreamIdleTimeoutSeconds = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_NON_STREAM_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.NonStreamTimeoutSeconds = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_CIRCUIT_FAILURE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.CircuitFailureThreshold = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_CIRCUIT_SUCCESS_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.CircuitSuccessThreshold = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_CIRCUIT_RESET_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.CircuitResetSeconds = n
		}
	}
}
