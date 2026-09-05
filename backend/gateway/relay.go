package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// relayMeta 是三种客户端协议请求体的最小公共字段。thinking 为 anthropic 与
// DeepSeek 等兼容方共用的顶层形状，随首次解码一并取出，供 -thinking 后缀复用
// budget_tokens（省去对请求体的第二次探测解码）。
type relayMeta struct {
	Model    string                   `json:"model"`
	Stream   bool                     `json:"stream"`
	Thinking *protocol.ThinkingConfig `json:"thinking,omitempty"`
}

// newRequestID 生成一次客户端请求的关联 ID：同一次请求对各上游的所有尝试共用，
// 落库 shared.Log.RequestID 并以 X-Request-Id 透传上游——lejurobot 这类中转上游
// 自身也带调用日志，两边按此 ID 对齐，排障不再两头靠时间戳猜。
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// thinkingSuffix 是「可控思考开关」的模型名后缀约定：对外模型名（分组名）带
// -thinking 表示本次调用要开思考。参考 new-api 的同名方案——开关跟随模型名走，
// 比全局转发 thinking 参数安全得多：不接受该参数的模型（glm-5.3 报 "cannot be
// disabled"、o 系列用 reasoning.effort）不带后缀即完全不受影响。
const thinkingSuffix = "-thinking"

// handleRelay 返回一个 relay handler，客户端协议由 clientProto 固定。
// 按分组策略遍历目标（外层），每个目标失败后按配置先重试 N 次（内层），
// 耗尽才换下一个目标；熔断开路的模型会被跳过。
func handleRelay(clientProto protocol.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := newRequestID()
		// 随响应头带给客户端：报障时把界面/客户端里的 req id 报上来即可精确定位。
		// 放在最早处，让 400 参数错这类失败也带得上 request id。
		c.Writer.Header().Set("X-Request-Id", requestID)

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeRelayError(c, clientProto, http.StatusBadRequest, relayErrInvalidRequest, "读取请求体失败")
			return
		}

		var meta relayMeta
		if err := json.Unmarshal(body, &meta); err != nil || meta.Model == "" {
			writeRelayError(c, clientProto, http.StatusBadRequest, relayErrInvalidRequest, "缺少 model 字段")
			return
		}

		// -thinking 后缀：剥掉后解析分组。思考开关意图（复用 anthropic 客户端
		// 自带的 budget_tokens，Claude Code 的 effort 就映射在这里）在此打包成
		// UpstreamRequest，是否写入上游请求体由 protocol 按上游协议决定——
		// gateway 不再触碰协议知识。
		groupName := meta.Model
		var upThinking *protocol.ThinkingConfig
		if strings.HasSuffix(groupName, thinkingSuffix) {
			groupName = strings.TrimSuffix(groupName, thinkingSuffix)
			upThinking = &protocol.ThinkingConfig{Type: "enabled"}
			if clientProto == protocol.ProviderAnthropic && meta.Thinking != nil && meta.Thinking.BudgetTokens > 0 {
				upThinking.BudgetTokens = meta.Thinking.BudgetTokens
			}
		}

		g, err := group.GetByName(groupName)
		if err != nil {
			writeRelayError(c, clientProto, http.StatusNotFound, relayErrNotFound, "分组不存在: "+groupName)
			return
		}

		targets, err := router.Resolve(g)
		if err != nil {
			writeRelayError(c, clientProto, http.StatusBadRequest, relayErrInvalidRequest, err.Error())
			return
		}

		apiKeyID := c.GetInt64("api_key_id")

		var lastErr error
		anyAttempted := false
		var skipped []string // 被熔断挡掉的目标描述，全跳过时用于日志
		for _, t := range targets {
			if allow, reason := breakerAllow(t.Model.ID); !allow {
				skipped = append(skipped, fmt.Sprintf("%s(id=%d) %s", t.Model.Name, t.Model.ID, reason))
				continue // 熔断开路，跳过该模型
			}
			for attempt := 0; attempt <= proxyCfg.RetryCount; attempt++ {
				anyAttempted = true
				out, status, err := relayToTarget(c, clientProto, meta.Stream, g, t, body, apiKeyID, requestID, upThinking)
				if err == nil {
					if !meta.Stream {
						c.Writer.Header().Set("Content-Type", "application/json")
						c.Writer.WriteHeader(status)
						c.Writer.Write(out)
					}
					return
				}
				lastErr = err
				d := failSpec(err)
				if !d.Retryable || d.StallNoHeader {
					break // 不可重试，或等头假死不重试：再等一轮超时大概率还是超时，直接换下一家
				}
			}
		}

		if !anyAttempted {
			// 503 的唯一出口必须留痕：stdout + 日志库（err_kind=circuit_open）。
			// 此前只写 stdout，管理端日志页零痕迹，用户报障只能登服务器翻容器日志。
			detail := "所有目标熔断开路: " + strings.Join(skipped, "; ")
			log.Printf("relay 拒绝: model=%s %s", meta.Model, detail)
			shared.LogDB.Create(&shared.Log{
				APIKeyID:  apiKeyID,
				GroupName: g.Name,
				Status:    http.StatusServiceUnavailable,
				Success:   false,
				Stream:    meta.Stream,
				RequestID: requestID,
				ErrKind:   errKindCircuitOpen,
				ErrMsg:    truncateErr(detail, 256),
			})
			// Retry-After 取各开路目标剩余冷却的最小值：最早冷却结束的那个即值得重试，
			// 客户端不必立刻重试撞墙，也不必盲等固定时长。
			retryAfter := 0
			for _, t := range targets {
				if s := breakerCooldownSeconds(t.Model.ID); s > 0 && (retryAfter == 0 || s < retryAfter) {
					retryAfter = s
				}
			}
			if retryAfter > 0 {
				c.Writer.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			}
			writeRelayError(c, clientProto, http.StatusServiceUnavailable, relayErrOverloaded, "所有渠道暂不可用")
			return
		}

		// 失败语义统一出自 failSpec：状态码、错误类别、上游 Retry-After 透传。
		// （等响应头 / 首包 / 非流式总超时是「上游超时未就绪」，failSpec 已裁为 504，
		// 与上游明确拒绝、网关侧连不上的 502 区分。）
		d := failSpec(lastErr)
		if d.RetryAfter != "" {
			// 上游限流 / 过载给出的重试节奏原样透传
			c.Writer.Header().Set("Retry-After", d.RetryAfter)
		}
		log.Printf("relay 失败: model=%s err=%v", meta.Model, lastErr)
		if len(skipped) > 0 {
			// 部分目标被熔断跳过、其余失败：跳过明细一并留痕，否则无从得知
			// failover 候选实际少了谁。
			log.Printf("relay 失败: 另有 %d 个目标被熔断跳过: %s", len(skipped), strings.Join(skipped, "; "))
		}
		// 上游有回话的失败取其错误原文，跨协议按客户端形状重组（原始体已落库不丢）；
		// 网关自身故障（超时 / 网络）用通用文案，不向客户端暴露内部细节。
		message := "上游调用失败"
		var se *upstreamStatusError
		if errors.As(lastErr, &se) {
			if m := upstreamErrorMessage(se.body); m != "" {
				message = m
			}
		}
		writeRelayError(c, clientProto, d.Status, d.RelayKind, message)
	}
}

// relayToTarget 对单个 target 完成一次 relay。
// 非流式：返回转换后的响应体（由调用方写入，以支持 failover 缓冲）；
// 流式：直接写客户端；首包前失败仍返回错误触发重试/换家，首包后（已提交）失败不再报错。
func relayToTarget(c *gin.Context, clientProto protocol.Provider, stream bool, g *group.Group, t router.Target, originalBody []byte, apiKeyID int64, requestID string, upThinking *protocol.ThinkingConfig) (out []byte, status int, err error) {
	start := time.Now()
	success := false
	var usage *protocol.Usage
	var first time.Time  // 流式首包写出时刻，非流式 / 首包前失败为零值
	var failReason error // committed 分支对外返回 nil 错误，真实失败原因暂存于此供日志归因
	defer func() {
		firstTokenMs := int64(0)
		if !first.IsZero() {
			firstTokenMs = first.Sub(start).Milliseconds()
		}
		logErr := err
		if logErr == nil {
			logErr = failReason
		}
		d := failSpec(logErr) // 失败语义唯一出口：归因、熔断喂法都读这份决定
		logCall(apiKeyID, g, t, status, success, stream, usage, time.Since(start).Milliseconds(), firstTokenMs, requestID, logErr)
		if !success {
			// 请求各阶段的失败都要留痕：dial 失败 / 看门狗超时 / 用户取消此前
			// 在 stdout 零日志，只能靠 DB 里的 status=0 反推。
			log.Printf("relay 尝试失败: group=%s model=%s channel=%s kind=%s err=%v",
				g.Name, t.Model.Name, t.Channel.Name, d.ErrKind, logErr)
		}
		if success {
			breakerRecord(t.Model.ID, false)
		} else if d.BreakerFail {
			if d.StallNoHeader {
				breakerRecordStall(t.Model.ID) // 等头假死按更低阈值快速开路，别让死模型拖垮每一轮请求
			} else {
				breakerRecord(t.Model.ID, true)
			}
		}
	}()

	// 装配上游请求：协议转换 + 模型名替换 + thinking 注入都在 protocol 内完成
	// （模型名落哪个字段、thinking 哪个上游消费，是协议知识，归 protocol）。
	upBody, err := protocol.ComposeUpstreamRequest(clientProto, t.Channel.Type, originalBody,
		protocol.UpstreamRequest{Model: t.Model.Name, Thinking: upThinking})
	if err != nil {
		return nil, 0, &convertError{err}
	}

	upstream, err := protocol.NewUpstream(t.Channel.Type, t.Channel.BaseURL, t.Channel.Key)
	if err != nil {
		return nil, 0, err
	}

	var wd *streamWatchdog
	reqCtx := c.Request.Context()
	if !stream {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, time.Duration(proxyCfg.NonStreamTimeoutSeconds)*time.Second)
		defer cancel()
	} else {
		// 流式：看门狗 ctx 贯穿请求与 body 读，触发时掐断读阻塞。
		// 首包计时在 relayStream 拿到响应头后才开始；等响应头由 Transport 兜底。
		reqCtx, wd = newStreamWatchdog(reqCtx,
			time.Duration(proxyCfg.FirstByteTimeoutSeconds)*time.Second,
			time.Duration(proxyCfg.StreamIdleTimeoutSeconds)*time.Second)
		defer wd.Stop()
	}

	headers := upstream.ChatHeaders()
	headers.Set("X-Request-Id", requestID)
	resp, err := doRequest(reqCtx, upstream.ChatURL(t.Model.Name, stream), headers, upBody)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	status = resp.StatusCode

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		log.Printf("上游返回 %d: %s", resp.StatusCode, string(errBody))
		return nil, status, &upstreamStatusError{status: status, body: errBody, retryAfter: resp.Header.Get("Retry-After")}
	}

	if stream {
		conv, convErr := protocol.NewStreamConverter(t.Channel.Type, clientProto)
		if convErr != nil {
			return nil, status, &convertError{convErr}
		}
		// 客户端是 Anthropic 时，message_start 需要 input_tokens 才能显示上下文占用；
		// 上游流式可能不返回 usage，先按客户端原始请求体估一个值兜底。
		if clientProto == protocol.ProviderAnthropic {
			if es, ok := conv.(protocol.EstimateSetter); ok {
				es.SetEstimateInputTokens(protocol.EstimateRequestTokens(clientProto, originalBody))
			}
		}
		var streamErr error
		first, streamErr = relayStream(c, clientProto, resp, conv, wd)
		if streamErr != nil {
			var committed *streamCommittedError
			if errors.As(streamErr, &committed) {
				// 已提交，无法 failover；记为失败但不再报错。
				// 已交付的部分流量仍可能累积了 usage，一并落库，避免费用漏记。
				usage = conv.Usage()
				// 存包装体而非解包的内层错误：failSpec 靠 errors.As 命中
				// streamCommittedError 才能归为 stream_interrupted，存内层会让
				// 该类别在生产路径不可达（Unwrap 已保证取消仍归 client_cancel）。
				failReason = committed
				// 熔断记账由 defer 统一处理：failSpec(committed) 对非取消的半途失败
				// 给出 BreakerFail=true（普通阈值计数），取消不计——不补记的话
				// 持续假死的模型永远开不了路，后续请求继续撞墙干等。
				// 失败明细由 defer 的「relay 尝试失败」统一留痕（kind=stream_interrupted）。
				return nil, status, nil
			}
			// 首包前失败，可重试 / 换家
			return nil, status, streamErr
		}
		usage = conv.Usage()
	} else {
		upRespBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, status, readErr
		}
		usage = protocol.UsageFromResponse(t.Channel.Type, upRespBody)
		out, err = protocol.ConvertResponse(t.Channel.Type, clientProto, upRespBody)
		if err != nil {
			return nil, status, &convertError{err}
		}
	}

	success = true
	return out, status, nil
}
