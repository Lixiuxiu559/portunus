package protocol

// providerImpl 汇总一个协议的全部转换能力:请求/响应的解析与渲染、
// 两个方向的流式转换器、上游接入器。convert.go / stream.go / upstream.go
// 的分发统一查 providerImpls,不再各自按 Provider switch。
type providerImpl struct {
	parseRequest   func([]byte) (*ChatCompletionRequest, error)
	renderRequest  func(*ChatCompletionRequest) ([]byte, error)
	parseResponse  func([]byte) (*ChatCompletionResponse, error)
	renderResponse func(*ChatCompletionResponse) ([]byte, error)
	newToStream    func() StreamConverter // from 协议 → OpenAI chunk
	newFromStream  func() StreamConverter // OpenAI chunk → to 协议
	newUpstream    func(baseURL, key string) (Upstream, error)
}

// providerImpls 是「Provider → 能力」的唯一事实来源。
// 加新协议 = 在此新增一个完整注册项,并实现对应的 7 个函数;合法性与分发都随之生效。
var providerImpls = map[Provider]providerImpl{
	ProviderOpenAI: {
		parseRequest:   parseOpenAIRequest,
		renderRequest:  renderOpenAIRequest,
		parseResponse:  parseOpenAIResponse,
		renderResponse: renderOpenAIResponse,
		newToStream:    newIdentityStream,
		newFromStream:  newIdentityStream,
		newUpstream:    newOpenAIUpstream,
	},
	ProviderOpenAIResponses: {
		parseRequest:   responsesRequestToOpenAI,
		renderRequest:  responsesRequestFromOpenAI,
		parseResponse:  responsesResponseToOpenAI,
		renderResponse: responsesResponseFromOpenAI,
		newToStream:    newResponsesToOpenAIStream,
		newFromStream:  newOpenAIToResponsesStream,
		newUpstream:    newResponsesUpstream,
	},
	ProviderAnthropic: {
		parseRequest:   anthropicRequestToOpenAI,
		renderRequest:  anthropicRequestFromOpenAI,
		parseResponse:  anthropicResponseToOpenAI,
		renderResponse: anthropicResponseFromOpenAI,
		newToStream:    newAnthropicToOpenAIStream,
		newFromStream:  newOpenAIToAnthropicStream,
		newUpstream:    newAnthropicUpstream,
	},
	ProviderGemini: {
		parseRequest:   geminiRequestToOpenAI,
		renderRequest:  geminiRequestFromOpenAI,
		parseResponse:  geminiResponseToOpenAI,
		renderResponse: geminiResponseFromOpenAI,
		newToStream:    newGeminiToOpenAIStream,
		newFromStream:  newOpenAIToGeminiStream,
		newUpstream:    newGeminiUpstream,
	},
}

// implFor 返回协议对应的实现,未注册返回 false。
func implFor(p Provider) (providerImpl, bool) {
	impl, ok := providerImpls[p]
	return impl, ok
}