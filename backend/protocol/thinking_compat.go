package protocol

import (
	"encoding/json"
	"fmt"
)

// thinking 兼容垫片：严格 thinking 上游（DeepSeek V4 / Kimi K2 等）要求多轮对话
// 把 assistant 的 reasoning_content 原样回传，缺失即 400
// "The reasoning_content in the thinking mode must be passed back to the API"
// （带 tool_calls 的消息报 "reasoning_content is missing in assistant tool call message"）。
// 但客户端不一定带：Claude Code 的辅助请求（标题/摘要生成）与上下文压缩后的
// 历史都没有 thinking 块，网关无从回传从未收到的内容。开启垫片的渠道对这类
// 消息注入占位——宽容上游本就忽略该字段，严格上游只查存在性（2026-09-10 线上
// 排查：同请求在严格渠道 400、宽容渠道 200，见 docs 与日志 dd74c857）。
//
// 注入范围只限带 tool_calls 的 assistant 消息，理由见 InjectReasoningPlaceholders。

// ReasoningPlaceholder 是注入 assistant 历史的占位思考内容。
const ReasoningPlaceholder = "（历史思考内容未保留）"

// InjectReasoningPlaceholders 对 openai 兼容请求体做垫片：assistant 消息带
// tool_calls 而缺 reasoning_content 时注入占位。
//
// 只认带 tool_calls 的消息：严格 thinking 上游的要求本就是「每条带 tool_calls 的
// assistant 必须回传 reasoning_content」，报错原文即 "reasoning_content is missing
// in assistant tool call message"。纯文本 assistant 不添字段——宽容上游本就忽略
// 该字段，严格上游也不要求（cc-switch 的 ensure_tool_call_reasoning_content 同款
// 粒度，见 docs/research/reasoning-content-passback.md §2.1）。
//
// 粒度收窄是刻意的，不只是"更克制"：垫片每改写一条历史消息，就改变一次送往上游
// 的字节。上游自动前缀缓存按 token 前缀精确匹配，被改写的消息之后整段失效。
// 纯文本 assistant 在真实对话里远多于 tool_calls 消息，对它一并注入会让前缀漂移
// 面积成倍放大——这正是 Claude Code 侧缓存命中率低的成因之一（同类分析见
// reasoning_bridge.go）。垫片在 ComposeUpstreamRequest 末端执行（转换管线末尾），
// 真实 reasoning_content 已回填完毕，不会被占位污染。
//
// map 往返保留 canonical 之外的未知字段（与 rewriteSameProtocol 同等保真）。
//
// 第二个返回值是被改写的 assistant 消息条数（遥测，供调用日志记录「前缀漂移
// 面积」）。零改写时原样返回入参切片，连重新序列化都不做。
func InjectReasoningPlaceholders(body []byte) ([]byte, int, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, 0, fmt.Errorf("解析请求失败: %w", err)
	}
	msgs, _ := m["messages"].([]any)
	rewrites := 0
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok || msg["role"] != "assistant" {
			continue
		}
		if rc, _ := msg["reasoning_content"].(string); rc != "" {
			continue
		}
		// 只认非空 tool_calls：空数组表示没有工具调用，不属于「工具调用消息」。
		tcs, _ := msg["tool_calls"].([]any)
		if len(tcs) == 0 {
			continue
		}
		msg["reasoning_content"] = ReasoningPlaceholder
		rewrites++
	}
	if rewrites == 0 {
		return body, 0, nil
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, rewrites, err
	}
	return out, rewrites, nil
}
