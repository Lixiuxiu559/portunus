package protocol

// 本文件定义 OpenAI Chat Completions 的请求 / 响应 / 流式 chunk 结构，
// 作为协议转换模块的内部规范格式（canonical）。其余协议均先转到该结构再转出。

// ChatCompletionRequest 是 OpenAI Chat Completions 请求体。
type ChatCompletionRequest struct {
	Model               string         `json:"model"`
	Messages            []ChatMessage  `json:"messages"`
	Temperature         *float64       `json:"temperature,omitempty"`
	TopP                *float64       `json:"top_p,omitempty"`
	MaxTokens           *int           `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int           `json:"max_completion_tokens,omitempty"`
	Stop                any            `json:"stop,omitempty"` // string 或 []string
	Stream              bool           `json:"stream,omitempty"`
	Tools               []Tool         `json:"tools,omitempty"`
	ToolChoice          any            `json:"tool_choice,omitempty"`
	StreamOptions       *StreamOptions `json:"stream_options,omitempty"`
	// Thinking 是思考开关意图（-thinking 后缀驱动，经 ComposeUpstreamRequest 的
	// UpstreamRequest.Thinking 显式设置），DeepSeek 等兼容方沿用 Anthropic 的
	// {type,budget_tokens} 形状。仅 openai 渲染把它写进上游请求体，不做任何
	// 默认开启——无条件转发曾导致部分上游 400（见 9a8e785）。
	Thinking *ThinkingConfig `json:"thinking,omitempty"`
}

// ThinkingConfig 是思考模式配置（Anthropic 与 DeepSeek 等兼容方同形状）。
type ThinkingConfig struct {
	Type         string `json:"type"` // enabled / auto / disabled
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// ChatMessage 是一条对话消息。
type ChatMessage struct {
	Role             string     `json:"role"` // system / user / assistant / tool
	Content          any        `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"` // DeepSeek 等兼容方的思考内容（thinking 模式多轮需回传）
	Name             *string    `json:"name,omitempty"`              // tool 消息的工具名；指针保证空串也输出（DeepSeek 等上游要求字段存在）
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

// ContentPart 是多模态 content 数组的一个单元。
type ContentPart struct {
	Type     string    `json:"type"` // text / image_url
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL 是图片 URL 内容。
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// Tool 是函数调用工具定义。
type Tool struct {
	Type     string      `json:"type"` // function
	Function FunctionDef `json:"function"`
}

// FunctionDef 是函数定义。
type FunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"` // JSON Schema
}

// ToolCall 是 assistant 消息里的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // function
	Function FunctionCall `json:"function"`
}

// FunctionCall 是工具调用的函数名与参数（JSON 字符串）。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamOptions 是流式相关选项。
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatCompletionResponse 是 OpenAI Chat Completions 非流式响应。
type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
}

// ChatChoice 是非流式响应里的一个候选。
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// Usage 及其归一化解析见 usage.go——「X → canonical Usage」的唯一数学归宿。

// ChatCompletionChunk 是 OpenAI Chat Completions 流式 chunk。
type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"` // chat.completion.chunk
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

// ChunkChoice 是流式 chunk 里的一个候选。
type ChunkChoice struct {
	Index        int        `json:"index"`
	Delta        ChunkDelta `json:"delta"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

// ChunkDelta 是流式 chunk 的增量内容。
type ChunkDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []ChunkToolCall `json:"tool_calls,omitempty"`
}

// ChunkToolCall 是流式 chunk 里的工具调用增量。
type ChunkToolCall struct {
	Index    int               `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function ChunkFunctionCall `json:"function,omitempty"`
}

// ChunkFunctionCall 是流式工具调用的函数名与参数增量。
type ChunkFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// stopToStrings 把 OpenAI 的 stop（string 或 []string，反序列化后为 string / []any）归一为 []string。
func stopToStrings(stop any) []string {
	switch v := stop.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
