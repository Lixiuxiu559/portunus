package protocol

import (
	"encoding/json"
	"fmt"
)

// providerImpl 汇总一个协议的全部转换能力:请求/响应的解析与渲染、
// 两个方向的流式转换器、直通流的 usage 提取、上游接入器。convert.go / stream.go /
// upstream.go 的分发统一查 providerImpls,不再各自按 Provider switch。
type providerImpl struct {
	parseRequest   func([]byte) (*ChatCompletionRequest, error)
	renderRequest  func(*ChatCompletionRequest) ([]byte, error)
	parseResponse  func([]byte) (*ChatCompletionResponse, error)
	renderResponse func(*ChatCompletionResponse) ([]byte, error)
	newToStream    func() StreamConverter // from 协议 → OpenAI chunk
	newFromStream  func() StreamConverter // OpenAI chunk → to 协议
	// newUsageExtractor 返回同协议直通流的 usage 提取闭包（实现见 usage.go）。
	// 编译期穷尽：新增协议必须实现，否则直通流式计费丢 token。
	newUsageExtractor func() func([]byte) *Usage
	newUpstream       func(baseConfig) Upstream
}

// renderOpenAIRequest 渲染 OpenAI 请求体。openai 自身就是 canonical,仅此一步带直通特有逻辑:
// 跨协议转成 OpenAI 的流式请求默认带上 include_usage,否则 OpenAI 兼容上游默认不返回
// usage chunk,客户端看不到上下文占用。直通(from==to)不经过这里,客户端自己的 stream_options 原样保留。
func renderOpenAIRequest(req *ChatCompletionRequest) ([]byte, error) {
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	return json.Marshal(req)
}

// providerImpls 是「Provider → 能力」的唯一事实来源。
// 加新协议 = 在此新增一个完整注册项,并实现对应的 8 个函数;合法性与分发都随之生效。
var providerImpls = map[Provider]providerImpl{
	ProviderOpenAI: {
		parseRequest: func(body []byte) (*ChatCompletionRequest, error) {
			var req ChatCompletionRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, fmt.Errorf("解析 openai 请求失败: %w", err)
			}
			return &req, nil
		},
		renderRequest: renderOpenAIRequest,
		parseResponse: func(body []byte) (*ChatCompletionResponse, error) {
			var resp ChatCompletionResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				return nil, fmt.Errorf("解析 openai 响应失败: %w", err)
			}
			return &resp, nil
		},
		renderResponse:    func(resp *ChatCompletionResponse) ([]byte, error) { return json.Marshal(resp) },
		newToStream:       newIdentityStream,
		newFromStream:     newIdentityStream,
		newUsageExtractor: newOpenAIUsageExtractor,
		newUpstream:       newBearerUpstream("/chat/completions"),
	},
	ProviderOpenAIResponses: {
		parseRequest:      responsesRequestToOpenAI,
		renderRequest:     responsesRequestFromOpenAI,
		parseResponse:     responsesResponseToOpenAI,
		renderResponse:    responsesResponseFromOpenAI,
		newToStream:       newResponsesToOpenAIStream,
		newFromStream:     newOpenAIToResponsesStream,
		newUsageExtractor: newResponsesUsageExtractor,
		newUpstream:       newBearerUpstream("/responses"),
	},
	ProviderAnthropic: {
		parseRequest:      anthropicRequestToOpenAI,
		renderRequest:     anthropicRequestFromOpenAI,
		parseResponse:     anthropicResponseToOpenAI,
		renderResponse:    anthropicResponseFromOpenAI,
		newToStream:       newAnthropicToOpenAIStream,
		newFromStream:     newOpenAIToAnthropicStream,
		newUsageExtractor: newAnthropicUsageExtractor,
		newUpstream:       newAnthropicUpstream,
	},
	ProviderGemini: {
		parseRequest:      geminiRequestToOpenAI,
		renderRequest:     geminiRequestFromOpenAI,
		parseResponse:     geminiResponseToOpenAI,
		renderResponse:    geminiResponseFromOpenAI,
		newToStream:       newGeminiToOpenAIStream,
		newFromStream:     newOpenAIToGeminiStream,
		newUsageExtractor: newGeminiUsageExtractor,
		newUpstream:       newGeminiUpstream,
	},
}

// implFor 返回协议对应的实现,未注册返回 false。
func implFor(p Provider) (providerImpl, bool) {
	impl, ok := providerImpls[p]
	return impl, ok
}
