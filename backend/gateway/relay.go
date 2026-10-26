package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/resource"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
)

// relayMeta 是三种客户端协议请求体的最小公共字段。thinking 为 anthropic 与
// DeepSeek 等兼容方共用的顶层形状，随首次解码一并取出，供 -thinking 后缀复用
// budget_tokens（省去对请求体的第二次探测解码）。存原文而非结构体：非对象
// 形状（字符串 / 布尔等自定义用法）不该让整个请求 400，仅在需要时按需解析。
type relayMeta struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Thinking json.RawMessage `json:"thinking,omitempty"`
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
func (s *relayServer) handleRelay(clientProto protocol.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 日志 writer 按请求复制，不改变共享 server；所有成功、失败和重试日志使用同一鉴权快照。
		requestServer := *s
		write := s.deps.LogWrite
		keyName := c.GetString("api_key_name")
		requestServer.deps.LogWrite = func(entry *shared.Log) {
			entry.APIKeyName = keyName
			write(entry)
		}
		s := &requestServer
		requestID := newRequestID()
		diagnostics := newRoutingDiagnostics(c, requestID, clientProto)
		defer diagnostics.flush(s, c)
		// 随响应头带给客户端：报障时把界面/客户端里的 req id 报上来即可精确定位。
		// 放在最早处，让 400 参数错这类失败也带得上 request id。
		c.Writer.Header().Set("X-Request-Id", requestID)

		limits := s.deps.Cfg.ResourceLimits.Normalize()
		if err := resource.CheckSize(c.Request.ContentLength, limits.MaxRequestBytes, resource.Request, "body_bytes"); err != nil {
			diagnostics.stop("resource_limit", err, "rejected")
			writeRelayError(c, clientProto, http.StatusRequestEntityTooLarge, relayErrInvalidRequest, "请求体超过资源上限")
			return
		}
		body, err := resource.ReadAll(c.Request.Body, limits.MaxRequestBytes, resource.Request)
		if err != nil {
			var limitErr *resource.LimitError
			if errors.As(err, &limitErr) {
				diagnostics.stop("resource_limit", err, "rejected")
				writeRelayError(c, clientProto, http.StatusRequestEntityTooLarge, relayErrInvalidRequest, "请求体超过资源上限")
				return
			}
			diagnostics.stop("request_read_failed", nil, "rejected")
			writeRelayError(c, clientProto, http.StatusBadRequest, relayErrInvalidRequest, "读取请求体失败")
			return
		}

		// Bound nesting before even the lightweight routing metadata decode.
		if err := resource.CheckDepth(body, limits.MaxJSONDepth, resource.Request); err != nil {
			diagnostics.stop("resource_limit", err, "rejected")
			writeRelayError(c, clientProto, http.StatusRequestEntityTooLarge, relayErrInvalidRequest, "请求结构超过资源上限")
			return
		}
		var meta relayMeta
		if err := json.Unmarshal(body, &meta); err != nil || meta.Model == "" {
			diagnostics.stop("request_invalid", nil, "rejected")
			writeRelayError(c, clientProto, http.StatusBadRequest, relayErrInvalidRequest, "缺少 model 字段")
			return
		}

		diagnostics.row.Stream = meta.Stream
		control := newRelayRequestControl(c.Request.Context(), s.deps.Cfg, meta.Stream)
		defer control.close()
		c.Request = c.Request.WithContext(control.ctx)

		// -thinking 后缀：剥掉后解析分组（后缀判定、分组名解析是分组 / 模型名知识，
		// 留在 gateway）；「thinking 落在哪个形状」是协议知识，交 protocol——
		// ThinkingEnabledIntent 复用 anthropic 客户端自带的 budget_tokens
		//（旧式预算，与 output_config.effort 档位独立）打包成意图，是否写入上游请求体由
		// protocol 按上游协议决定。
		groupName := meta.Model
		var upThinking *protocol.ThinkingConfig
		if strings.HasSuffix(groupName, thinkingSuffix) {
			groupName = strings.TrimSuffix(groupName, thinkingSuffix)
			upThinking = protocol.ThinkingEnabledIntent(clientProto, meta.Thinking)
		}

		g, err := group.GetByName(groupName)
		if err != nil {
			diagnostics.stop("group_not_found", nil, "rejected")
			writeRelayError(c, clientProto, http.StatusNotFound, relayErrNotFound, "分组不存在: "+groupName)
			return
		}

		diagnostics.row.GroupID, diagnostics.row.Strategy = g.ID, string(g.Strategy)
		targets, err := router.Resolve(g)
		if err != nil {
			diagnostics.stop("route_unresolved", nil, "rejected")
			writeRelayError(c, clientProto, http.StatusBadRequest, relayErrInvalidRequest, err.Error())
			return
		}

		diagnostics.row.CandidateCount = len(targets)
		apiKeyID := c.GetInt64("api_key_id")

		sequence, relaySequence := 0, 0
		var lastErr error
		anyAttempted := false
		var skipped []string // 被熔断挡掉的目标描述，全跳过时用于日志
		var cooling *rateLimitCooldownError
		var capacityBlocked *channelCapacityError
	targetsLoop:
		for candidateIndex, t := range targets {
			candidate := diagnostics.target(t, candidateIndex+1, 0)
			if stopErr := control.check(); stopErr != nil {
				lastErr = stopErr
				break targetsLoop
			}
			if s.deps.Cooldowns != nil {
				if blocked := s.deps.Cooldowns.Check(t); blocked != nil {
					if cooling == nil || blocked.RetryAfter < cooling.RetryAfter {
						cooling = blocked
					}
					candidate.event("candidate_skipped", "rate_limit_cooldown", blocked)
					skipped = append(skipped, fmt.Sprintf("%s(id=%d) 限流冷却中", t.Model.Name, t.Model.ID))
					continue
				}
			}
			if blocked := s.deps.Breakers.unavailable(t.Model.ID); blocked != nil {
				reason := blocked.Error()
				candidate.event("candidate_skipped", blocked.Reason, blocked)
				skipped = append(skipped, fmt.Sprintf("%s(id=%d) %s", t.Model.Name, t.Model.ID, reason))
				continue // 熔断开路，跳过该模型
			}

			if blocked := s.deps.Concurrency.Check(t.Channel.ID, t.Channel.MaxConcurrentRequests); blocked != nil {
				capacityBlocked = blocked
				candidate.event("candidate_skipped", "channel_capacity", blocked)
				skipped = append(skipped, fmt.Sprintf("%s(id=%d) 渠道并发已满", t.Model.Name, t.Model.ID))
				continue
			}
			targetEndReason := "retry_exhausted"
			budgetRetry := &chatBudgetRetry{} // 当前目标内记住字段切换，普通重试不重新触发兼容重试
			for attempt := 0; attempt <= s.deps.Cfg.RetryCount; attempt++ {
				if stopErr := control.available(); stopErr != nil {
					lastErr = stopErr
					break targetsLoop
				}
				if attempt > 0 {
					if blocked := s.deps.Breakers.unavailable(t.Model.ID); blocked != nil {
						candidate.event("candidate_skipped", blocked.Reason, blocked)
						targetEndReason = blocked.Reason
						break
					}
				}
				anyAttempted = true
				reason := "initial"
				if relaySequence > 0 {
					reason = "failover"
				}
				if attempt > 0 {
					reason = "retry"
				}
				relaySequence++
				selected := diagnostics.target(t, candidateIndex+1, relaySequence)
				selectReason := "primary"
				if candidateIndex > 0 {
					selectReason = "backup"
				}
				if attempt > 0 {
					selectReason = "retry"
				}
				selected.event("candidate_selected", selectReason, nil)
				diagnostics.row.FinalCandidate, diagnostics.row.FinalChannelID, diagnostics.row.FinalModelID = candidateIndex+1, t.Channel.ID, t.Model.ID
				trace := &relayAttemptTrace{routing: selected, sequence: &sequence, template: shared.UpstreamAttempt{
					RequestID: requestID, RelayAttempt: relaySequence, Reason: reason,
					APIKeyID: apiKeyID, GroupName: g.Name, ChannelID: t.Channel.ID,
					ModelID: t.Model.ID, ModelName: t.Model.Name,
					ClientProtocol: string(clientProto), UpstreamProtocol: string(t.Channel.Type), Stream: meta.Stream,
				}}
				out, status, err := s.relayToTarget(c, clientProto, meta.Stream, g, t, body, apiKeyID, requestID, upThinking, budgetRetry, trace)
				if err == nil {
					if diagnostics.lastSuccess {
						diagnostics.stop("relay_succeeded", nil, "relay_succeeded")
					} else {
						diagnostics.stop("committed_failure", diagnostics.lastErr, "relay_failed")
					}
					if !meta.Stream {
						c.Writer.Header().Set("Content-Type", "application/json")
						c.Writer.WriteHeader(status)
						diagnostics.row.DeliveryStarted = true
						if _, writeErr := c.Writer.Write(out); writeErr != nil {
							diagnostics.stop("client_write", nil, "relay_failed")
							diagnostics.row.StopErrKind = shared.ErrKindClientWrite
						}
					}
					return
				}
				lastErr = err
				d := failSpec(err)
				if d.RateLimited && s.deps.Cooldowns != nil {
					if blocked := s.deps.Cooldowns.Check(t); blocked != nil {
						cooling = blocked
					}
				}
				if d.StopRequest {
					break targetsLoop
				}
				if !d.Retryable || d.StallNoHeader {
					targetEndReason = "not_retryable"
					if d.StallNoHeader {
						targetEndReason = "header_stall"
					}
					break // 不可重试，或等头假死不重试：再等一轮超时大概率还是超时，直接换下一家
				}
			}
			candidate.event("target_exhausted", targetEndReason, lastErr)
		}

		if stopErr := control.check(); stopErr != nil {
			lastErr = stopErr
		}
		if !anyAttempted && lastErr == nil && cooling != nil {
			diagnostics.stop("no_available_target", cooling, "rejected")
			d := failSpec(cooling)
			s.deps.LogWrite(&shared.Log{APIKeyID: apiKeyID, GroupName: g.Name, Status: d.Status, Stream: meta.Stream, RequestID: requestID, ErrKind: d.ErrKind, ErrMsg: cooling.Error()})
			c.Writer.Header().Set("Retry-After", d.RetryAfter)
			writeRelayError(c, clientProto, d.Status, d.RelayKind, "目标正在限流冷却，请稍后重试")
			return
		}

		if !anyAttempted && lastErr == nil && capacityBlocked != nil {
			d := failSpec(capacityBlocked)
			diagnostics.stop("no_available_target", capacityBlocked, "rejected")
			s.deps.LogWrite(&shared.Log{APIKeyID: apiKeyID, GroupName: g.Name, Status: d.Status, Stream: meta.Stream, RequestID: requestID, ErrKind: d.ErrKind, ErrMsg: capacityBlocked.Error()})
			c.Writer.Header().Set("Retry-After", d.RetryAfter)
			writeRelayError(c, clientProto, d.Status, d.RelayKind, "渠道并发已满，请稍后重试")
			return
		}
		if !anyAttempted && lastErr == nil {
			// 503 的唯一出口必须留痕：stdout + 日志库（err_kind=circuit_open）。
			// 此前只写 stdout，管理端日志页零痕迹，用户报障只能登服务器翻容器日志。
			diagnostics.stop("no_available_target", &circuitUnavailableError{Reason: "circuit_open"}, "rejected")
			detail := "所有目标熔断开路: " + strings.Join(skipped, "; ")
			log.Printf("relay 拒绝: model=%s %s", meta.Model, detail)
			s.deps.LogWrite(&shared.Log{
				APIKeyID:  apiKeyID,
				GroupName: g.Name,
				Status:    http.StatusServiceUnavailable,
				Success:   false,
				Stream:    meta.Stream,
				RequestID: requestID,
				ErrKind:   shared.ErrKindCircuitOpen,
				ErrMsg:    truncateErr(detail, 256),
			})
			// Retry-After 取各开路目标剩余冷却的最小值：最早冷却结束的那个即值得重试，
			// 客户端不必立刻重试撞墙，也不必盲等固定时长。
			retryAfter := 0
			for _, t := range targets {
				if s2 := s.deps.Breakers.CooldownSeconds(t.Model.ID); s2 > 0 && (retryAfter == 0 || s2 < retryAfter) {
					retryAfter = s2
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
		stopReason := routingStopReason(lastErr)
		if sequence == 0 && stopReason == "candidates_exhausted" {
			stopReason = "no_available_target"
		}
		diagnostics.stop(stopReason, lastErr, "relay_failed")
		d := failSpec(lastErr)
		if d.RetryAfter != "" {
			// 上游限流 / 过载给出的重试节奏原样透传
			c.Writer.Header().Set("Retry-After", d.RetryAfter)
		} else if d.RateLimited && cooling != nil {
			c.Writer.Header().Set("Retry-After", strconv.Itoa(cooling.RetryAfter))
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
		var budgetErr *attemptBudgetError
		var deadlineErr *firstContentDeadlineError
		var nonStreamDeadline *nonStreamDeadlineError
		var clientDeadline *clientDeadlineError
		if errors.As(lastErr, &clientDeadline) || errors.As(lastErr, &budgetErr) || errors.As(lastErr, &deadlineErr) || errors.As(lastErr, &nonStreamDeadline) {
			message = lastErr.Error()
		}
		var resourceErr *resource.LimitError
		if errors.As(lastErr, &resourceErr) {
			message = "调用数据超过转换资源上限"
		}
		var se *upstreamStatusError
		if errors.As(lastErr, &se) {
			if m := upstreamErrorMessage(se.body); m != "" {
				message = m
			}
		}
		var reasoningErr *protocol.ReasoningConversionError
		if errors.As(lastErr, &reasoningErr) {
			message = reasoningErr.Error()
		}
		writeRelayError(c, clientProto, d.Status, d.RelayKind, message)
	}
}

// relayToTarget 对单个 target 完成一次 relay。
// 非流式：返回转换后的响应体（由调用方写入，以支持 failover 缓冲）；
// 流式：直接写客户端；首包前失败仍返回错误触发重试/换家，首包后（已提交）失败不再报错。
func (s *relayServer) relayToTarget(c *gin.Context, clientProto protocol.Provider, stream bool, g *group.Group, t router.Target, originalBody []byte, apiKeyID int64, requestID string, upThinking *protocol.ThinkingConfig, budgetRetry *chatBudgetRetry, trace *relayAttemptTrace) (out []byte, status int, err error) {
	if controlFrom(c.Request.Context()) == nil {
		control := newRelayRequestControl(c.Request.Context(), s.deps.Cfg, stream)
		originalRequest := c.Request
		c.Request = c.Request.WithContext(control.ctx)
		defer func() { c.Request = originalRequest; control.close() }()
	}
	limits := s.deps.Cfg.ResourceLimits.Normalize()
	start := time.Now()
	success := false
	var usage *protocol.Usage
	var first time.Time // 流式首包写出时刻，非流式 / 首包前失败为零值
	var rateLimitRetryAfter string
	rateLimitRecorded := false
	var failReason error // committed 分支对外返回 nil 错误，真实失败原因暂存于此供日志归因
	// stats 是装配上游请求时产生的诊断遥测。在 defer 之前声明，供下面的日志闭包捕获。
	var stats protocol.ComposeStats
	var deliveryReport streamDeliveryReport
	permit := &breakerAttempt{store: s.deps.Breakers, modelID: t.Model.ID}
	capacity := &channelCapacityAttempt{store: s.deps.Concurrency, channelID: t.Channel.ID, limit: t.Channel.MaxConcurrentRequests}
	if trace != nil {
		permit.routing = trace.routing
		capacity.routing = trace.routing
	}
	defer func() {
		firstTokenMs := int64(0)
		if !first.IsZero() {
			firstTokenMs = first.Sub(start).Milliseconds()
		}
		logErr := err
		if logErr == nil {
			logErr = failReason
		}
		durationMs := time.Since(start).Milliseconds() // 采样先于新增记录写库，保持旧日志耗时边界
		d := failSpec(logErr)                          // 失败语义唯一出口：归因、熔断喂法都读这份决定
		// Body/stream defers run before this point. Release health admission before
		// observability writers, which may block or fail independently of the upstream.
		permit.finish(success, d)
		capacity.release()
		if trace != nil {
			trace.routing.finished(success, logErr, deliveryReport.Started)
		}
		s.finishAttempt(trace, status, success, logErr)
		if s.deps.Cooldowns != nil && !rateLimitRecorded && d.RateLimited {
			s.deps.Cooldowns.Record(t, rateLimitRetryAfter)
		}
		s.logCall(apiKeyID, g, t, clientProto, stats, status, success, stream, usage, durationMs, firstTokenMs, requestID, logErr, &deliveryReport)
		if !success && !s.quiet {
			// 请求各阶段的失败都要留痕：dial 失败 / 看门狗超时 / 用户取消此前
			// 在 stdout 零日志，只能靠 DB 里的 status=0 反推。
			log.Printf("relay 尝试失败: group=%s model=%s channel=%s kind=%s err=%v",
				g.Name, t.Model.Name, t.Channel.Name, d.ErrKind, logErr)
		}
	}()

	// 装配上游请求：协议转换 + 模型名替换 + thinking 注入都在 protocol 内完成
	// （模型名落哪个字段、thinking 哪个上游消费，是协议知识，归 protocol）。
	// 第三个返回值是装配遥测（桥接命中 / 垫片改写），交给 defer 里的日志闭包。
	var upBody []byte
	upBody, stats, err = protocol.ComposeUpstreamRequest(clientProto, t.Channel.Type, originalBody,
		protocol.UpstreamRequest{Model: t.Model.Name, Thinking: upThinking, ThinkingCompat: t.Channel.ThinkingCompat, Limits: limits})
	if err != nil {
		return nil, 0, &convertError{err}
	}

	replaySafe := protocol.StreamRequestReplaySafe(clientProto, originalBody)

	connection, err := upstream.Open(t.Channel.ConnectionConfig())
	if err != nil {
		return nil, 0, err
	}

	var wd *streamWatchdog
	reqCtx := c.Request.Context()
	reqCtx = context.WithValue(reqCtx, rateLimitTargetContextKey{}, t)
	reqCtx = context.WithValue(reqCtx, breakerAttemptContextKey{}, permit)
	reqCtx = context.WithValue(reqCtx, channelCapacityContextKey{}, capacity)
	if stream {
		// 流式：看门狗 ctx 贯穿请求与 body 读，触发时掐断读阻塞。
		// 首包计时在 relayStream 拿到响应头后才开始；等响应头由 Transport 兜底。
		reqCtx, wd = newStreamWatchdog(reqCtx,
			time.Duration(s.deps.Cfg.FirstByteTimeoutSeconds)*time.Second,
			time.Duration(s.deps.Cfg.StreamIdleTimeoutSeconds)*time.Second)
		defer wd.Stop()
	}

	headers := connection.ChatHeaders()
	headers.Set("X-Request-Id", requestID)
	allowBudgetRetry := clientProto != protocol.ProviderOpenAI && t.Channel.Type == protocol.ProviderOpenAI
	resp, err := s.doRequestWithChatBudgetFallback(reqCtx, connection.ChatURL(t.Model.Name, stream), headers, upBody, allowBudgetRetry, budgetRetry, trace)
	if err != nil {
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		// Capacity rejection before acquiring a slot proves this relay never sent.
		// Choosing another target here is initial admission, not replay of an operation.
		var capacityErr *channelCapacityError
		unsentCapacity := errors.As(err, &capacityErr) && capacity.lease == nil
		if !unsentCapacity && !replaySafe && !failSpec(err).StopRequest {
			err = &streamReplayUnsafeError{err: err}
		}
		return nil, status, err
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	rateLimitRetryAfter = resp.Header.Get("Retry-After")
	if status == http.StatusTooManyRequests && s.deps.Cooldowns != nil {
		// A slow/error body must not postpone an already observed rate limit.
		s.deps.Cooldowns.Record(t, rateLimitRetryAfter)
		rateLimitRecorded = true
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, readErr := readUpstreamBody(resp, limits.MaxResponseBytes)
		if stopErr := requestControlError(reqCtx); stopErr != nil {
			return nil, status, stopErr
		}
		var limitErr *resource.LimitError
		if errors.As(readErr, &limitErr) {
			return nil, status, readErr
		}
		if !s.quiet {
			log.Printf("上游返回 %d: %s", resp.StatusCode, string(errBody))
		}
		statusErr := &upstreamStatusError{status: status, body: errBody, retryAfter: resp.Header.Get("Retry-After")}
		if (status >= 500 || status == http.StatusTooManyRequests) && !replaySafe {
			return nil, status, &streamReplayUnsafeError{err: statusErr}
		}
		return nil, status, statusErr
	}

	if stream {
		// message_start 需要 input_tokens 才能显示上下文占用（anthropic 客户端）；
		// 上游流式可能不返回 usage，惰性估算兜底——thunk 仅 anthropic 转换器调用，
		// 其他协议不付 body 解析开销，gateway 无须判断客户端协议。
		conv, convErr := protocol.NewStreamConverter(t.Channel.Type, clientProto, protocol.WithResourceLimits(limits),
			protocol.WithInputEstimate(func() int {
				return protocol.EstimateRequestTokens(clientProto, originalBody)
			}))
		if convErr != nil {
			return nil, status, &convertError{convErr}
		}
		var streamErr error
		first, streamErr = relayStream(c, clientProto, resp, conv, wd, streamRelayOptions{Source: t.Channel.Type, ReplayUnsafe: !replaySafe, Report: &deliveryReport, OnRateLimit: func() {
			if s.deps.Cooldowns != nil && !rateLimitRecorded {
				s.deps.Cooldowns.Record(t, rateLimitRetryAfter)
				rateLimitRecorded = true
			}
		}})
		usage = conv.Usage()
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
		upRespBody, readErr := readUpstreamBody(resp, limits.MaxResponseBytes)
		if stopErr := requestControlError(reqCtx); stopErr != nil {
			return nil, status, stopErr
		}
		if readErr != nil {
			if !replaySafe {
				return nil, status, &streamReplayUnsafeError{err: readErr}
			}
			return nil, status, readErr
		}
		out, deliveryReport.ResponseCompatibility, err = protocol.ConvertResponseWithReport(t.Channel.Type, clientProto, upRespBody, limits)
		if stopErr := requestControlError(reqCtx); stopErr != nil {
			return nil, status, stopErr
		}
		if err != nil {
			conversionErr := &convertError{err}
			if !replaySafe {
				return nil, status, &streamReplayUnsafeError{err: conversionErr}
			}
			return nil, status, conversionErr
		}
		usage = protocol.UsageFromResponse(t.Channel.Type, upRespBody)
		// 完整响应与转换均在请求累计期限内完成，之后交给调用方写客户端。
		if control := controlFrom(c.Request.Context()); control != nil {
			if stopErr := control.commit(); stopErr != nil {
				return nil, status, stopErr
			}
		}
	}

	success = true
	return out, status, nil
}
