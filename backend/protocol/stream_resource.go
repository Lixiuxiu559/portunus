package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/Lixiuxiu559/portunus/backend/resource"
)

// Each stateful conversion stage owns a budget. Accounting precedes retained
// writes; a failed budget remains failed, including when Finish is called later.
type streamResourceBudget struct {
	limits resource.Limits
	total  int64
	items  int
	args   map[string]int64
	err    error
}

func (b *streamResourceBudget) normalized() resource.Limits { return b.limits.Normalize() }
func (b *streamResourceBudget) fail(kind string, limit int64) error {
	if b.err == nil {
		b.err = &resource.LimitError{Scope: resource.Stream, Kind: kind, Limit: limit}
	}
	return b.err
}
func (b *streamResourceBudget) add(n int) error {
	if b.err != nil {
		return b.err
	}
	l := b.normalized()
	if int64(n) > l.MaxAccumulatedBytes-b.total {
		return b.fail("accumulated_bytes", l.MaxAccumulatedBytes)
	}
	b.total += int64(n)
	return nil
}
func (b *streamResourceBudget) item() error {
	if b.err != nil {
		return b.err
	}
	l := b.normalized()
	if b.items >= l.MaxNativeFragments {
		return b.fail("stream_items", int64(l.MaxNativeFragments))
	}
	b.items++
	return nil
}
func (b *streamResourceBudget) tool(key, args string, reset bool) error {
	if b.err != nil {
		return b.err
	}
	l := b.normalized()
	if b.args == nil {
		b.args = map[string]int64{}
	}
	current, exists := b.args[key]
	if !exists {
		if err := b.item(); err != nil {
			return err
		}
	}
	if reset {
		current = 0
	}
	if int64(len(args)) > l.MaxToolArgumentsBytes-current {
		return b.fail("tool_arguments_bytes", l.MaxToolArgumentsBytes)
	}
	if err := b.add(len(args)); err != nil {
		return err
	}
	b.args[key] = current + int64(len(args))
	return nil
}
func (b *streamResourceBudget) payload(p Provider, payload []byte, retainedText bool) error {
	if b.err != nil {
		return b.err
	}
	if len(payload) > 1<<20 {
		return b.fail("sse_event_bytes", 1<<20)
	}
	if err := resource.CheckDepth(payload, b.normalized().MaxJSONDepth, resource.Stream); err != nil {
		b.err = err
		return err
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(payload, &obj) != nil {
		return nil
	} // existing decoder owns syntax errors
	addText := func(s string) error {
		if retainedText {
			return b.add(len(s))
		}
		return nil
	}
	switch p {
	case ProviderOpenAI:
		var chunk ChatCompletionChunk
		if json.Unmarshal(payload, &chunk) != nil {
			return nil
		}
		for _, c := range chunk.Choices {
			d := c.Delta
			if err := addText(d.Content); err != nil {
				return err
			}
			if err := addText(d.ReasoningContent); err != nil {
				return err
			}
			for _, t := range d.ToolCalls {
				key := fmt.Sprintf("%d/%d", c.Index, t.Index)
				if err := b.tool(key, t.Function.Arguments, false); err != nil {
					return err
				}
				if err := b.add(len(t.ID) + len(t.Function.Name)); err != nil {
					return err
				}
			}
		}
	case ProviderAnthropic:
		var ev MessagesStreamEvent
		if json.Unmarshal(payload, &ev) != nil {
			return nil
		}
		key := fmt.Sprint(indexOrZero(ev.Index))
		if ev.Type == "content_block_start" {
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				delete(b.args, key)
				args := rawMap(obj["content_block"])["input"]
				if string(args) == "{}" || string(args) == "null" {
					args = nil
				}
				if err := b.tool(key, string(args), true); err != nil {
					return err
				}
			} else if err := b.item(); err != nil {
				return err
			}
		}
		if ev.Type == "content_block_delta" && ev.Delta != nil {
			if err := addText(ev.Delta.Text); err != nil {
				return err
			}
			if err := addText(ev.Delta.Thinking); err != nil {
				return err
			}
			if ev.Delta.Type == "input_json_delta" {
				return b.tool(key, ev.Delta.PartialJSON, false)
			}
		}
	case ProviderOpenAIResponses:
		var ev ResponsesStreamEvent
		if json.Unmarshal(payload, &ev) != nil {
			return nil
		}
		key := fmt.Sprint(ev.OutputIndex)
		switch ev.Type {
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				delete(b.args, key)
				return b.tool(key, ev.Item.Arguments, true)
			}
			return b.item()
		case "response.content_part.added", "response.reasoning_summary_part.added":
			return b.item()
		case "response.function_call_arguments.delta":
			return b.tool(key, ev.Delta, false)
		case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			return addText(ev.Delta)
		}
	case ProviderGemini:
		var ev GenerateContentResponse
		if json.Unmarshal(payload, &ev) != nil {
			return nil
		}
		for ci, c := range ev.Candidates {
			if c.Content == nil {
				continue
			}
			for pi, p := range c.Content.Parts {
				if err := addText(p.Text); err != nil {
					return err
				}
				if p.FunctionCall != nil {
					args, _ := json.Marshal(p.FunctionCall.Args)
					key := fmt.Sprintf("%d/%d", ci, pi)
					if err := b.item(); err != nil {
						return err
					}
					if err := b.tool(key, string(args), true); err != nil {
						return err
					}
				}
			}
		}
	}
	return b.err
}

type streamResourceSetter interface{ setResourceLimits(resource.Limits) }

func setStreamLimits(v any, l resource.Limits) {
	if s, ok := v.(streamResourceSetter); ok {
		s.setResourceLimits(l.Normalize())
	}
}
func converterLimits(c StreamConverter) resource.Limits {
	if v, ok := c.(interface{ resourceLimits() resource.Limits }); ok {
		return v.resourceLimits()
	}
	return resource.Defaults()
}
