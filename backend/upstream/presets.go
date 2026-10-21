package upstream

import "github.com/Lixiuxiu559/portunus/backend/protocol"

type Preset struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	DefaultProfile string    `json:"default_profile"`
	Profiles       []Profile `json:"profiles"`
}

type Profile struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           protocol.Provider `json:"type"`
	BaseURL        string            `json:"base_url"`
	AdapterID      string            `json:"adapter_id"`
	ModelDiscovery string            `json:"model_discovery"`
	Help           string            `json:"help"`
	SourceURL      string            `json:"source_url,omitempty"`
}

// Presets 每次返回独立目录，调用方修改不会影响其他渠道或请求。
// 目录只提供配置预设；可用模型和调用权限以实际账号验证为准。
func Presets() []Preset {
	profile := func(id, name string, typ protocol.Provider, base, help string) Profile {
		return Profile{ID: id, Name: name, Type: typ, BaseURL: base, AdapterID: "generic", ModelDiscovery: "api", Help: help}
	}
	genericHelp := "填写包含版本及自定义路径前缀的 API 根地址；不自动补充 /v1。配置预设不代表已验证上游调用。"
	officialHelp := "仅配置预设，尚未进行真实账号调用验证；可修改 API 根地址，模型权限以实际账号为准。"
	chat := func(base, help string) Profile {
		return profile("chat", "Chat Completions", protocol.ProviderOpenAI, base, help)
	}
	responses := func(base, help string) Profile {
		return profile("responses", "Responses", protocol.ProviderOpenAIResponses, base, help)
	}
	anthropic := func(base, help string) Profile {
		return profile("messages", "Messages", protocol.ProviderAnthropic, base, help)
	}
	gemini := func(base, help string) Profile {
		return profile("generate_content", "Generate Content", protocol.ProviderGemini, base, help)
	}
	presets := []Preset{
		{ID: "generic", Name: "通用兼容服务", DefaultProfile: "chat", Profiles: []Profile{chat("", genericHelp), responses("", genericHelp), anthropic("", genericHelp), gemini("", genericHelp)}},
		{ID: "openai", Name: "OpenAI", DefaultProfile: "chat", Profiles: []Profile{chat("https://api.openai.com/v1", officialHelp), responses("https://api.openai.com/v1", officialHelp)}},
		{ID: "anthropic", Name: "Anthropic", DefaultProfile: "messages", Profiles: []Profile{anthropic("https://api.anthropic.com/v1", officialHelp)}},
		{ID: "gemini", Name: "Gemini", DefaultProfile: "generate_content", Profiles: []Profile{gemini("https://generativelanguage.googleapis.com/v1beta", officialHelp)}},
		{ID: "deepseek", Name: "DeepSeek", DefaultProfile: "chat", Profiles: []Profile{chat("https://api.deepseek.com", officialHelp)}},
		{ID: "openrouter", Name: "OpenRouter", DefaultProfile: "chat", Profiles: []Profile{chat("https://openrouter.ai/api/v1", officialHelp)}},
		{ID: "siliconflow", Name: "SiliconFlow", DefaultProfile: "chat", Profiles: []Profile{chat("https://api.siliconflow.cn/v1", officialHelp)}},
	}
	sources := map[string]string{
		"openai":      "https://developers.openai.com/api/reference/overview",
		"anthropic":   "https://platform.claude.com/docs/en/api/overview",
		"gemini":      "https://ai.google.dev/api/models",
		"deepseek":    "https://api-docs.deepseek.com/",
		"openrouter":  "https://openrouter.ai/docs/api/api-reference/models/get-models",
		"siliconflow": "https://api-docs.siliconflow.cn/docs/api/models-get",
	}
	for i := range presets {
		for j := range presets[i].Profiles {
			presets[i].Profiles[j].SourceURL = sources[presets[i].ID]
		}
	}
	return presets
}
