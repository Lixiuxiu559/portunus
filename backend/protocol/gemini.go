package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件定义 Gemini generateContent API 的结构，以及 gemini ↔ openai 的请求 / 响应 / 流式转换。

// GenerateContentRequest 是 Gemini generateContent 请求体。
type GenerateContentRequest struct {
	Contents          []GeminiContent   `json:"contents"`
	SystemInstruction *GeminiContent    `json:"systemInstruction,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	Tools             []GeminiTool      `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
}

// GeminiContent 是一条 Gemini 对话内容。
type GeminiContent struct {
	Role  string       `json:"role,omitempty"` // user / model
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart 是 content 的一个 part。
type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"` // 思考摘要（Gemini 2.5+ thought summaries），text 携带思考内容
	InlineData       *InlineData             `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
}

// InlineData 是内联二进制数据。
type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

// GeminiFunctionCall 是 Gemini 函数调用。
type GeminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// GeminiFunctionResponse 是 Gemini 函数调用结果。
type GeminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// GenerationConfig 是 Gemini 生成配置。
type GenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
	CandidateCount  int      `json:"candidateCount,omitempty"`
}

// GeminiTool 是 Gemini 工具定义。
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// GeminiFunctionDeclaration 是 Gemini 函数声明。
type GeminiFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ToolConfig 是 Gemini 工具调用配置。
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig 控制函数调用模式。
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode"` // AUTO / ANY / NONE
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GenerateContentResponse 是 Gemini 响应（非流式与流式 chunk 共用此结构）。
type GenerateContentResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

// GeminiCandidate 是一个候选结果。
type GeminiCandidate struct {
	Content      *GeminiContent `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
	Index        int            `json:"index,omitempty"`
}

// GeminiUsageMetadata 是 Gemini 用量元信息。
type GeminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// ===== 请求：gemini → openai =====

// geminiRequestToOpenAI 将 Gemini 请求体转换为 OpenAI 规范请求。
func geminiRequestToOpenAI(body []byte) (*ChatCompletionRequest, error) {
	var req GenerateContentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 gemini 请求失败: %w", err)
	}

	out := &ChatCompletionRequest{}
	if req.SystemInstruction != nil {
		out.Messages = append(out.Messages, ChatMessage{Role: "system", Content: geminiPartsToText(req.SystemInstruction.Parts)})
	}
	for _, c := range req.Contents {
		out.Messages = append(out.Messages, geminiContentToChat(c))
	}
	if req.GenerationConfig != nil {
		out.Temperature = req.GenerationConfig.Temperature
		out.TopP = req.GenerationConfig.TopP
		out.MaxTokens = req.GenerationConfig.MaxOutputTokens
		out.Stop = req.GenerationConfig.StopSequences
	}
	for _, t := range req.Tools {
		for _, fd := range t.FunctionDeclarations {
			out.Tools = append(out.Tools, Tool{
				Type: "function",
				Function: FunctionDef{
					Name:        fd.Name,
					Description: fd.Description,
					Parameters:  fd.Parameters,
				},
			})
		}
	}
	if req.ToolConfig != nil && req.ToolConfig.FunctionCallingConfig != nil {
		out.ToolChoice = geminiToolConfigToChoice(req.ToolConfig.FunctionCallingConfig)
	}
	return out, nil
}

// geminiContentToChat 将一条 Gemini content 转成 OpenAI ChatMessage。
func geminiContentToChat(c GeminiContent) ChatMessage {
	out := ChatMessage{}
	switch c.Role {
	case "user":
		out.Role = "user"
	case "model":
		out.Role = "assistant"
	default:
		out.Role = "user"
	}

	var textParts []string
	for _, p := range c.Parts {
		switch {
		case p.Thought && p.Text != "":
			// 思考摘要映射为 OpenAI 的 reasoning_content 扩展字段（DeepSeek 同款形状），
			// 经下游转换链透出（如转 Anthropic thinking 块）；不进正文 text。
			out.ReasoningContent += p.Text
		case p.Text != "":
			textParts = append(textParts, p.Text)
		case p.FunctionCall != nil:
			args, _ := json.Marshal(p.FunctionCall.Args)
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:       fmt.Sprintf("call_%d", len(out.ToolCalls)),
				Type:     "function",
				Function: FunctionCall{Name: p.FunctionCall.Name, Arguments: string(args)},
			})
		case p.FunctionResponse != nil:
			out.Role = "tool"
			out.ToolCallID = p.FunctionResponse.Name
			if resp, err := json.Marshal(p.FunctionResponse.Response); err == nil {
				out.Content = string(resp)
			}
		}
	}
	if len(textParts) > 0 {
		out.Content = strings.Join(textParts, "")
	}
	return out
}

// geminiToolConfigToChoice 把 Gemini 工具调用配置映射为 OpenAI tool_choice。
func geminiToolConfigToChoice(cfg *FunctionCallingConfig) any {
	switch cfg.Mode {
	case "NONE":
		return "none"
	case "ANY":
		return "required"
	default:
		return "auto"
	}
}

// ===== 请求：openai → gemini =====

// geminiRequestFromOpenAI 将 OpenAI 规范请求转为 Gemini 请求体。
func geminiRequestFromOpenAI(req *ChatCompletionRequest) ([]byte, error) {
	out := GenerateContentRequest{}

	for _, m := range req.Messages {
		if m.Role == "system" {
			out.SystemInstruction = &GeminiContent{Parts: textToGeminiParts(chatContentToText(m.Content))}
			continue
		}
		content := GeminiContent{Role: openAIRoleToGemini(m.Role)}
		switch m.Role {
		case "tool":
			content.Parts = append(content.Parts, GeminiPart{
				FunctionResponse: &GeminiFunctionResponse{
					Name:     m.ToolCallID,
					Response: textToMap(m.Content),
				},
			})
		case "assistant":
			if text := chatContentToText(m.Content); text != "" {
				content.Parts = append(content.Parts, GeminiPart{Text: text})
			}
			for _, tc := range m.ToolCalls {
				content.Parts = append(content.Parts, GeminiPart{
					FunctionCall: &GeminiFunctionCall{Name: tc.Function.Name, Args: jsonToMap(tc.Function.Arguments)},
				})
			}
		default: // user
			content.Parts = openAIContentToGeminiParts(m.Content)
		}
		out.Contents = append(out.Contents, content)
	}

	out.GenerationConfig = &GenerationConfig{
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   stopToStrings(req.Stop),
	}
	if out.GenerationConfig.MaxOutputTokens == nil && req.MaxCompletionTokens != nil {
		out.GenerationConfig.MaxOutputTokens = req.MaxCompletionTokens
	}

	var decls []GeminiFunctionDeclaration
	for _, t := range req.Tools {
		decls = append(decls, GeminiFunctionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	if len(decls) > 0 {
		out.Tools = []GeminiTool{{FunctionDeclarations: decls}}
	}
	if choice := openAIToolChoiceToGemini(req.ToolChoice); choice != "" {
		out.ToolConfig = &ToolConfig{FunctionCallingConfig: &FunctionCallingConfig{Mode: choice}}
	}
	return json.Marshal(out)
}

// openAIRoleToGemini 映射 OpenAI role → Gemini role。
func openAIRoleToGemini(role string) string {
	switch role {
	case "assistant":
		return "model"
	case "system":
		return "user"
	default:
		return "user"
	}
}

// openAIToolChoiceToGemini 映射 OpenAI tool_choice → Gemini 模式。
func openAIToolChoiceToGemini(choice any) string {
	switch v := choice.(type) {
	case string:
		switch v {
		case "none":
			return "NONE"
		case "required":
			return "ANY"
		}
	case map[string]any:
		if v["type"] == "function" {
			return "ANY"
		}
	}
	return ""
}

// textToGeminiParts 把纯文本转成 Gemini parts。
func textToGeminiParts(text string) []GeminiPart {
	if text == "" {
		return nil
	}
	return []GeminiPart{{Text: text}}
}

// openAIContentToGeminiParts 把 OpenAI 多模态 content 转成 Gemini parts。
func openAIContentToGeminiParts(content any) []GeminiPart {
	switch v := content.(type) {
	case string:
		return textToGeminiParts(v)
	case []any:
		var parts []GeminiPart
		for _, item := range v {
			part, _ := item.(map[string]any)
			t, _ := part["type"].(string)
			switch t {
			case "text":
				parts = append(parts, GeminiPart{Text: fmt.Sprint(part["text"])})
			case "image_url":
				if url, ok := part["image_url"].(map[string]any); ok {
					src := url["url"].(string)
					if strings.HasPrefix(src, "data:") {
						parts = append(parts, GeminiPart{InlineData: &InlineData{
							MimeType: mediaTypeFromDataURL(src),
							Data:     strings.SplitN(src, ",", 2)[1],
						}})
					}
				}
			}
		}
		return parts
	}
	return nil
}

// textToMap 把 OpenAI tool 消息 content（JSON 字符串）解析成 map。
func textToMap(content any) map[string]any {
	if s, ok := content.(string); ok {
		if m, err := jsonStringToMap(s); err == nil {
			return m
		}
	}
	return map[string]any{}
}

// jsonToMap 解析 JSON 字符串为 map（失败返回空 map）。
func jsonToMap(s string) map[string]any {
	m, _ := jsonStringToMap(s)
	return m
}

func jsonStringToMap(s string) (map[string]any, error) {
	m := map[string]any{}
	err := json.Unmarshal([]byte(s), &m)
	return m, err
}

// ===== 响应：gemini → openai =====

// geminiResponseToOpenAI 将 Gemini 响应体转换为 OpenAI 规范响应。
func geminiResponseToOpenAI(body []byte) (*ChatCompletionResponse, error) {
	var resp GenerateContentResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 gemini 响应失败: %w", err)
	}

	out := &ChatCompletionResponse{
		Object:  "chat.completion",
		Choices: []ChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant"}}},
	}
	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		if cand.Content != nil {
			out.Choices[0].Message = geminiContentToChat(*cand.Content)
		}
		out.Choices[0].FinishReason = geminiFinishToOpenAI(cand.FinishReason)
	}
	if resp.UsageMetadata != nil {
		u := usageFromGemini(*resp.UsageMetadata)
		out.Usage = &u
	}
	return out, nil
}

// geminiFinishToOpenAI 映射 finishReason → finish_reason。
func geminiFinishToOpenAI(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	}
	return ""
}

// ===== 响应：openai → gemini =====

// geminiResponseFromOpenAI 将 OpenAI 规范响应转为 Gemini 响应体。
func geminiResponseFromOpenAI(resp *ChatCompletionResponse) ([]byte, error) {
	out := GenerateContentResponse{}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		content := GeminiContent{Role: "model"}
		if text := chatContentToText(choice.Message.Content); text != "" {
			content.Parts = append(content.Parts, GeminiPart{Text: text})
		}
		for _, tc := range choice.Message.ToolCalls {
			content.Parts = append(content.Parts, GeminiPart{
				FunctionCall: &GeminiFunctionCall{Name: tc.Function.Name, Args: jsonToMap(tc.Function.Arguments)},
			})
		}
		out.Candidates = []GeminiCandidate{{
			Content:      &content,
			FinishReason: openAIFinishToGemini(choice.FinishReason),
			Index:        0,
		}}
	}
	if resp.Usage != nil {
		out.UsageMetadata = toGeminiUsage(resp.Usage)
	}
	return json.Marshal(out)
}

// openAIFinishToGemini 映射 finish_reason → finishReason。
func openAIFinishToGemini(reason string) string {
	switch reason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	}
	return "STOP"
}

// geminiPartsToText 把 Gemini parts 压成纯文本。
func geminiPartsToText(parts []GeminiPart) string {
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// ===== 流式：gemini → openai =====

// geminiToOpenAIStream 把 Gemini 流式 chunk 逐条映射为 OpenAI chunk。
type geminiToOpenAIStream struct {
	st openAIStreamState
}

func newGeminiToOpenAIStream() StreamConverter { return &geminiToOpenAIStream{} }

func (g *geminiToOpenAIStream) Convert(payload []byte) ([][]byte, error) {
	var resp GenerateContentResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("解析 gemini 流事件失败: %w", err)
	}
	var out [][]byte
	if resp.UsageMetadata != nil {
		// 每条 chunk 的 usageMetadata 均为累积值，最后一条生效。经 usageFromGemini
		// 归一：扣缓存并记 CacheRead，与非流式口径一致——此前流式不扣缓存也不记
		// CacheRead，缓存段被按全价 input 计费。
		u := usageFromGemini(*resp.UsageMetadata)
		g.st.usage = &u
	}
	if len(resp.Candidates) == 0 {
		return out, nil
	}
	cand := resp.Candidates[0]
	if cand.Content != nil {
		for _, p := range cand.Content.Parts {
			if p.Text == "" {
				continue
			}
			if b := g.st.emitRole("assistant"); b != nil {
				out = append(out, b)
			}
			if p.Thought {
				// 思考摘要增量 → reasoning_content（与 DeepSeek 流式同形状），
				// 不计入正文 text，下游转 thinking 块
				out = append(out, g.st.emitChunk(ChunkDelta{ReasoningContent: p.Text}, ""))
				continue
			}
			g.st.text.WriteString(p.Text)
			out = append(out, g.st.emitChunk(ChunkDelta{Content: p.Text}, ""))
		}
	}
	if cand.FinishReason != "" {
		g.st.finish = geminiFinishToOpenAI(cand.FinishReason)
	}
	return out, nil
}

func (g *geminiToOpenAIStream) Finish() ([][]byte, error) {
	if b := g.st.emitFinal(); b != nil {
		return [][]byte{b}, nil
	}
	return nil, nil
}

func (g *geminiToOpenAIStream) Usage() *Usage { return g.st.usage }

// ===== 流式：openai → gemini =====

// geminiBlockSink 把块生命周期回调映射为 Gemini 流式 chunk（由
// fromOpenAISkeleton 驱动）。Gemini 的 functionCall part 携带完整参数对象
// （没有增量 JSON 形态），因此工具参数由骨架累积、ToolComplete 时整包发出。
func newGeminiFromOpenAI() StreamConverter {
	return newFromOpenAISkeleton(&geminiBlockSink{})
}

type geminiBlockSink struct{}

func (g *geminiBlockSink) BeginMessage(string, string, *Usage) [][]byte { return nil }

// TextDelta / ReasoningDelta 每个增量一个 candidate chunk；思考摘要走
// thought 标记 part（Gemini 2.5+ 形状，与 gemini→openai 方向的解析对称）。
func (g *geminiBlockSink) TextDelta(s string) [][]byte {
	return g.candidate(GeminiContent{Role: "model", Parts: []GeminiPart{{Text: s}}})
}

func (g *geminiBlockSink) ReasoningDelta(s string) [][]byte {
	return g.candidate(GeminiContent{Role: "model", Parts: []GeminiPart{{Text: s, Thought: true}}})
}

func (g *geminiBlockSink) ToolStart(int, string, string) [][]byte { return nil }
func (g *geminiBlockSink) ToolDelta(int, string) [][]byte         { return nil }

// ToolComplete 发出完整的 functionCall part（openai 工具块关闭时由骨架回调）。
func (g *geminiBlockSink) ToolComplete(_ int, _, name, args string) [][]byte {
	return g.candidate(GeminiContent{Role: "model", Parts: []GeminiPart{
		{FunctionCall: &GeminiFunctionCall{Name: name, Args: jsonToMap(args)}},
	}})
}

func (g *geminiBlockSink) TextEnd() [][]byte { return nil }

// Finish 发收尾 chunk：finishReason 与 usageMetadata（经 toGeminiUsage 反向
// 还原）。与旧实现一致：未收到 finishReason 时不产出收尾 chunk——被截断的流
// 保持截断语义，客户端据此触发重试 / 告警，不凭空补 STOP 伪装自然完成。
func (g *geminiBlockSink) Finish(finishReason string, u *Usage) [][]byte {
	if finishReason == "" {
		return nil
	}
	resp := GenerateContentResponse{Candidates: []GeminiCandidate{{FinishReason: openAIFinishToGemini(finishReason)}}}
	resp.UsageMetadata = toGeminiUsage(u)
	b, _ := json.Marshal(resp)
	return [][]byte{b}
}

func (g *geminiBlockSink) candidate(content GeminiContent) [][]byte {
	b, _ := json.Marshal(GenerateContentResponse{
		Candidates: []GeminiCandidate{{Content: &content}},
	})
	return [][]byte{b}
}
