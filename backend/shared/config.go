package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/Lixiuxiu559/portunus/backend/resource"
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
	RateLimitCooldownSeconds    int             `json:"rate_limit_cooldown_seconds"`     // 429 无有效 Retry-After 时的冷却秒数；0 使用 30 秒
	RateLimitMaxCooldownSeconds int             `json:"rate_limit_max_cooldown_seconds"` // 429 冷却秒数上限；0 使用 300 秒
	ResourceLimits              resource.Limits `json:"resource_limits"`                 // 转换链资源上限；0 使用默认值
	RetryCount                  int             `json:"retry_count"`                     // 每个上游失败后重试次数（即最多尝试 retry_count+1 次）
	RetryBackoffBaseMs          int             `json:"retry_backoff_base_ms"`           // 请求重试退避基数；0 使用默认值 200 毫秒
	RetryBackoffMaxMs           int             `json:"retry_backoff_max_ms"`            // 请求重试退避上限；0 使用默认值 2000 毫秒
	MaxHTTPAttempts             int             `json:"max_http_attempts"`               // 单个请求的 HTTP 总尝试预算，包含兼容重试；0 使用默认值 4
	FirstContentTimeoutSeconds  int             `json:"first_content_timeout_seconds"`   // 跨目标累计首内容期限；0 使用默认值 120 秒
	ConnectTimeoutSeconds       int             `json:"connect_timeout_seconds"`         // 连接超时
	FirstByteTimeoutSeconds     int             `json:"first_byte_timeout_seconds"`      // 等响应头 / 流式首包（首个数据事件）超时
	StreamIdleTimeoutSeconds    int             `json:"stream_idle_timeout_seconds"`     // 流式首包后，上游静默多久掐断（0 禁用）
	NonStreamTimeoutSeconds     int             `json:"non_stream_timeout_seconds"`      // 非流式请求跨目标累计期限；0 使用默认值 600 秒
	CircuitFailureThreshold     int             `json:"circuit_failure_threshold"`       // 熔断：连续失败 N 次开路
	CircuitSuccessThreshold     int             `json:"circuit_success_threshold"`       // 熔断：半开后连续成功 N 次转关闭
	CircuitResetSeconds         int             `json:"circuit_reset_seconds"`           // 熔断：开路冷却时长
}

// DefaultProxyConfig 返回网关转发的默认配置。
func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		RateLimitCooldownSeconds:    30,
		RateLimitMaxCooldownSeconds: 300,
		ResourceLimits:              resource.Defaults(),
		RetryCount:                  2,
		RetryBackoffBaseMs:          200,
		RetryBackoffMaxMs:           2000,
		MaxHTTPAttempts:             4,
		FirstContentTimeoutSeconds:  120,
		ConnectTimeoutSeconds:       30,
		// 等响应头 / 流式首包时限。曾试过 20s：高峰期中转上游排队时正常请求的
		// 首包也会超 20s（30s~2min 的成功 200 大量存在），20s 误杀慢而正常的请求，
		// 反而制造额外失败与客户端指数退避，故定 60s（见 gateway/client_wait_test.go
		// 头注释）。配合假死目标跳过同目标重试 + 熔断快速开路，客户端最多等一个窗口。
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
	if err := validateResourceLimits(c.Proxy.ResourceLimits); err != nil {
		return nil, err
	}
	c.Proxy.ResourceLimits = c.Proxy.ResourceLimits.Normalize()
	if c.Proxy.MaxHTTPAttempts < 0 {
		return nil, fmt.Errorf("proxy.max_http_attempts 不能为负数")
	}
	if c.Proxy.FirstContentTimeoutSeconds < 0 {
		return nil, fmt.Errorf("proxy.first_content_timeout_seconds 不能为负数")
	}
	if c.Proxy.NonStreamTimeoutSeconds < 0 {
		return nil, fmt.Errorf("proxy.non_stream_timeout_seconds 不能为负数")
	}
	if c.Proxy.RetryBackoffBaseMs < 0 {
		return nil, fmt.Errorf("proxy.retry_backoff_base_ms 不能为负数")
	}
	if c.Proxy.RetryBackoffMaxMs < 0 {
		return nil, fmt.Errorf("proxy.retry_backoff_max_ms 不能为负数")
	}
	if c.Proxy.RateLimitCooldownSeconds < 0 {
		return nil, fmt.Errorf("proxy.rate_limit_cooldown_seconds 不能为负数")
	}
	if c.Proxy.RateLimitMaxCooldownSeconds < 0 {
		return nil, fmt.Errorf("proxy.rate_limit_max_cooldown_seconds 不能为负数")
	}
	defaults := DefaultProxyConfig()
	if c.Proxy.RateLimitCooldownSeconds == 0 {
		c.Proxy.RateLimitCooldownSeconds = defaults.RateLimitCooldownSeconds
	}
	if c.Proxy.RateLimitMaxCooldownSeconds == 0 {
		c.Proxy.RateLimitMaxCooldownSeconds = defaults.RateLimitMaxCooldownSeconds
	}
	if c.Proxy.RateLimitMaxCooldownSeconds < c.Proxy.RateLimitCooldownSeconds {
		return nil, fmt.Errorf("proxy.rate_limit_max_cooldown_seconds 不能小于 proxy.rate_limit_cooldown_seconds")
	}
	if c.Proxy.MaxHTTPAttempts == 0 {
		c.Proxy.MaxHTTPAttempts = DefaultProxyConfig().MaxHTTPAttempts
	}
	if c.Proxy.FirstContentTimeoutSeconds == 0 {
		c.Proxy.FirstContentTimeoutSeconds = DefaultProxyConfig().FirstContentTimeoutSeconds
	}
	if c.Proxy.NonStreamTimeoutSeconds == 0 {
		c.Proxy.NonStreamTimeoutSeconds = defaults.NonStreamTimeoutSeconds
	}
	if c.Proxy.RetryBackoffBaseMs == 0 {
		c.Proxy.RetryBackoffBaseMs = defaults.RetryBackoffBaseMs
	}
	if c.Proxy.RetryBackoffMaxMs == 0 {
		c.Proxy.RetryBackoffMaxMs = defaults.RetryBackoffMaxMs
	}
	if c.Proxy.RetryBackoffMaxMs < c.Proxy.RetryBackoffBaseMs {
		return nil, fmt.Errorf("proxy.retry_backoff_max_ms 不能小于 proxy.retry_backoff_base_ms")
	}
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
	if n, err := strconv.Atoi(os.Getenv("PORTUNUS_PROXY_RATE_LIMIT_COOLDOWN_SECONDS")); err == nil {
		c.Proxy.RateLimitCooldownSeconds = n
	}
	if n, err := strconv.Atoi(os.Getenv("PORTUNUS_PROXY_RATE_LIMIT_MAX_COOLDOWN_SECONDS")); err == nil {
		c.Proxy.RateLimitMaxCooldownSeconds = n
	}
	applyResourceEnv(&c.Proxy.ResourceLimits)
	if v := os.Getenv("PORTUNUS_PROXY_RETRY_BACKOFF_BASE_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.RetryBackoffBaseMs = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_RETRY_BACKOFF_MAX_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.RetryBackoffMaxMs = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_MAX_HTTP_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.MaxHTTPAttempts = n
		}
	}
	if v := os.Getenv("PORTUNUS_PROXY_FIRST_CONTENT_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Proxy.FirstContentTimeoutSeconds = n
		}
	}
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

func validateResourceLimits(l resource.Limits) error {
	fields := []struct {
		name  string
		value int64
	}{
		{"max_request_bytes", l.MaxRequestBytes},
		{"max_response_bytes", l.MaxResponseBytes},
		{"max_native_fragments", int64(l.MaxNativeFragments)},
		{"max_tool_arguments_bytes", l.MaxToolArgumentsBytes},
		{"max_accumulated_bytes", l.MaxAccumulatedBytes},
		{"max_json_depth", int64(l.MaxJSONDepth)},
	}
	for _, field := range fields {
		if field.value < 0 {
			return fmt.Errorf("proxy.resource_limits.%s 不能为负数", field.name)
		}
	}
	return nil
}

func applyResourceEnv(l *resource.Limits) {
	bytesFields := []struct {
		name   string
		target *int64
	}{
		{"MAX_REQUEST_BYTES", &l.MaxRequestBytes},
		{"MAX_RESPONSE_BYTES", &l.MaxResponseBytes},
		{"MAX_TOOL_ARGUMENTS_BYTES", &l.MaxToolArgumentsBytes},
		{"MAX_ACCUMULATED_BYTES", &l.MaxAccumulatedBytes},
	}
	for _, field := range bytesFields {
		if value := os.Getenv("PORTUNUS_PROXY_" + field.name); value != "" {
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				*field.target = n
			}
		}
	}
	intFields := []struct {
		name   string
		target *int
	}{
		{"MAX_NATIVE_FRAGMENTS", &l.MaxNativeFragments},
		{"MAX_JSON_DEPTH", &l.MaxJSONDepth},
	}
	for _, field := range intFields {
		if value := os.Getenv("PORTUNUS_PROXY_" + field.name); value != "" {
			if n, err := strconv.Atoi(value); err == nil {
				*field.target = n
			}
		}
	}
}
