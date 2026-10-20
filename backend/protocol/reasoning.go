package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ReasoningConversionError 表示显式思考参数无法跨协议表达；不以猜测档位或预算兜底。
// 同协议不经过此校验，原生参数由上游解释。
type ReasoningConversionError struct{ Field, Reason string }

func (e *ReasoningConversionError) Error() string {
	return fmt.Sprintf("无法转换思考参数 %s: %s", e.Field, e.Reason)
}

// reasoningIntent 是跨协议内部元数据，不序列化到 Chat 请求中。
// nil 与关闭、预算 0 分别保留；effort 不按模型名改写或钳制。
type reasoningIntent struct {
	effort *string
	mode   string
	budget *int
}

func reasoningError(field, reason string) error {
	return &ReasoningConversionError{Field: field, Reason: reason}
}

func parseReasoning(p Provider, body []byte) (*reasoningIntent, error) {
	var fields struct {
		Effort    *string `json:"reasoning_effort"`
		Reasoning *struct {
			Effort *string `json:"effort"`
		} `json:"reasoning"`
		Output *struct {
			Effort *string `json:"effort"`
		} `json:"output_config"`
		Thinking *struct {
			Type   string `json:"type"`
			Budget *int   `json:"budget_tokens"`
		} `json:"thinking"`
		Generation *struct {
			Thinking *struct {
				Level  *string `json:"thinkingLevel"`
				Budget *int    `json:"thinkingBudget"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
	}
	// 只解析源协议的参数，避免无关的扩展字段影响正常转换。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	selected := map[string]json.RawMessage{}
	var keys []string
	switch p {
	case ProviderOpenAI:
		keys = []string{"reasoning_effort", "thinking"}
	case ProviderOpenAIResponses:
		keys = []string{"reasoning"}
	case ProviderAnthropic:
		keys = []string{"output_config", "thinking"}
	case ProviderGemini:
		keys = []string{"generationConfig"}
	}
	for _, k := range keys {
		if v, ok := raw[k]; ok {
			selected[k] = v
		}
	}
	data, _ := json.Marshal(selected)
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, reasoningError(string(p), "参数类型不合法: "+err.Error())
	}
	r := &reasoningIntent{}
	switch p {
	case ProviderOpenAI:
		r.effort = fields.Effort
	case ProviderOpenAIResponses:
		if fields.Reasoning != nil {
			r.effort = fields.Reasoning.Effort
		}
	case ProviderAnthropic:
		if fields.Output != nil {
			r.effort = fields.Output.Effort
		}
	case ProviderGemini:
		if fields.Generation != nil && fields.Generation.Thinking != nil {
			th := fields.Generation.Thinking
			r.effort = th.Level
			r.budget = th.Budget
			if r.effort != nil {
				level := strings.ToLower(*r.effort)
				r.effort = &level
			}
			if r.budget != nil && *r.budget == 0 {
				r.mode = "disabled"
				r.budget = nil
			}
		}
	}
	if (p == ProviderOpenAI || p == ProviderAnthropic) && fields.Thinking != nil {
		r.mode = fields.Thinking.Type
		r.budget = fields.Thinking.Budget
	}
	if r.effort != nil && *r.effort == "" {
		return nil, reasoningError("effort", "强度不能为空")
	}
	if r.effort == nil && r.mode == "" && r.budget == nil {
		return nil, nil
	}
	return r, nil
}

// renderReasoning 只改目标协议的对应字段；原始档位保留，能力与合法档位由上游裁决。
// legacy 仅表示请求明确使用了既有 Chat thinking 扩展（如 -thinking），不是模型能力推测。
func renderReasoning(p Provider, body []byte, r *reasoningIntent, legacy *ThinkingConfig) ([]byte, error) {
	if r == nil {
		return body, nil
	}
	effort := r.effort
	mode := r.mode
	// Chat/Responses none 的关闭语义可用目标协议的原生关闭字段表达。
	if (p == ProviderAnthropic || p == ProviderGemini) && effort != nil && *effort == "none" {
		if mode != "" && mode != "disabled" {
			return nil, reasoningError("effort/thinking", "关闭与开启设置冲突")
		}
		mode = "disabled"
		effort = nil
	}
	if mode != "" && mode != "enabled" && mode != "disabled" && mode != "adaptive" {
		return nil, reasoningError("thinking.type", "目标协议无法表达模式 "+mode)
	}
	var m map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&m); err != nil {
		return nil, err
	}
	switch p {
	case ProviderOpenAI, ProviderOpenAIResponses:
		if p == ProviderOpenAI && legacy != nil {
			// 显式原生模式优先于兼容后缀；不将 adaptive 原样塞给 Chat。
			if mode == "disabled" {
				m["thinking"] = map[string]any{"type": "disabled"}
			}
			if r.budget != nil {
				th := map[string]any{"type": legacy.Type, "budget_tokens": *r.budget}
				if mode == "disabled" {
					th["type"] = "disabled"
				}
				m["thinking"] = th
			}
		} else {
			if r.budget != nil {
				return nil, reasoningError("thinking budget", "目标协议没有对应的 token 预算字段")
			}
			if mode == "disabled" {
				if effort != nil && *effort != "none" {
					return nil, reasoningError("thinking/effort", "目标协议不能同时表达关闭思考与非 none 强度")
				}
				disabled := "none"
				effort = &disabled
			} else if mode != "" && effort == nil {
				return nil, reasoningError("thinking.type", "目标协议需要明确 effort，不能从模式猜测强度")
			}
		}
		if effort != nil {
			if p == ProviderOpenAI {
				m["reasoning_effort"] = *effort
			} else {
				m["reasoning"] = map[string]any{"effort": *effort}
			}
		}
	case ProviderAnthropic:
		if r.budget != nil && *r.budget < 0 {
			return nil, reasoningError("thinking budget", "目标协议不能表达动态或负数预算")
		}
		if effort != nil {
			m["output_config"] = map[string]any{"effort": *effort}
		}
		if mode != "" || r.budget != nil {
			if mode == "" {
				mode = "enabled"
			}
			th := map[string]any{"type": mode}
			if r.budget != nil {
				th["budget_tokens"] = *r.budget
			}
			m["thinking"] = th
		}
	case ProviderGemini:
		if effort != nil && (r.budget != nil || mode == "disabled") {
			return nil, reasoningError("thinking/effort", "不能同时生成 thinkingLevel 与 thinkingBudget")
		}
		th := map[string]any{}
		if effort != nil {
			th["thinkingLevel"] = strings.ToUpper(*effort)
		} else if mode == "disabled" {
			th["thinkingBudget"] = 0
		} else if r.budget != nil {
			th["thinkingBudget"] = *r.budget
		} else {
			return nil, reasoningError("thinking.type", "目标协议需要明确档位或预算，不能从模式推测")
		}
		cfg, ok := m["generationConfig"].(map[string]any)
		if !ok {
			cfg = map[string]any{}
			m["generationConfig"] = cfg
		}
		cfg["thinkingConfig"] = th
	}
	return json.Marshal(m)
}

// hasExplicitThinking 不让旧后缀覆盖原生 thinking 或显式 none。
func hasExplicitThinking(body map[string]any) bool {
	if raw, ok := body["thinking"]; ok && raw != nil {
		return true
	}
	effort, _ := body["reasoning_effort"].(string)
	return effort == "none"
}
