package gateway

// 复现环：上游不回响应头（排队挂死）时，客户端必须等满 (RetryCount+1)×首包超时
// 才能拿到终态。生产对照（2026-09-04 logs，lejurobot 渠道）：103 条 status=0 /
// duration≈60000ms 的卡死记录，旧默认 FirstByte=60s → Claude Code 单请求最多挂
// 180s 才收到失败，连续失败后客户端指数退避到分钟级（"will retry in 3m 54s"）。
// 据此曾把默认 FirstByte 降为 20s；后发现高峰期 lejurobot 排队时正常请求的首包
// 也会超过 20s（30s~2min 的成功 200 大量存在），20s 会误杀慢而正常的请求，反而
// 制造额外失败与客户端退避，故回调为 60s——挂死等待的治理交给熔断与渠道并发限流。
// 本测试把超时缩放为毫秒级，锁定该客户端可见等待时长，作为后续修复的基线。

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestClientWaitAllHeaderTimeoutRetries(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 2              // 最多 3 次尝试，同生产默认
	pc.FirstByteTimeoutSeconds = 1 // 由 setResponseHeaderTimeout 缩放为 200ms
	setProxyConfigForTest(t, pc)
	setResponseHeaderTimeout(t, 200*time.Millisecond)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// 模拟上游排队挂死：不回响应头。不依赖 r.Context().Done()——Go server 在
		// handler 运行期间不感知客户端单方面放弃连接，等它会让 httptest.Server.Close()
		// 永久阻塞；2s 兜底自行退出（远大于 3×200ms 的客户端侧总耗时）。
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	start := time.Now()
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":true}`, key)
	elapsed := time.Since(start)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("超时耗尽应返回 502，实际 %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("上游应被调用 3 次，实际 %d", got)
	}
	// 客户端可见等待 = 3 次尝试 × 200ms 串行（当前行为）。
	// 若引入「客户端侧总预算」类修复，下界断言即变红——这是本环红的条件。
	if elapsed < 600*time.Millisecond {
		t.Errorf("客户端等待 %v，低于 3×200ms 下界，说明超时重试未串行占满", elapsed)
	}
	if elapsed >= 1500*time.Millisecond {
		t.Errorf("客户端等待 %v，超出 3×200ms+开销，存在额外挂起", elapsed)
	}
	t.Logf("客户端总等待 %v（3 次尝试 × 200ms 首包超时）", elapsed)
}
