package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/Lixiuxiu559/portunus/backend/resource"
)

// 本文件是协议转换的非流式入口，所有转换都经 OpenAI 规范格式中转。

// ConvertRequest 将 from 协议的请求体转换为 to 协议的请求体。
// from == to 时原样返回。
func ConvertRequest(from, to Provider, body []byte) ([]byte, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("未知的目标协议: %s", to)
	}
	if err := inspectNativeResources(from, body, resource.Defaults(), resource.Request); err != nil {
		return nil, err
	}
	if from == to {
		return body, nil
	}
	req, err := parseRequestToOpenAI(from, body)
	if err != nil {
		return nil, err
	}
	out, err := renderRequestFromOpenAI(to, req)
	if err == nil {
		err = inspectNativeResources(to, out, resource.Defaults(), resource.Request)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ConvertResponse 将 from 协议的响应体转换为 to 协议的响应体。
func ConvertResponse(from, to Provider, body []byte) ([]byte, error) {
	out, _, err := ConvertResponseWithReport(from, to, body)
	return out, err
}

// UpstreamRequest 是装配上游请求时的覆盖项。零值字段表示「不覆盖」。
type UpstreamRequest struct {
	Limits   resource.Limits
	Model    string          // 上游模型名（渠道侧命名）；空 = 不替换
	Thinking *ThinkingConfig // -thinking 后缀驱动的思考开关意图；仅 openai 兼容上游消费
	// ThinkingCompat 渠道级 thinking 兼容垫片：开启后对 openai 兼容上游注入
	// assistant 历史缺失的 reasoning_content 占位（严格 thinking 上游缺失即 400，
	// 见 InjectReasoningPlaceholders）。
	ThinkingCompat bool
}

// ComposeStats 是装配上游请求时产生的诊断遥测，随返回值交给调用方写入调用日志。
// 它不参与请求体、不影响转换语义，只为让「reasoning 桥是否生效」「垫片改写了多少
// 历史」这两件此前只能靠猜的事变得可测量（写入方 gateway，读法见 shared.Log）。
type ComposeStats struct {
	Compatibility ConversionReport

	// BridgeDecoded 是从 thinking / redacted_thinking 块签名里成功还原原始
	// reasoning_content 的次数。跨协议入向（anthropic → openai）才有意义；
	// 恒为 0 而请求里确有思考块 → 客户端没回传本网关的桥载荷，桥接未生效
	// （见 reasoning_bridge.go）。
	BridgeDecoded int
	// ShimRewrites 是 thinking 兼容垫片注入占位的 assistant 消息条数，即
	// 「前缀漂移面积」：每改写一条历史消息，其后的上游自动前缀缓存即失效
	// （见 thinking_compat.go）。
	ShimRewrites int
	// HostedToolsStripped 是被剥离的托管工具声明计数：跨协议到不支持 hosted
	// 工具的 openai 上游时剔除的声明数，加上 canonical 无法承载、入站即丢弃的
	// 未知 hosted 类型数（如 code_execution_*）。web_search_* 能透传到 anthropic
	// 上游，只在发往 openai 时被剥离。gemini 入站识别到的未知 hosted 键不计入
	// （仅 anthropic/responses 入站计数）——源协议歧义在此说清，别指望数字对得上
	// 全部入站协议。
	HostedToolsStripped int
}

// ComposeUpstreamRequest 把客户端请求体装配为发往指定上游的请求体：
// 协议转换（from != to 经 canonical 中转）+ 上游模型名替换 + thinking 注入。
// gateway 不再自行改写请求体——模型名与思考开关落在哪个字段是协议知识，
// 归各协议自己：openai / anthropic / responses 在顶层 model 字段，gemini
// 在 URL（body 无模型字段，替换为 no-op）；thinking 仅 openai 兼容上游消费
// （DeepSeek/GLM 形状扩展），anthropic 上游靠客户端自带配置透传，gemini
// 上游无此形状。只写出显式请求的意图、不做任何默认开启——无条件转发
// thinking 曾导致部分上游 400（见 9a8e785）。
// 第二个返回值是本次装配的诊断遥测（ComposeStats），与请求体一同交给调用方。
func ComposeUpstreamRequest(from, to Provider, body []byte, up UpstreamRequest) ([]byte, ComposeStats, error) {
	envelope, err := CaptureRequest(from, body, up.Limits)
	if err != nil {
		return nil, ComposeStats{}, err
	}
	return envelope.PrepareTarget(to, up)
}

func composeUpstream(from, to Provider, body []byte, up UpstreamRequest) ([]byte, ComposeStats, error) {
	var stats ComposeStats
	if from == to {
		out, err := rewriteSameProtocol(to, body, up)
		return out, stats, err
	}
	req, err := parseRequestToOpenAI(from, body)
	if err != nil {
		return nil, stats, err
	}
	return composeParsedUpstream(to, req, up)
}

func composeParsedUpstream(to Provider, req *ChatCompletionRequest, up UpstreamRequest) ([]byte, ComposeStats, error) {
	var stats ComposeStats
	stats.BridgeDecoded = req.bridgeDecoded
	stats.HostedToolsStripped = req.hostedToolsStripped
	if up.Model != "" {
		req.Model = up.Model
	}
	// thinking 意图仅 openai 兼容上游消费：非 openai 目标不写入 canonical——
	// 死存储会让「装配意图」看似各协议自行取舍，一旦任一 renderer 将来读取
	// 该字段，-thinking 意图即静默漏进非 openai 上游（9a8e785 的 400 回归）。
	if up.Thinking != nil && to == ProviderOpenAI && req.Thinking == nil &&
		(req.reasoning == nil || (req.reasoning.mode != "disabled" &&
			(req.reasoning.effort == nil || *req.reasoning.effort != "none"))) {
		req.Thinking = up.Thinking
	}
	// 托管工具声明发往 openai 上游前剥离：openai 兼容上游没有 hosted 工具语义，
	// 带 type 的声明会被当成普通 function 或直接 400。同协议直通（rewriteSameProtocol）
	// 不在此列——直通天然保真，客户端自己发的 hosted 声明原样送出。
	if to == ProviderOpenAI {
		var kept []Tool
		for _, t := range req.Tools {
			if isHostedToolType(t.Type) {
				stats.HostedToolsStripped++
				continue
			}
			kept = append(kept, t)
		}
		req.Tools = kept
	}
	out, err := renderRequestFromOpenAI(to, req)
	return out, stats, err
}

// rewriteSameProtocol 同协议直通的顶层覆盖：model / thinking 的外科手术式
// 替换（map 往返）。不走 canonical 往返——客户端请求体里 canonical 之外的
// 未知字段原样保留，与 ConvertRequest 的直通语义（原样返回）保持同等保真度。
// gemini 请求体不含模型字段（在 URL），model 覆盖为 no-op；thinking 仅
// openai 形状存在，其余协议忽略。
func rewriteSameProtocol(p Provider, body []byte, up UpstreamRequest) ([]byte, error) {
	if up.Model == "" && up.Thinking == nil {
		return body, nil
	}
	if p == ProviderGemini {
		return body, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("解析 %s 请求失败: %w", p, err)
	}
	if m == nil {
		return nil, fmt.Errorf("解析 %s 请求失败: 请求必须为对象", p)
	}
	if up.Model != "" {
		m["model"], _ = json.Marshal(up.Model)
	}
	if up.Thinking != nil && p == ProviderOpenAI && !hasExplicitThinkingRaw(m) {
		thinking := map[string]any{"type": up.Thinking.Type}
		if up.Thinking.BudgetTokens > 0 {
			thinking["budget_tokens"] = up.Thinking.BudgetTokens
		}
		m["thinking"], _ = json.Marshal(thinking)
	}
	return json.Marshal(m)
}

// parseRequestToOpenAI 按 from 协议解析请求体为 OpenAI 规范请求。
func parseRequestToOpenAI(from Provider, body []byte) (*ChatCompletionRequest, error) {
	impl, ok := implFor(from)
	if !ok {
		return nil, fmt.Errorf("暂不支持解析 %s 请求", from)
	}
	reasoning, err := parseReasoning(from, body)
	if err != nil {
		return nil, err
	}
	req, err := impl.parseRequest(body)
	if err != nil {
		return nil, err
	}
	req.reasoning = reasoning
	return req, nil
}

// renderRequestFromOpenAI 将 OpenAI 规范请求渲染为 to 协议请求体。
func renderRequestFromOpenAI(to Provider, req *ChatCompletionRequest) ([]byte, error) {
	impl, ok := implFor(to)
	if !ok {
		return nil, fmt.Errorf("暂不支持渲染 %s 请求", to)
	}
	body, err := impl.renderRequest(req)
	if err != nil {
		return nil, err
	}
	return renderReasoning(to, body, req.reasoning, req.Thinking)
}

// parseResponseToOpenAI 按 from 协议解析响应体为 OpenAI 规范响应。
func parseResponseToOpenAI(from Provider, body []byte) (*ChatCompletionResponse, error) {
	impl, ok := implFor(from)
	if !ok {
		return nil, fmt.Errorf("暂不支持解析 %s 响应", from)
	}
	return impl.parseResponse(body)
}

// renderResponseFromOpenAI 将 OpenAI 规范响应渲染为 to 协议响应体。
func renderResponseFromOpenAI(to Provider, resp *ChatCompletionResponse) ([]byte, error) {
	impl, ok := implFor(to)
	if !ok {
		return nil, fmt.Errorf("暂不支持渲染 %s 响应", to)
	}
	return impl.renderResponse(resp)
}

// ===== openai 协议直通 =====
// openai 自身就是 canonical 格式，解析/渲染退化为普通 JSON 编解码。
// 直通实现内联在 provider_registry.go 的注册表 lambda 里（除 renderOpenAIRequest 含
// include_usage 注入逻辑、按具名函数保留在 registry 文件），此处不再重复。

// UsageFromResponse 从 from 协议的响应体中提取统一用量，供日志 / 计费。
// 解析失败或响应无 usage 时返回 nil。
func UsageFromResponse(from Provider, body []byte) *Usage {
	resp, err := parseResponseToOpenAI(from, body)
	if err != nil {
		return nil
	}
	return resp.Usage
}

// EstimateRequestTokens 对原始客户端请求体做粗略的输入 token 估算。
// 经 parseRequestToOpenAI 先归一为 OpenAI 规范再接 own 补偿，因此对
// OpenAI / Anthropic / Gemini / Responses 协议都能解析（依赖现有各协议解析器）。
// 用于上游未返回 usage 时填充流式 message_start 的 input_tokens，
// 让客户端能看到上下文占用。按文本 rune 数 / 4 估算（约 4 字符 ≈ 1 token），
// 仅为近似值，不替代真实计费。
func EstimateRequestTokens(from Provider, body []byte) int {
	req, err := parseRequestToOpenAI(from, body)
	if err != nil {
		return 0
	}
	runes := 0
	for _, m := range req.Messages {
		switch v := m.Content.(type) {
		case string:
			runes += len([]rune(v))
		case []any:
			for _, part := range v {
				if p, ok := part.(map[string]any); ok {
					if t, _ := p["text"].(string); t != "" {
						runes += len([]rune(t))
					}
				}
			}
		}
		for _, tc := range m.ToolCalls {
			runes += len([]rune(tc.Function.Name)) + len([]rune(tc.Function.Arguments))
		}
	}
	// 工具的 name/description/parameters 也计入输入
	for _, t := range req.Tools {
		runes += len([]rune(t.Function.Name)) + len([]rune(t.Function.Description))
	}
	if runes == 0 {
		return 0
	}
	return runes/4 + 1
}
