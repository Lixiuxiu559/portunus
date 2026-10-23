package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRateLimitCooldownConfig(t *testing.T) {
	for _, tc := range []struct {
		name, body, baseEnv, maxEnv, wantErr string
		base, max                            int
	}{
		{name: "default", base: 30, max: 300},
		{name: "legacy", body: `{"proxy":{"retry_count":1}}`, base: 30, max: 300},
		{name: "configured", body: `{"proxy":{"rate_limit_cooldown_seconds":60,"rate_limit_max_cooldown_seconds":90}}`, base: 60, max: 90},
		{name: "zero", body: `{"proxy":{"rate_limit_cooldown_seconds":0,"rate_limit_max_cooldown_seconds":0}}`, base: 30, max: 300},
		{name: "env", baseEnv: "12", maxEnv: "24", base: 12, max: 24},
		{name: "env zero", baseEnv: "0", maxEnv: "0", base: 30, max: 300},
		{name: "invalid env ignored", baseEnv: "no", maxEnv: "x", base: 30, max: 300},
		{name: "negative base", body: `{"proxy":{"rate_limit_cooldown_seconds":-1}}`, wantErr: "proxy.rate_limit_cooldown_seconds"},
		{name: "negative max", maxEnv: "-1", wantErr: "proxy.rate_limit_max_cooldown_seconds"},
		{name: "max below base", baseEnv: "60", maxEnv: "30", wantErr: "不能小于"},
		{name: "normalized max below base", baseEnv: "301", maxEnv: "0", wantErr: "不能小于"},
		{name: "normalized base above max", baseEnv: "0", maxEnv: "29", wantErr: "不能小于"},
		{name: "equal", baseEnv: "30", maxEnv: "30", base: 30, max: 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PORTUNUS_PROXY_RATE_LIMIT_COOLDOWN_SECONDS", tc.baseEnv)
			t.Setenv("PORTUNUS_PROXY_RATE_LIMIT_MAX_COOLDOWN_SECONDS", tc.maxEnv)
			path := filepath.Join(t.TempDir(), "config.json")
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := Load(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Proxy.RateLimitCooldownSeconds != tc.base || c.Proxy.RateLimitMaxCooldownSeconds != tc.max {
				t.Fatalf("cooldown=%d/%d want %d/%d", c.Proxy.RateLimitCooldownSeconds, c.Proxy.RateLimitMaxCooldownSeconds, tc.base, tc.max)
			}
		})
	}
}
