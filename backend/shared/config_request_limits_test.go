package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestLimitsConfig(t *testing.T) {
	for _, tc := range []struct {
		name, body, attemptsEnv, timeoutEnv string
		wantAttempts, wantTimeout           int
		wantErr                             string
	}{
		{name: "缺失文件", wantAttempts: 4, wantTimeout: 240},
		{name: "旧配置", body: `{"proxy":{"retry_count":1}}`, wantAttempts: 4, wantTimeout: 240},
		{name: "显式配置", body: `{"proxy":{"max_http_attempts":7,"first_content_timeout_seconds":90}}`, wantAttempts: 7, wantTimeout: 90},
		{name: "零值默认", body: `{"proxy":{"max_http_attempts":0,"first_content_timeout_seconds":0}}`, wantAttempts: 4, wantTimeout: 240},
		{name: "环境覆盖", body: `{"proxy":{"max_http_attempts":7,"first_content_timeout_seconds":90}}`, attemptsEnv: "2", timeoutEnv: "30", wantAttempts: 2, wantTimeout: 30},
		{name: "环境零值默认", body: `{"proxy":{"max_http_attempts":7,"first_content_timeout_seconds":90}}`, attemptsEnv: "0", timeoutEnv: "0", wantAttempts: 4, wantTimeout: 240},
		{name: "配置负预算", body: `{"proxy":{"max_http_attempts":-1}}`, wantErr: "proxy.max_http_attempts"},
		{name: "配置负期限", body: `{"proxy":{"first_content_timeout_seconds":-1}}`, wantErr: "proxy.first_content_timeout_seconds"},
		{name: "环境负预算", body: `{}`, attemptsEnv: "-1", wantErr: "proxy.max_http_attempts"},
		{name: "环境负期限", body: `{}`, timeoutEnv: "-1", wantErr: "proxy.first_content_timeout_seconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PORTUNUS_PROXY_MAX_HTTP_ATTEMPTS", tc.attemptsEnv)
			t.Setenv("PORTUNUS_PROXY_FIRST_CONTENT_TIMEOUT_SECONDS", tc.timeoutEnv)
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
			if cfg.Proxy.MaxHTTPAttempts != tc.wantAttempts || cfg.Proxy.FirstContentTimeoutSeconds != tc.wantTimeout {
				t.Fatalf("attempts=%d timeout=%d, want %d/%d", cfg.Proxy.MaxHTTPAttempts, cfg.Proxy.FirstContentTimeoutSeconds, tc.wantAttempts, tc.wantTimeout)
			}
			if tc.name == "旧配置" && cfg.Proxy.RetryCount != 1 {
				t.Fatalf("旧配置 retry_count=%d, want 1", cfg.Proxy.RetryCount)
			}
		})
	}
}

func TestDefaultRequestLimits(t *testing.T) {
	cfg := DefaultProxyConfig()
	if cfg.MaxHTTPAttempts != 4 || cfg.FirstContentTimeoutSeconds != 240 || cfg.FirstByteTimeoutSeconds != 120 || cfg.RetryCount != 2 {
		t.Fatalf("默认配置=%+v", cfg)
	}
}
