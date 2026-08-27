package gateway

import (
	"strings"
	"testing"
)

func TestRewriteModel(t *testing.T) {
	in := []byte(`{"model":"group-name","messages":[{"role":"user","content":"hi"}]}`)
	out, err := rewriteModel(in, "gpt-4o")
	if err != nil {
		t.Fatalf("rewriteModel 失败: %v", err)
	}
	if !strings.Contains(string(out), `"model":"gpt-4o"`) {
		t.Errorf("model 未改写: %s", out)
	}
	if strings.Contains(string(out), "group-name") {
		t.Errorf("group-name 不应残留: %s", out)
	}
}
