package shared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetryBackoffAndNonStreamDeadlineConfig(t *testing.T) {
	for _, tc := range []struct {
		name, body, baseEnv, maxEnv, timeoutEnv string
		wantBase, wantMax, wantTimeout          int
		wantErr                                 string
	}{
		{name: "缺失文件", wantBase: 200, wantMax: 2000, wantTimeout: 600},
		{name: "旧配置", body: `{"proxy":{"retry_count":1,"conversion_policy":"strict"}}`, wantBase: 200, wantMax: 2000, wantTimeout: 600},
		{name: "显式配置", body: `{"proxy":{"retry_backoff_base_ms":50,"retry_backoff_max_ms":500,"non_stream_timeout_seconds":90}}`, wantBase: 50, wantMax: 500, wantTimeout: 90},
		{name: "零值默认", body: `{"proxy":{"retry_backoff_base_ms":0,"retry_backoff_max_ms":0,"non_stream_timeout_seconds":0}}`, wantBase: 200, wantMax: 2000, wantTimeout: 600},
		{name: "环境覆盖", body: `{"proxy":{"retry_backoff_base_ms":50,"retry_backoff_max_ms":500,"non_stream_timeout_seconds":90}}`, baseEnv: "100", maxEnv: "800", timeoutEnv: "30", wantBase: 100, wantMax: 800, wantTimeout: 30},
		{name: "环境零值默认", body: `{"proxy":{"retry_backoff_base_ms":50,"retry_backoff_max_ms":500,"non_stream_timeout_seconds":90}}`, baseEnv: "0", maxEnv: "0", timeoutEnv: "0", wantBase: 200, wantMax: 2000, wantTimeout: 600},
		{name: "配置负基数", body: `{"proxy":{"retry_backoff_base_ms":-1}}`, wantErr: "proxy.retry_backoff_base_ms"},
		{name: "配置负上限", body: `{"proxy":{"retry_backoff_max_ms":-1}}`, wantErr: "proxy.retry_backoff_max_ms"},
		{name: "配置负期限", body: `{"proxy":{"non_stream_timeout_seconds":-1}}`, wantErr: "proxy.non_stream_timeout_seconds"},
		{name: "环境负基数", baseEnv: "-1", wantErr: "proxy.retry_backoff_base_ms"},
		{name: "环境负上限", maxEnv: "-1", wantErr: "proxy.retry_backoff_max_ms"},
		{name: "环境负期限", timeoutEnv: "-1", wantErr: "proxy.non_stream_timeout_seconds"},
		{name: "配置上限小于基数", body: `{"proxy":{"retry_backoff_base_ms":500,"retry_backoff_max_ms":100}}`, wantErr: "proxy.retry_backoff_max_ms"},
		{name: "环境上限小于基数", baseEnv: "500", maxEnv: "100", wantErr: "proxy.retry_backoff_max_ms"},
		{name: "默认上限小于显式基数", body: `{"proxy":{"retry_backoff_base_ms":3000,"retry_backoff_max_ms":0}}`, wantErr: "proxy.retry_backoff_max_ms"},
		{name: "默认基数大于显式上限", body: `{"proxy":{"retry_backoff_base_ms":0,"retry_backoff_max_ms":100}}`, wantErr: "proxy.retry_backoff_max_ms"},
		{name: "上限等于基数", body: `{"proxy":{"retry_backoff_base_ms":100,"retry_backoff_max_ms":100}}`, wantBase: 100, wantMax: 100, wantTimeout: 600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PORTUNUS_PROXY_RETRY_BACKOFF_BASE_MS", tc.baseEnv)
			t.Setenv("PORTUNUS_PROXY_RETRY_BACKOFF_MAX_MS", tc.maxEnv)
			t.Setenv("PORTUNUS_PROXY_NON_STREAM_TIMEOUT_SECONDS", tc.timeoutEnv)
			t.Setenv("PORTUNUS_PROXY_MAX_HTTP_ATTEMPTS", "")
			t.Setenv("PORTUNUS_PROXY_FIRST_CONTENT_TIMEOUT_SECONDS", "")
			t.Setenv("PORTUNUS_PROXY_CONVERSION_POLICY", "strict")
			path := filepath.Join(t.TempDir(), "config.json")
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v, want field %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Proxy.RetryBackoffBaseMs != tc.wantBase || cfg.Proxy.RetryBackoffMaxMs != tc.wantMax || cfg.Proxy.NonStreamTimeoutSeconds != tc.wantTimeout {
				t.Fatalf("proxy=%+v, want base/max/timeout=%d/%d/%d", cfg.Proxy, tc.wantBase, tc.wantMax, tc.wantTimeout)
			}
			if tc.name == "旧配置" && cfg.Proxy.RetryCount != 1 {
				t.Fatalf("旧配置 retry_count=%d, want 1", cfg.Proxy.RetryCount)
			}
			encoded, err := json.Marshal(cfg.Proxy)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "conversion_policy") {
				t.Fatalf("已移除策略不应重新输出: %s", encoded)
			}
		})
	}
}

func TestDefaultRetryBackoffAndNonStreamDeadline(t *testing.T) {
	cfg := DefaultProxyConfig()
	if cfg.RetryBackoffBaseMs != 200 || cfg.RetryBackoffMaxMs != 2000 || cfg.NonStreamTimeoutSeconds != 600 {
		t.Fatalf("默认配置=%+v", cfg)
	}
}
