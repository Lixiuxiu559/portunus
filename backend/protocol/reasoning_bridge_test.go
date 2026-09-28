package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// 本文件锁定「思考内容往返桥」（reasoning_bridge.go）的行为契约。
// 最重要的一条是 TestReasoningBridgeUpstreamPrefixStableAcrossTurns：
// 它把「缓存能否命中」翻译成可断言的不变量——客户端回传轮送往上游的
// reasoning_content 必须与首轮逐字节一致，否则自动前缀缓存从改写点起整段失效。

func TestReasoningBridgeEncodeDecodeRoundTrip(t *testing.T) {
	cases := []string{
		"思考内容",
		"multi\nline\twith \"quotes\" and \\backslash",
		"emoji 🚀 与 中文 混排",
		strings.Repeat("x", 5000),
	}
	for _, in := range cases {
		sig := EncodeReasoningSignature(in)
		if sig == "" {
			t.Fatalf("非空内容应产出签名: %q", in)
		}
		if !strings.HasPrefix(sig, ReasoningSignaturePrefix) {
			t.Errorf("签名应带版本前缀: %q", sig)
		}
		got, ok := DecodeReasoningSignature(sig)
		if !ok {
			t.Fatalf("桥签名应能解码: %q", sig)
		}
		if got != in {
			t.Errorf("往返不一致:\n got %q\nwant %q", got, in)
		}
		// 确定性：同一内容两次编码必须完全一致。若编码不确定，跨轮前缀必然漂移。
		if again := EncodeReasoningSignature(in); again != sig {
			t.Errorf("编码不确定，会导致前缀漂移:\n %q\n %q", again, sig)
		}
	}
	if sig := EncodeReasoningSignature(""); sig != "" {
		t.Errorf("空内容不应产出签名，实际 %q", sig)
	}
}

func TestReasoningBridgeDecodeRejectsForeignSignature(t *testing.T) {
	for _, sig := range []string{
		"",
		"sig", // 改动前的常量占位
		"ErUBCkYIBRgCIkAqL7Zf5c1kQm3n8pVdWxYz0123456789abcdef", // 上游原生签名形态
		ReasoningSignaturePrefix + "!!!not-base64!!!",
		ReasoningSignaturePrefix + "====",
	} {
		if got, ok := DecodeReasoningSignature(sig); ok {
			t.Errorf("非桥签名应解码失败，实际 %q -> %q", sig, got)
		}
	}
}

// TestConvertResponseOpenAIToAnthropicCarriesBridgeSignature 断言响应方向写出的
// thinking 块签名是**可解码的桥载荷**（而非旧常量 "sig"），且能还原原始思考内容。
func TestConvertResponseOpenAIToAnthropicCarriesBridgeSignature(t *testing.T) {
	const reasoning = "上游返回的原始思考内容"
	in := `{
		"id":"c1","object":"chat.completion","model":"deepseek-v4-pro",
		"choices":[{"index":0,"message":{"role":"assistant","content":"答案","reasoning_content":"` + reasoning + `"},"finish_reason":"stop"}]
	}`
	out, err := ConvertResponse(ProviderOpenAI, ProviderAnthropic, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	var sig string
	for _, b := range sliceAt(t, unmarshalAny(t, out), "content") {
		blk, ok := b.(map[string]any)
		if ok && blk["type"] == "thinking" {
			sig, _ = blk["signature"].(string)
		}
	}
	if sig == "" {
		t.Fatalf("thinking 块应带签名: %s", out)
	}
	got, ok := DecodeReasoningSignature(sig)
	if !ok {
		t.Fatalf("签名应是可解码的桥载荷，实际 %q", sig)
	}
	if got != reasoning {
		t.Errorf("桥载荷应还原原始思考内容:\n got %q\nwant %q", got, reasoning)
	}
}

// TestReasoningBridgeUpstreamPrefixStableAcrossTurns 是缓存命中的充要条件：
// 首轮上游返回 reasoning_content → 网关渲染 thinking 块给客户端 → 客户端下一轮
// 回传（明文被脱敏/压缩/规范化改写，签名原样保留）→ 网关转回上游时，
// reasoning_content 必须逐字节等于首轮原文。
//
// 改动前签名是常量 "sig"、不含信息，回传轮的 reasoning_content 只能取客户端
// 改写后的明文（或退回占位），同一段历史逐轮产出不同字节 —— 上游自动前缀缓存
// 从改写点起整段失效。本测试即锁定该回归。
func TestReasoningBridgeUpstreamPrefixStableAcrossTurns(t *testing.T) {
	const reasoning = "第一轮的真实思考内容 reasoning_content"

	// ── 首轮：上游（DeepSeek）返回 reasoning_content，网关渲染给 Anthropic 客户端 ──
	upResp := `{
		"id":"c1","object":"chat.completion","model":"deepseek-v4-pro",
		"choices":[{"index":0,"message":{"role":"assistant","content":"答案","reasoning_content":"` + reasoning + `"},"finish_reason":"stop"}]
	}`
	clientResp, err := ConvertResponse(ProviderOpenAI, ProviderAnthropic, []byte(upResp))
	if err != nil {
		t.Fatalf("响应转换失败: %v", err)
	}
	var sig string
	for _, b := range sliceAt(t, unmarshalAny(t, clientResp), "content") {
		blk, ok := b.(map[string]any)
		if ok && blk["type"] == "thinking" {
			sig, _ = blk["signature"].(string)
		}
	}
	if sig == "" {
		t.Fatalf("首轮响应应带 thinking 块与签名: %s", clientResp)
	}

	// ── 回传轮：客户端保留签名，但把 thinking 明文改写成了别的内容 ──
	echoed := `{
		"model":"deepseek-v4-pro","max_tokens":100,
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"（客户端改写/脱敏后的占位）","signature":"` + sig + `"},
				{"type":"text","text":"答案"}
			]},
			{"role":"user","content":[{"type":"text","text":"继续"}]}
		]
	}`
	upReq, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(echoed))
	if err != nil {
		t.Fatalf("请求转换失败: %v", err)
	}
	assistant, ok := sliceAt(t, unmarshalAny(t, upReq), "messages")[1].(map[string]any)
	if !ok {
		t.Fatalf("第二条消息应为 assistant: %s", upReq)
	}
	got, _ := assistant["reasoning_content"].(string)
	if got != reasoning {
		t.Errorf("回传轮的 reasoning_content 必须逐字节等于首轮原文，否则上游前缀缓存失效:\n got %q\nwant %q", got, reasoning)
	}
}

// TestConvertRequestAnthropicBridgeSignaturePreferred 断言明文与签名不一致时，
// 以签名（权威原文）为准——这正是客户端改写明文后仍能保真的原因。
func TestConvertRequestAnthropicBridgeSignaturePreferred(t *testing.T) {
	const original = "原始思考内容，逐字节必须还原"
	sig := EncodeReasoningSignature(original)
	in := `{
		"model":"deepseek-chat","max_tokens":100,
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"被截断的明文","signature":"` + sig + `"},
				{"type":"text","text":"答案"}
			]}
		]
	}`
	out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	assistant := sliceAt(t, unmarshalAny(t, out), "messages")[1].(map[string]any)
	if got, _ := assistant["reasoning_content"].(string); got != original {
		t.Errorf("应优先采用签名里的桥载荷:\n got %q\nwant %q", got, original)
	}
}

// TestConvertRequestAnthropicLegacySignatureFallsBack 断言非桥签名（旧常量 "sig"、
// 上游原生签名）仍退回读 thinking 明文——向后兼容，行为与改动前一致。
func TestConvertRequestAnthropicLegacySignatureFallsBack(t *testing.T) {
	for _, sig := range []string{"sig", "ErUBCkYIBRgCIkAqL7Zf5c1kQm3n8pVdWx"} {
		in := `{
			"model":"deepseek-chat","max_tokens":100,
			"messages":[
				{"role":"user","content":[{"type":"text","text":"hi"}]},
				{"role":"assistant","content":[
					{"type":"thinking","thinking":"明文思考","signature":"` + sig + `"},
					{"type":"text","text":"答案"}
				]}
			]
		}`
		out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
		if err != nil {
			t.Fatalf("转换失败: %v", err)
		}
		assistant := sliceAt(t, unmarshalAny(t, out), "messages")[1].(map[string]any)
		if got, _ := assistant["reasoning_content"].(string); got != "明文思考" {
			t.Errorf("非桥签名(%q)应退回明文:\n got %q\nwant %q", sig, got, "明文思考")
		}
	}
}

// TestConvertRequestAnthropicRedactedThinkingBridgeData 断言 redacted_thinking 的
// data 恰为桥载荷时解码还原（无损）；非桥密文仍退回占位。
func TestConvertRequestAnthropicRedactedThinkingBridgeData(t *testing.T) {
	const original = "被脱敏前的原始思考内容"
	bridged := EncodeReasoningSignature(original)

	for _, tc := range []struct {
		name    string
		data    string
		want    string
		exactly bool
	}{
		{name: "桥载荷", data: bridged, want: original, exactly: true},
		{name: "上游密文", data: "encrypted-blob", want: ReasoningPlaceholder, exactly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := `{
				"model":"deepseek-chat","max_tokens":100,
				"messages":[
					{"role":"user","content":[{"type":"text","text":"hi"}]},
					{"role":"assistant","content":[
						{"type":"redacted_thinking","data":"` + tc.data + `"},
						{"type":"text","text":"答案"}
					]}
				]
			}`
			out, err := ConvertRequest(ProviderAnthropic, ProviderOpenAI, []byte(in))
			if err != nil {
				t.Fatalf("转换失败: %v", err)
			}
			assistant := sliceAt(t, unmarshalAny(t, out), "messages")[1].(map[string]any)
			got, _ := assistant["reasoning_content"].(string)
			if tc.exactly && got != tc.want {
				t.Errorf("reasoning_content 不符:\n got %q\nwant %q", got, tc.want)
			}
			if !tc.exactly && !strings.Contains(got, tc.want) {
				t.Errorf("reasoning_content 应含 %q，实际 %q", tc.want, got)
			}
		})
	}
}

// TestConvertRequestAnthropicRedactedThinkingDataPreserved 断言 redacted_thinking
// 的密文经结构体往返后不丢（此前 ContentBlock 缺 data 字段，密文被静默吞掉）。
func TestConvertRequestAnthropicRedactedThinkingDataPreserved(t *testing.T) {
	var blk ContentBlock
	if err := json.Unmarshal([]byte(`{"type":"redacted_thinking","data":"opaque-blob"}`), &blk); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if blk.Data != "opaque-blob" {
		t.Errorf("data 字段应被保留，实际 %q", blk.Data)
	}
	out, err := json.Marshal(blk)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(out), "opaque-blob") {
		t.Errorf("序列化应带 data，实际 %s", out)
	}
}

// TestStreamAnthropicBridgeSignatureCarriesReasoning 断言流式方向 signature_delta
// 携带的是整块思考内容的桥载荷（需要跨多个 thinking_delta 累积），可解码还原。
func TestStreamAnthropicBridgeSignatureCarriesReasoning(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var events []MessagesStreamEvent
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		for _, o := range outs {
			events = append(events, parseAnthropicEvent(t, o))
		}
	}
	feed(`{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"reasoning_content":"思考"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"reasoning_content":"内容"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"content":"答案"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	for _, o := range finals {
		events = append(events, parseAnthropicEvent(t, o))
	}

	var sigs []string
	for _, ev := range events {
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "signature_delta" {
			sigs = append(sigs, ev.Delta.Signature)
		}
	}
	if len(sigs) != 1 {
		t.Fatalf("应恰好补发一个 signature_delta，实际 %d 个", len(sigs))
	}
	got, ok := DecodeReasoningSignature(sigs[0])
	if !ok {
		t.Fatalf("signature_delta 应是可解码的桥载荷，实际 %q", sigs[0])
	}
	if got != "思考内容" {
		t.Errorf("桥载荷应累积整块思考内容:\n got %q\nwant %q", got, "思考内容")
	}
}

// TestStreamAnthropicBridgeSignatureResetsBetweenBlocks 断言 thinking → text →
// thinking 的第二块签名只含第二块内容（累积器在开新块时重置）。
func TestStreamAnthropicBridgeSignatureResetsBetweenBlocks(t *testing.T) {
	conv, err := NewStreamConverter(ProviderOpenAI, ProviderAnthropic)
	if err != nil {
		t.Fatalf("构建转换器失败: %v", err)
	}

	var sigs []string
	collect := func(outs [][]byte) {
		for _, o := range outs {
			ev := parseAnthropicEvent(t, o)
			if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "signature_delta" {
				sigs = append(sigs, ev.Delta.Signature)
			}
		}
	}
	feed := func(payload string) {
		outs, err := conv.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("Convert 失败: %v", err)
		}
		collect(outs)
	}
	feed(`{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"reasoning_content":"第一块"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{"content":"正文"},"finish_reason":null}]}`) // 切换 → 关闭第一块
	feed(`{"choices":[{"index":0,"delta":{"reasoning_content":"第二块"},"finish_reason":null}]}`)
	feed(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	finals, err := conv.Finish()
	if err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	collect(finals) // 第二块在收尾时才关闭，签名在这里发出

	if len(sigs) != 2 {
		t.Fatalf("两个 thinking 块应各补一个签名，实际 %d 个", len(sigs))
	}
	if got, _ := DecodeReasoningSignature(sigs[0]); got != "第一块" {
		t.Errorf("第一块签名应为 %q，实际 %q", "第一块", got)
	}
	if got, _ := DecodeReasoningSignature(sigs[1]); got != "第二块" {
		t.Errorf("第二块签名应为 %q（累积器未重置会串入第一块），实际 %q", "第二块", got)
	}
}
