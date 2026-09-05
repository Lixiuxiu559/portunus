package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件定义 OpenAI Responses API 的结构，以及 responses ↔ openai chat 的请求 / 响应 / 流式转换。

// ResponsesRequest 是 OpenAI Responses 请求体。
type ResponsesRequest struct {
	Model           string          `json:"model"`
	Input           any             `json:"input,omitempty"` // string 或 []InputItem
	Instructions    string          `json:"instructions,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	MaxOutputTokens *int            `json:"max_output_tokens,omitempty"`
	Tools           []ResponsesTool `json:"tools,omitempty"`
	ToolChoice      any             `json:"tool_choice,omitempty"`
}

// ResponsesTool 是 Responses 工具定义。
type ResponsesTool struct {
	Type        string `json:"type"` // function
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
	Strict      bool   `json:"strict,omitempty"`
}

// InputItem 是 responses input 的一个单元。
type InputItem struct {
	Type      string             `json:"type"` // message / function_call / function_call_output
	Role      string             `json:"role,omitempty"`
	Content   []ResponsesContent `json:"content,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Output    string             `json:"output,omitempty"`
}

// ResponsesContent 是 message 里的一段内容。
type ResponsesContent struct {
	Type string `json:"type"` // input_text / output_text / input_image
	Text string `json:"text,omitempty"`
}

// ResponsesResponse 是 Responses 响应体。
type ResponsesResponse struct {
	ID     string          `json:"id"`
	Object string          `json:"object"`
	Status string          `json:"status"`
	Model  string          `json:"model"`
	Output []OutputItem    `json:"output"`
	Usage  *ResponsesUsage `json:"usage,omitempty"`
	Error  any             `json:"error,omitempty"`
}

// OutputItem 是 responses output 的一个单元。
type OutputItem struct {
	Type      string             `json:"type"` // message / function_call / reasoning
	ID        string             `json:"id,omitempty"`
	Role      string             `json:"role,omitempty"`
	Content   []ResponsesContent `json:"content,omitempty"`
	Summary   []ResponsesContent `json:"summary,omitempty"` // reasoning item 的思考摘要（summary_text 段）
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Status    string             `json:"status,omitempty"`
}

// ResponsesUsage 是 Responses 用量。
type ResponsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// ResponsesStreamEvent 是 Responses 流式事件。
type ResponsesStreamEvent struct {
	Type        string             `json:"type"`
	Response    *ResponsesResponse `json:"response,omitempty"`
	Item        *OutputItem        `json:"item,omitempty"`
	Delta       string             `json:"delta,omitempty"`
	OutputIndex int                `json:"output_index,omitempty"`
	ItemID      string             `json:"item_id,omitempty"`
}

// ===== 请求：responses → openai =====

// responsesRequestToOpenAI 将 Responses 请求体转换为 OpenAI 规范请求。
func responsesRequestToOpenAI(body []byte) (*ChatCompletionRequest, error) {
	var req ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 responses 请求失败: %w", err)
	}

	out := &ChatCompletionRequest{
		Model:               req.Model,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		MaxCompletionTokens: req.MaxOutputTokens,
		Stream:              req.Stream,
	}
	if req.Instructions != "" {
		out.Messages = append(out.Messages, ChatMessage{Role: "system", Content: req.Instructions})
	}

	switch v := req.Input.(type) {
	case string:
		if v != "" {
			out.Messages = append(out.Messages, ChatMessage{Role: "user", Content: v})
		}
	case []any:
		// 先收集 function_call 的 call_id → 工具名映射，供 function_call_output 补 name 字段
		// （部分上游如 DeepSeek 要求 tool 消息必须带 name）。
		toolNameByID := map[string]string{}
		for _, item := range v {
			b, _ := json.Marshal(item)
			var it InputItem
			if json.Unmarshal(b, &it) == nil && it.Type == "function_call" {
				toolNameByID[it.CallID] = it.Name
			}
		}
		for _, item := range v {
			b, _ := json.Marshal(item)
			var it InputItem
			if json.Unmarshal(b, &it) == nil {
				out.Messages = append(out.Messages, inputItemToChat(it, toolNameByID))
			}
		}
	}

	for _, t := range req.Tools {
		out.Tools = append(out.Tools, Tool{
			Type: "function",
			Function: FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	if req.ToolChoice != nil {
		out.ToolChoice = req.ToolChoice
	}
	return out, nil
}

// inputItemToChat 将 Responses 输入项转为 OpenAI ChatMessage。
// toolNames 是 function_call 的 call_id → 工具名映射，用于给 tool 消息补 name 字段。
func inputItemToChat(it InputItem, toolNames map[string]string) ChatMessage {
	switch it.Type {
	case "function_call":
		return ChatMessage{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:       it.CallID,
				Type:     "function",
				Function: FunctionCall{Name: it.Name, Arguments: it.Arguments},
			}},
		}
	case "function_call_output":
		name := toolNames[it.CallID]
		return ChatMessage{Role: "tool", ToolCallID: it.CallID, Content: it.Output, Name: &name}
	default: // message
		role := it.Role
		if role == "developer" {
			role = "system"
		}
		return ChatMessage{Role: role, Content: responsesContentToText(it.Content)}
	}
}

// responsesContentToText 把 Responses content 压成纯文本。
func responsesContentToText(content []ResponsesContent) any {
	if len(content) == 0 {
		return ""
	}
	if len(content) == 1 && content[0].Type == "output_text" {
		return content[0].Text
	}
	var sb strings.Builder
	for _, c := range content {
		if c.Type == "output_text" || c.Type == "input_text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// ===== 请求：openai → responses =====

// responsesRequestFromOpenAI 将 OpenAI 规范请求转为 Responses 请求体。
func responsesRequestFromOpenAI(req *ChatCompletionRequest) ([]byte, error) {
	out := ResponsesRequest{
		Model:           req.Model,
		Stream:          req.Stream,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxOutputTokens: req.MaxCompletionTokens,
	}

	var input []InputItem
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			out.Instructions = chatContentToText(m.Content)
		case "tool":
			input = append(input, InputItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: chatContentToText(m.Content),
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					input = append(input, InputItem{
						Type:      "function_call",
						CallID:    tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					})
				}
			} else {
				input = append(input, InputItem{
					Type:    "message",
					Role:    "assistant",
					Content: textToResponsesContent(m.Content, "output_text"),
				})
			}
		case "user":
			input = append(input, InputItem{
				Type:    "message",
				Role:    "user",
				Content: textToResponsesContent(m.Content, "input_text"),
			})
		}
	}
	out.Input = input

	for _, t := range req.Tools {
		out.Tools = append(out.Tools, ResponsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	if req.ToolChoice != nil {
		out.ToolChoice = req.ToolChoice
	}
	return json.Marshal(out)
}

// textToResponsesContent 把 OpenAI content 转成 Responses content 数组。
func textToResponsesContent(content any, typ string) []ResponsesContent {
	text := chatContentToText(content)
	if text == "" {
		return nil
	}
	return []ResponsesContent{{Type: typ, Text: text}}
}

// ===== 响应：responses → openai =====

// responsesResponseToOpenAI 将 Responses 响应体转换为 OpenAI 规范响应。
func responsesResponseToOpenAI(body []byte) (*ChatCompletionResponse, error) {
	var resp ResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 responses 响应失败: %w", err)
	}

	out := &ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: []ChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant"}}},
	}

	var finish string
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			out.Choices[0].Message.Content = responsesContentToText(item.Content)
			if item.Status == "completed" {
				finish = "stop"
			}
		case "function_call":
			out.Choices[0].Message.ToolCalls = append(out.Choices[0].Message.ToolCalls, ToolCall{
				ID:       item.CallID,
				Type:     "function",
				Function: FunctionCall{Name: item.Name, Arguments: item.Arguments},
			})
			if finish == "" {
				finish = "tool_calls"
			}
		case "reasoning":
			// 思考摘要映射为 reasoning_content（DeepSeek 同款扩展字段），经下游转换
			// 链透出（如转 Anthropic thinking 块）。官方 item 摘要在 summary[]；个别
			// 上游放 content[]（reasoning_text 段），summary 为空时兜底取 content。
			for _, s := range item.Summary {
				if s.Text != "" {
					out.Choices[0].Message.ReasoningContent += s.Text
				}
			}
			if out.Choices[0].Message.ReasoningContent == "" {
				for _, s := range item.Content {
					if s.Text != "" {
						out.Choices[0].Message.ReasoningContent += s.Text
					}
				}
			}
		}
	}
	out.Choices[0].FinishReason = finish

	if resp.Usage != nil {
		u := usageFromResponses(*resp.Usage)
		out.Usage = &u
	}
	return out, nil
}

// ===== 响应：openai → responses =====

// responsesResponseFromOpenAI 将 OpenAI 规范响应转为 Responses 响应体。
func responsesResponseFromOpenAI(resp *ChatCompletionResponse) ([]byte, error) {
	out := ResponsesResponse{
		ID:     resp.ID,
		Object: "response",
		Model:  resp.Model,
		Status: "completed",
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		if text := chatContentToText(choice.Message.Content); text != "" {
			out.Output = append(out.Output, OutputItem{
				Type:    "message",
				Role:    "assistant",
				Content: []ResponsesContent{{Type: "output_text", Text: text}},
				Status:  "completed",
			})
		}
		for _, tc := range choice.Message.ToolCalls {
			out.Output = append(out.Output, OutputItem{
				Type:      "function_call",
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
				Status:    "completed",
			})
		}
	}
	if resp.Usage != nil {
		out.Usage = toResponsesUsage(resp.Usage)
	}
	return json.Marshal(out)
}

// ===== 流式：responses → openai =====

// responsesToOpenAIStream 把 Responses 流事件逐条映射为 OpenAI chunk。
type responsesToOpenAIStream struct {
	st      openAIStreamState
	sawTool bool // 流式过程中是否出现过 function_call，用于决定 finish_reason
}

func newResponsesToOpenAIStream() StreamConverter { return &responsesToOpenAIStream{} }

func (r *responsesToOpenAIStream) Convert(payload []byte) ([][]byte, error) {
	var ev ResponsesStreamEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("解析 responses 流事件失败: %w", err)
	}
	var out [][]byte
	switch ev.Type {
	case "response.created", "response.in_progress":
		if ev.Response != nil {
			r.st.setMeta(ev.Response.ID, ev.Response.Model)
		}
	case "response.output_text.delta":
		if b := r.st.emitRole("assistant"); b != nil {
			out = append(out, b)
		}
		r.st.text.WriteString(ev.Delta)
		out = append(out, r.st.emitChunk(ChunkDelta{Content: ev.Delta}, ""))
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		// 思考摘要 / 思考正文增量 → reasoning_content（DeepSeek 同款形状），先于正文流出
		if ev.Delta == "" {
			break
		}
		if b := r.st.emitRole("assistant"); b != nil {
			out = append(out, b)
		}
		out = append(out, r.st.emitChunk(ChunkDelta{ReasoningContent: ev.Delta}, ""))
	case "response.output_item.added":
		// Responses 的工具调用以 function_call item 形式流式给出：开场即带 call_id / name，
		// 后续参数增量走 function_call_arguments.delta。映射为 OpenAI tool_calls 开场 chunk。
		if ev.Item != nil && ev.Item.Type == "function_call" {
			r.sawTool = true
			if b := r.st.emitRole("assistant"); b != nil {
				out = append(out, b)
			}
			out = append(out, r.st.emitChunk(ChunkDelta{ToolCalls: []ChunkToolCall{{
				Index:    ev.OutputIndex,
				ID:       ev.Item.CallID,
				Type:     "function",
				Function: ChunkFunctionCall{Name: ev.Item.Name},
			}}}, ""))
		}
	case "response.function_call_arguments.delta":
		out = append(out, r.st.emitChunk(ChunkDelta{ToolCalls: []ChunkToolCall{{
			Index:    ev.OutputIndex,
			Function: ChunkFunctionCall{Arguments: ev.Delta},
		}}}, ""))
	case "response.completed":
		if ev.Response != nil {
			r.st.setMeta(ev.Response.ID, ev.Response.Model)
			if ev.Response.Usage != nil {
				u := usageFromResponses(*ev.Response.Usage)
				r.st.usage = &u
			}
			// finish_reason 取决于流式过程中是否出现过 function_call（更可靠，
			// 因为部分上游的 completed.response.output 可能为空或只带精简项）；
			// 若 output 元素里有 function_call 也一并识别。
			if r.sawTool || hasFunctionCallItem(ev.Response.Output) {
				r.st.finish = "tool_calls"
			} else {
				r.st.finish = "stop"
			}
		}
	case "response.failed":
		// 错误暂不映射为 chunk
	}
	return out, nil
}

// hasFunctionCallItem 判断 responses output 是否含 function_call 项，用于决定 finish_reason。
func hasFunctionCallItem(output []OutputItem) bool {
	for _, it := range output {
		if it.Type == "function_call" {
			return true
		}
	}
	return false
}

func (r *responsesToOpenAIStream) Finish() ([][]byte, error) {
	if b := r.st.emitFinal(); b != nil {
		return [][]byte{b}, nil
	}
	return nil, nil
}

func (r *responsesToOpenAIStream) Usage() *Usage { return r.st.usage }

// ===== 流式：openai → responses =====

// responsesBlockSink 把块生命周期回调映射为 Responses 流事件（由
// fromOpenAISkeleton 驱动）。工具调用映射为 output_item.added（item 携带
// call_id / name，output_index 沿用 openai 的 tool_calls index）+ 参数增量事件；
// 思考增量映射为 reasoning_summary_text.delta（与 responses→openai 方向的
// 解析对称）。旧实现只透文本，tool_calls / reasoning 被静默丢弃。
func newResponsesFromOpenAI() StreamConverter {
	return newFromOpenAISkeleton(&responsesBlockSink{})
}

type responsesBlockSink struct {
	id, model string
	started   bool
}

// ev 序列化一个流事件。
func (r *responsesBlockSink) ev(e ResponsesStreamEvent) []byte {
	b, _ := json.Marshal(e)
	return b
}

func (r *responsesBlockSink) BeginMessage(id, model string, _ *Usage) [][]byte {
	r.started = true
	r.id, r.model = id, model
	return [][]byte{r.ev(ResponsesStreamEvent{Type: "response.created", Response: &ResponsesResponse{
		ID: r.id, Object: "response", Status: "in_progress", Model: r.model,
	}})}
}

func (r *responsesBlockSink) TextDelta(s string) [][]byte {
	return [][]byte{r.ev(ResponsesStreamEvent{Type: "response.output_text.delta", Delta: s})}
}

func (r *responsesBlockSink) ReasoningDelta(s string) [][]byte {
	return [][]byte{r.ev(ResponsesStreamEvent{Type: "response.reasoning_summary_text.delta", Delta: s})}
}

func (r *responsesBlockSink) ToolStart(index int, id, name string) [][]byte {
	return [][]byte{r.ev(ResponsesStreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: index,
		Item:        &OutputItem{Type: "function_call", CallID: id, Name: name},
	})}
}

func (r *responsesBlockSink) ToolDelta(index int, args string) [][]byte {
	return [][]byte{r.ev(ResponsesStreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: index,
		Delta:       args,
	})}
}

// ToolComplete 发 output_item.done——Responses 客户端（codex 的 SSE 处理器）
// 只在该事件上分发工具执行，added + delta 而无 done 的工具调用永远不会被执行；
// item 携带骨架累积的完整参数与 completed 状态。
func (r *responsesBlockSink) ToolComplete(index int, id, name, args string) [][]byte {
	return [][]byte{r.ev(ResponsesStreamEvent{
		Type:        "response.output_item.done",
		OutputIndex: index,
		Item:        &OutputItem{Type: "function_call", CallID: id, Name: name, Arguments: args, Status: "completed"},
	})}
}

func (r *responsesBlockSink) TextEnd() [][]byte { return nil }

// Finish 发 response.completed（Responses 事件无 finish_reason 字段，完成即
// status=completed；usage 反向还原——input_tokens 是含缓存的总输入，需加回
// 缓存读 / 写）。与旧实现一致：未收到任何 chunk 时不产出。
func (r *responsesBlockSink) Finish(_ string, u *Usage) [][]byte {
	if !r.started {
		return nil
	}
	resp := &ResponsesResponse{
		ID:     r.id,
		Object: "response",
		Status: "completed",
		Model:  r.model,
	}
	if u != nil {
		resp.Usage = toResponsesUsage(u)
	}
	return [][]byte{r.ev(ResponsesStreamEvent{Type: "response.completed", Response: resp})}
}
