package gateway

// 客户端可见等待时长基线（复现环 → 修复环）：上游不回响应头（排队挂死）时。
// 生产对照（2026-09-04 logs，lejurobot 渠道）：103 条 status=0 / duration≈60000ms
// 的卡死记录，旧默认 FirstByte=60s → 同目标重试烧满 (RetryCount+1)×60s，Claude Code
// 单请求最多挂 180s 才收到失败，连续失败后客户端指数退避到分钟级（"will retry in 3m 54s"）。
// 据此曾把默认 FirstByte 降为 20s；后发现高峰期 lejurobot 排队时正常请求的首包
// 也会超过 20s（30s~2min 的成功 200 大量存在），20s 会误杀慢而正常的请求，反而
// 制造额外失败与客户端退避，故回调为 60s。
// 2026-09-05 落地假死快速通道：不吐响应头的目标跳过同目标重试直接换下一家
// （relay.go isStall），配合熔断 stallOpenThreshold=2 快速开路——单目标分组下
// 客户端只等一个超时窗口。本测试把超时缩放为毫秒级，锁定修复后的等待时长。

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestClientWaitHeaderTimeoutSingleWindow(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 2 // 同生产默认：若退回同目标重试，会烧满 3 个窗口
	// 等头超时缩为 200ms（秒级配置表达不了毫秒，直接注入自定义 client）
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 200 * time.Millisecond
	customClient := &http.Client{Transport: tr}

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// 模拟上游排队挂死：不回响应头。不依赖 r.Context().Done()——Go server 在
		// handler 运行期间不感知客户端单方面放弃连接，等它会让 httptest.Server.Close()
		// 永久阻塞；2s 兜底自行退出（远大于 200ms 的客户端侧总耗时）。
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		d.Cfg = pc
		d.Breakers = NewBreakerStore(pc)
		d.Client = customClient
	}, protocol.ProviderOpenAI, upstream)
	start := time.Now()
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":true}`, key)
	elapsed := time.Since(start)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("假死目标应直接放弃并返回 504 上游超时语义，实际 %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("不吐响应头的目标不应同目标重试，上游应被调用 1 次，实际 %d", got)
	}
	// 客户端可见等待 = 1 个超时窗口（200ms）+ 少量开销。
	// 若退回同目标重试，elapsed 回到 3×200ms——本断言即变红。
	if elapsed >= 600*time.Millisecond {
		t.Errorf("客户端等待 %v，达到 3×200ms，假死目标被同目标重试了", elapsed)
	}
	t.Logf("客户端总等待 %v（1 个超时窗口）", elapsed)
}
