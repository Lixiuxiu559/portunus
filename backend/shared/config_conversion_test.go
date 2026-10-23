package shared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRemovedConversionPolicyIgnored(t *testing.T) {
	for _, tc := range []struct {
		name, body, env string
	}{
		{"缺省旧字段", `{}`, ""},
		{"旧空值", `{"proxy":{"conversion_policy":""}}`, ""},
		{"旧兼容策略", `{"proxy":{"conversion_policy":"legacy"}}`, ""},
		{"旧严格策略", `{"proxy":{"conversion_policy":"strict"}}`, ""},
		{"旧未知策略", `{"proxy":{"conversion_policy":"typo"}}`, ""},
		{"旧非字符串字段", `{"proxy":{"conversion_policy":{"obsolete":true}}}`, ""},
		{"旧严格环境变量", `{}`, "strict"},
		{"旧未知环境变量", `{}`, "typo"},
		{"旧字段及环境变量", `{"proxy":{"conversion_policy":"strict"}}`, "typo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PORTUNUS_PROXY_CONVERSION_POLICY", tc.env)
			t.Setenv("PORTUNUS_PROXY_MAX_HTTP_ATTEMPTS", "")
			t.Setenv("PORTUNUS_PROXY_FIRST_CONTENT_TIMEOUT_SECONDS", "")
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("已移除策略应作为未知配置忽略: %v", err)
			}
			if cfg.Proxy.MaxHTTPAttempts != 4 || cfg.Proxy.FirstContentTimeoutSeconds != 120 {
				t.Fatalf("请求安全限制发生变化: %+v", cfg.Proxy)
			}
			encoded, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var output struct {
				Proxy map[string]json.RawMessage `json:"proxy"`
			}
			if err := json.Unmarshal(encoded, &output); err != nil {
				t.Fatal(err)
			}
			if _, exists := output.Proxy["conversion_policy"]; exists {
				t.Fatalf("新配置不应再输出 conversion_policy: %s", encoded)
			}
		})
	}
}
