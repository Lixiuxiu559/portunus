package protocol

import (
	"encoding/json"
	"fmt"
)

// thinking 兼容垫片：严格 thinking 上游（DeepSeek V4 / Kimi K2 等）要求多轮对话
// 把 assistant 的 reasoning_content 原样回传，缺失即 400
// "The reasoning_content in the thinking mode must be passed back to the API"。
// 但客户端不一定带：Claude Code 的辅助请求（标题/摘要生成）与上下文压缩后的
// 历史都没有 thinking 块，网关无从回传从未收到的内容。开启垫片的渠道对这类
// 消息注入占位——宽容上游本就忽略该字段，严格上游只查存在性（2026-09-10 线上
// 排查：同请求在严格渠道 400、宽容渠道 200，见 docs 与日志 dd74c857）。

// ReasoningPlaceholder 是注入 assistant 历史的占位思考内容。
const ReasoningPlaceholder = "（历史思考内容未保留）"

// InjectReasoningPlaceholders 对 openai 兼容请求体做垫片：assistant 消息缺
// reasoning_content 且有实际内容（content 非空或带 tool_calls）时注入占位。
// map 往返保留 canonical 之外的未知字段（与 rewriteSameProtocol 同等保真）。
func InjectReasoningPlaceholders(body []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("解析请求失败: %w", err)
	}
	msgs, _ := m["messages"].([]any)
	changed := false
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok || msg["role"] != "assistant" {
			continue
		}
		if rc, _ := msg["reasoning_content"].(string); rc != "" {
			continue
		}
		_, hasToolCalls := msg["tool_calls"].([]any)
		if !nonEmptyContent(msg["content"]) && !hasToolCalls {
			continue
		}
		msg["reasoning_content"] = ReasoningPlaceholder
		changed = true
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(m)
}

// nonEmptyContent 判断消息 content 是否有实际内容（字符串非空 / 块数组非空）。
func nonEmptyContent(v any) bool {
	switch c := v.(type) {
	case string:
		return c != ""
	case []any:
		return len(c) > 0
	}
	return false
}
