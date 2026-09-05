package protocol

import (
	"encoding/json"
	"fmt"
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
	if from == to {
		return body, nil
	}
	req, err := parseRequestToOpenAI(from, body)
	if err != nil {
		return nil, err
	}
	return renderRequestFromOpenAI(to, req)
}

// ConvertResponse 将 from 协议的响应体转换为 to 协议的响应体。
func ConvertResponse(from, to Provider, body []byte) ([]byte, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("未知的目标协议: %s", to)
	}
	if from == to {
		return body, nil
	}
	resp, err := parseResponseToOpenAI(from, body)
	if err != nil {
		return nil, err
	}
	return renderResponseFromOpenAI(to, resp)
}

// UpstreamRequest 是装配上游请求时的覆盖项。零值字段表示「不覆盖」。
type UpstreamRequest struct {
	Model    string          // 上游模型名（渠道侧命名）；空 = 不替换
	Thinking *ThinkingConfig // -thinking 后缀驱动的思考开关意图；仅 openai 兼容上游消费
}

// ComposeUpstreamRequest 把客户端请求体装配为发往指定上游的请求体：
// 协议转换（from != to 经 canonical 中转）+ 上游模型名替换 + thinking 注入。
// gateway 不再自行改写请求体——模型名与思考开关落在哪个字段是协议知识，
// 归各协议自己：openai / anthropic / responses 在顶层 model 字段，gemini
// 在 URL（body 无模型字段，替换为 no-op）；thinking 仅 openai 兼容上游消费
// （DeepSeek/GLM 形状扩展），anthropic 上游靠客户端自带配置透传，gemini
// 上游无此形状。只写出显式请求的意图、不做任何默认开启——无条件转发
// thinking 曾导致部分上游 400（见 9a8e785）。
func ComposeUpstreamRequest(from, to Provider, body []byte, up UpstreamRequest) ([]byte, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("未知的源协议: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("未知的目标协议: %s", to)
	}
	if from == to {
		return rewriteSameProtocol(to, body, up)
	}
	req, err := parseRequestToOpenAI(from, body)
	if err != nil {
		return nil, err
	}
	applyUpstreamRequest(req, up)
	return renderRequestFromOpenAI(to, req)
}

// applyUpstreamRequest 把覆盖项写进 canonical 请求。Thinking 是否落进目标
// 请求体由各协议渲染决定（当前仅 openai 渲染经 canonical 字段带出）。
func applyUpstreamRequest(req *ChatCompletionRequest, up UpstreamRequest) {
	if up.Model != "" {
		req.Model = up.Model
	}
	if up.Thinking != nil {
		req.Thinking = up.Thinking
	}
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
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("解析 %s 请求失败: %w", p, err)
	}
	if up.Model != "" {
		m["model"] = up.Model
	}
	if up.Thinking != nil && p == ProviderOpenAI {
		thinking := map[string]any{"type": up.Thinking.Type}
		if up.Thinking.BudgetTokens > 0 {
			thinking["budget_tokens"] = up.Thinking.BudgetTokens
		}
		m["thinking"] = thinking
	}
	return json.Marshal(m)
}

// parseRequestToOpenAI 按 from 协议解析请求体为 OpenAI 规范请求。
func parseRequestToOpenAI(from Provider, body []byte) (*ChatCompletionRequest, error) {
	impl, ok := implFor(from)
	if !ok {
		return nil, fmt.Errorf("暂不支持解析 %s 请求", from)
	}
	return impl.parseRequest(body)
}

// renderRequestFromOpenAI 将 OpenAI 规范请求渲染为 to 协议请求体。
func renderRequestFromOpenAI(to Provider, req *ChatCompletionRequest) ([]byte, error) {
	impl, ok := implFor(to)
	if !ok {
		return nil, fmt.Errorf("暂不支持渲染 %s 请求", to)
	}
	return impl.renderRequest(req)
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
