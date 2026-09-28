package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

// 跨轮前缀稳定性：上游自动前缀缓存按 token 前缀精确匹配，所以「第 N+1 轮发往上游
// 的请求体以前一轮为前缀」是缓存命中的充要条件。任何在**历史消息**上做非幂等改写
// 的逻辑都会在这里暴露。本文件用两轮真实形态的 Claude Code 对话做实证。
//
// 这是对「缓存为什么没命中」的判定依据：与其推理哪段代码可疑，不如直接比字节。

// upstreamParts 取出装配后上游请求体的 messages / system / tools，供逐轮比对。
func upstreamParts(t *testing.T, body []byte) (msgs []any, system, tools any) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("解析上游请求体失败: %v\n%s", err, body)
	}
	if v, ok := m["messages"].([]any); ok {
		msgs = v
	}
	return msgs, m["system"], m["tools"]
}

// firstAssistant 返回 messages 里第一条 assistant 消息（system 被拼进 messages，
// 下标不固定，所以按 role 找而不是硬编码位置）。
func firstAssistant(t *testing.T, msgs []any) map[string]any {
	t.Helper()
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "assistant" {
			return mm
		}
	}
	t.Fatalf("messages 里没有 assistant: %v", msgs)
	return nil
}

// assertPrefixStable 断言 turn2 的 system / tools 与 turn1 相同，且 turn2 的前
// len(turn1.messages) 条消息与 turn1 逐字节等价。
func assertPrefixStable(t *testing.T, up1, up2 []byte) {
	t.Helper()
	m1, sys1, tools1 := upstreamParts(t, up1)
	m2, sys2, tools2 := upstreamParts(t, up2)

	if !reflect.DeepEqual(sys1, sys2) {
		t.Errorf("system 段跨轮不一致（前缀缓存从此处起整段失效）:\n turn1=%v\n turn2=%v", sys1, sys2)
	}
	if !reflect.DeepEqual(tools1, tools2) {
		t.Errorf("tools 段跨轮不一致（前缀缓存从此处起整段失效）:\n turn1=%v\n turn2=%v", tools1, tools2)
	}
	if len(m2) <= len(m1) {
		t.Fatalf("第二轮 messages 应更长: turn1=%d turn2=%d", len(m1), len(m2))
	}
	for i := range m1 {
		if !reflect.DeepEqual(m1[i], m2[i]) {
			b1, _ := json.Marshal(m1[i])
			b2, _ := json.Marshal(m2[i])
			t.Errorf("第 %d 条历史消息跨轮被改写（前缀从此处起失效）:\n turn1=%s\n turn2=%s", i, b1, b2)
		}
	}
}

const probeSystem = `[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."}]`
const probeTools = `[{"name":"Bash","description":"Run a shell command","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]`

// TestPrefixStableClientEchoesThinkingVerbatim 基线：客户端原样回传 thinking 明文
// 与签名。跨轮必须完全稳定。
func TestPrefixStableClientEchoesThinkingVerbatim(t *testing.T) {
	const thinking = "我需要调用 Bash 来列出目录"
	sig := EncodeReasoningSignature(thinking)

	turn1 := []byte(`{"model":"m","max_tokens":8192,"system":` + probeSystem + `,"tools":` + probeTools + `,"messages":[
		{"role":"user","content":[{"type":"text","text":"列出当前目录"}]}
	]}`)
	turn2 := []byte(`{"model":"m","max_tokens":8192,"system":` + probeSystem + `,"tools":` + probeTools + `,"messages":[
		{"role":"user","content":[{"type":"text","text":"列出当前目录"}]},
		{"role":"assistant","content":[
			{"type":"thinking","thinking":"` + thinking + `","signature":"` + sig + `"},
			{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}
		]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a.txt\nb.txt"}]}
	]}`)

	up := func(b []byte) []byte {
		out, _, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, b, UpstreamRequest{Model: "deepseek-v4-pro", ThinkingCompat: true})
		if err != nil {
			t.Fatalf("装配失败: %v", err)
		}
		return out
	}
	assertPrefixStable(t, up(turn1), up(turn2))
}

// TestPrefixStableShimInjectionIsDeterministic 检验一个关键判断：垫片注入会不会
// 造成跨轮前缀漂移？
//
// 结论是不会。注入是历史消息的**确定性函数**：同一条历史在每一轮都渲染出同一个
// 占位，所以第 N+1 轮的请求体仍以前一轮为前缀。这也意味着「收窄垫片范围」对缓存
// 命中**没有收益**——它的收益是保真度（少偏离客户端原始输入），不是缓存。
// 真正造成漂移的是客户端自己改写了历史消息，那是 ① 负责的场景。
func TestPrefixStableShimInjectionIsDeterministic(t *testing.T) {
	// 助手消息带 tool_use 但完全没有思考块 → 垫片注入占位
	assistantNoThinking := `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}`

	turn2 := []byte(`{"model":"m","max_tokens":8192,"system":` + probeSystem + `,"tools":` + probeTools + `,"messages":[
		{"role":"user","content":[{"type":"text","text":"列出当前目录"}]},
		` + assistantNoThinking + `,
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a.txt"}]}
	]}`)
	turn3 := []byte(`{"model":"m","max_tokens":8192,"system":` + probeSystem + `,"tools":` + probeTools + `,"messages":[
		{"role":"user","content":[{"type":"text","text":"列出当前目录"}]},
		` + assistantNoThinking + `,
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a.txt"}]},
		{"role":"assistant","content":[{"type":"text","text":"目录里有两个文件"}]},
		{"role":"user","content":[{"type":"text","text":"好"}]}
	]}`)

	up := func(b []byte) []byte {
		out, stats, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, b, UpstreamRequest{Model: "deepseek-v4-pro", ThinkingCompat: true})
		if err != nil {
			t.Fatalf("装配失败: %v", err)
		}
		if stats.ShimRewrites != 1 {
			t.Fatalf("前置条件不成立：应恰好垫片改写 1 条，实际 %d", stats.ShimRewrites)
		}
		return out
	}

	// 垫片确实注入了占位（否则这条测试没测到东西）
	m2, _, _ := upstreamParts(t, up(turn2))
	if got, _ := firstAssistant(t, m2)["reasoning_content"].(string); got != ReasoningPlaceholder {
		t.Fatalf("垫片应注入占位，实际 %q", got)
	}
	// 但跨轮前缀依然稳定 —— 收窄垫片对缓存无收益，实证在此
	assertPrefixStable(t, up(turn2), up(turn3))
}

// TestPrefixStableClientManglesThinkingPlaintext 客户端改写了 thinking 明文
// （脱敏/截断/规范化），只保留签名——且**两轮改写得还不一样**。这是 ① 要解决的
// 场景：若网关读明文，同一段历史在两轮里渲染出的 reasoning_content 就不同，
// 前缀从该处起失效；桥载荷保证网关读签名，两轮必须完全一致。
func TestPrefixStableClientManglesThinkingPlaintext(t *testing.T) {
	const thinking = "我需要调用 Bash 来列出目录"
	sig := EncodeReasoningSignature(thinking)

	body := func(mangled string) []byte {
		return []byte(`{"model":"m","max_tokens":8192,"system":` + probeSystem + `,"tools":` + probeTools + `,"messages":[
			{"role":"user","content":[{"type":"text","text":"列出当前目录"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"` + mangled + `","signature":"` + sig + `"},
				{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}
			]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a.txt"}]}
		]}`)
	}

	up := func(b []byte) []byte {
		out, _, err := ComposeUpstreamRequest(ProviderAnthropic, ProviderOpenAI, b, UpstreamRequest{Model: "deepseek-v4-pro", ThinkingCompat: true})
		if err != nil {
			t.Fatalf("装配失败: %v", err)
		}
		return out
	}

	upA := up(body("（第一轮里被改写成的样子）"))
	upB := up(body("（第二轮改写成了完全不同的内容，而且明显更长一些）"))

	mA, _, _ := upstreamParts(t, upA)
	mB, _, _ := upstreamParts(t, upB)
	rcA, _ := firstAssistant(t, mA)["reasoning_content"].(string)
	rcB, _ := firstAssistant(t, mB)["reasoning_content"].(string)

	if rcA != thinking || rcB != thinking {
		t.Errorf("桥载荷应压过被改写的明文:\n A=%q\n B=%q\nwant %q", rcA, rcB, thinking)
	}
	if rcA != rcB {
		t.Errorf("同一签名在不同明文改写下产出不同的 reasoning_content → 前缀漂移:\n A=%q\n B=%q", rcA, rcB)
	}
	// 幂等：同一输入两次装配必须逐字节一致
	if a, b := string(up(body("x"))), string(up(body("x"))); a != b {
		t.Errorf("同一输入两次装配结果不一致（非幂等 → 前缀必漂移）")
	}
}
