package protocol

// Provider 是 LLM API 协议类型，上游渠道与对外接口共用。
type Provider string

const (
	ProviderOpenAI          Provider = "openai"           // OpenAI Chat Completions
	ProviderOpenAIResponses Provider = "openai_responses" // OpenAI Responses
	ProviderAnthropic       Provider = "anthropic"        // Anthropic Messages
	ProviderGemini          Provider = "gemini"           // Google Gemini
)

// Valid 校验协议枚举。合法性与可分发性同源:注册到 providerImpls 即合法。
func (p Provider) Valid() bool {
	_, ok := providerImpls[p]
	return ok
}
