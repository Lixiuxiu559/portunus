package shared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/resource"
)

var resourceEnvNames = []string{
	"MAX_REQUEST_BYTES", "MAX_RESPONSE_BYTES", "MAX_NATIVE_FRAGMENTS",
	"MAX_TOOL_ARGUMENTS_BYTES", "MAX_ACCUMULATED_BYTES", "MAX_JSON_DEPTH",
}

func clearResourceEnv(t *testing.T) {
	t.Helper()
	for _, name := range resourceEnvNames {
		t.Setenv("PORTUNUS_PROXY_"+name, "")
	}
}

func loadResourceConfig(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return Load(path)
}

func TestResourceConfigDefaultsAndCompatibility(t *testing.T) {
	if got := DefaultProxyConfig().ResourceLimits; got != resource.Defaults() {
		t.Fatalf("defaults=%+v", got)
	}
	for _, body := range []string{"", `{}`, `{"proxy":{"retry_count":1,"conversion_policy":"strict"}}`, `{"proxy":{"resource_limits":{}}}`, `{"proxy":{"resource_limits":{"max_request_bytes":0,"max_response_bytes":0,"max_native_fragments":0,"max_tool_arguments_bytes":0,"max_accumulated_bytes":0,"max_json_depth":0}}}`} {
		t.Run(body, func(t *testing.T) {
			clearResourceEnv(t)
			t.Setenv("PORTUNUS_PROXY_CONVERSION_POLICY", "strict")
			cfg, err := loadResourceConfig(t, body)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Proxy.ResourceLimits != resource.Defaults() {
				t.Fatalf("limits=%+v", cfg.Proxy.ResourceLimits)
			}
			if strings.Contains(body, `"retry_count":1`) && cfg.Proxy.RetryCount != 1 {
				t.Fatalf("旧配置 retry_count=%d", cfg.Proxy.RetryCount)
			}
			raw, err := json.Marshal(cfg.Proxy)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "conversion_policy") {
				t.Fatalf("obsolete policy retained: %s", raw)
			}
		})
	}
}

func TestResourceConfigPartialAndEnvironment(t *testing.T) {
	clearResourceEnv(t)
	cfg, err := loadResourceConfig(t, `{"proxy":{"resource_limits":{"max_request_bytes":4294967296,"max_json_depth":64}}}`)
	if err != nil {
		t.Fatal(err)
	}
	expected := resource.Defaults()
	expected.MaxRequestBytes = 4294967296
	expected.MaxJSONDepth = 64
	if cfg.Proxy.ResourceLimits != expected {
		t.Fatalf("partial=%+v, want %+v", cfg.Proxy.ResourceLimits, expected)
	}
	for _, name := range resourceEnvNames {
		t.Setenv("PORTUNUS_PROXY_"+name, "77")
	}
	t.Setenv("PORTUNUS_PROXY_MAX_RESPONSE_BYTES", "8589934592")
	cfg, err = loadResourceConfig(t, `{"proxy":{"resource_limits":{"max_request_bytes":999}}}`)
	if err != nil {
		t.Fatal(err)
	}
	expected = resource.Limits{MaxRequestBytes: 77, MaxResponseBytes: 8589934592, MaxNativeFragments: 77, MaxToolArgumentsBytes: 77, MaxAccumulatedBytes: 77, MaxJSONDepth: 77}
	if cfg.Proxy.ResourceLimits != expected {
		t.Fatalf("env=%+v, want %+v", cfg.Proxy.ResourceLimits, expected)
	}
	for _, name := range resourceEnvNames {
		t.Setenv("PORTUNUS_PROXY_"+name, "0")
	}
	cfg, err = loadResourceConfig(t, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.ResourceLimits != resource.Defaults() {
		t.Fatalf("zero env=%+v", cfg.Proxy.ResourceLimits)
	}
}

func TestResourceConfigRejectsNegative(t *testing.T) {
	for _, field := range []string{"max_request_bytes", "max_response_bytes", "max_native_fragments", "max_tool_arguments_bytes", "max_accumulated_bytes", "max_json_depth"} {
		t.Run(field, func(t *testing.T) {
			clearResourceEnv(t)
			body := `{"proxy":{"resource_limits":{"` + field + `":-1}}}`
			_, err := loadResourceConfig(t, body)
			if err == nil || !strings.Contains(err.Error(), "proxy.resource_limits."+field) {
				t.Fatalf("error=%v", err)
			}
			t.Setenv("PORTUNUS_PROXY_"+strings.ToUpper(field), "-1")
			_, err = loadResourceConfig(t, `{}`)
			if err == nil || !strings.Contains(err.Error(), "proxy.resource_limits."+field) {
				t.Fatalf("env error=%v", err)
			}
		})
	}
}
