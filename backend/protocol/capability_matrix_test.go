package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// 本文件是「协议转换能力覆盖矩阵（capability matrix）」测试：把「哪条转换路径
// 保留思考内容」从散在六个转换器里的隐式知识，收敛成一张带出处的显式登记表 +
// 机械验证。设计见 docs/research/2026-09-29-capability-matrix-design.md。
//
// 与既有测试的分工：矩阵测「在不在」，reasoning_align_test.go / convert_test.go
// 测「形状对不对」——互补不替代。矩阵 runner 只用三个公开入口
// （ConvertRequest / ConvertResponse / NewStreamConverter），与现有测试的黑盒
// 驱动方式一致；唯一白盒是遍历 providerImpls 的穷尽检查（与 usage 的
// newUsageExtractor 同属「漏了就炸」，但更早——CI 而非线上，见设计文档 §1.3）。

// reasoningProbe 是所有协议探针 fixture 统一使用的思考文本。探测器按「存在且
// 等于探针」判定，与正文文本区分（正文统一用「答」，不与之冲突）。
const reasoningProbe = "思考探针"

// capability 是一个「协议 × 位置」格子对思考内容（当前唯一能力维度）的处置声明。
type capability int

const (
	// capDrop 表示该位置当前不产出 / 不保留思考内容——是**待补的能力缺口**。
	capDrop capability = iota
	// capKeep 表示该位置保留思考内容。
	capKeep
	// capSkip 表示该位置**有意不尝试**：官方语义下要么必被拒绝、要么无收益。
	// 运行时观测与 capDrop 相同（思考内容不出现），区别只在语义——drop 是缺口，
	// skip 是已判定的不归路（理由见各格注释与设计文档 §1.2）。
	capSkip
)

func (c capability) String() string {
	switch c {
	case capKeep:
		return "keep"
	case capDrop:
		return "drop"
	case capSkip:
		return "skip"
	}
	return "unknown"
}

// keeps 报告该格子期望思考内容出现在输出体里（探测器的期望值）。
func (c capability) keeps() bool { return c == capKeep }

// capPosition 是矩阵的「位置」维度：请求 / 响应 × 入向 / 出向 × 是否流式，
// 与 providerImpl 的六个转换入口一一对应。
type capPosition string

const (
	posReqParse         capPosition = "reqParse"         // 入向请求解析（parseRequest）
	posRespParse        capPosition = "respParse"        // 入向响应解析（parseResponse，非流式）
	posRespStreamParse  capPosition = "respStreamParse"  // 入向响应解析（newToStream，流式）
	posReqRender        capPosition = "reqRender"        // 出向请求渲染（renderRequest）
	posRespRender       capPosition = "respRender"       // 出向响应渲染（renderResponse，非流式）
	posRespStreamRender capPosition = "respStreamRender" // 出向响应渲染（newFromStream，流式）
)

// capCell 是一个登记格子：处置声明 + 理由（drop / skip 必填）。
type capCell struct {
	value  capability
	reason string
}

// ===== (1) 期望登记表 =====

// inboundMatrix 登记「from 协议 → canonical（openai）」三个入向位置对思考内容的处置。
// 四个协议的入向能力现已齐备（anthropic 非流式响应的思考块缺口已在 P2 补齐，
// 见设计文档 §3.2）。
var inboundMatrix = map[Provider]map[capPosition]capCell{
	ProviderOpenAI: {
		posReqParse:        {capKeep, ""},
		posRespParse:       {capKeep, ""},
		posRespStreamParse: {capKeep, ""},
	},
	ProviderAnthropic: {
		posReqParse:        {capKeep, ""},
		posRespParse:       {capKeep, ""},
		posRespStreamParse: {capKeep, ""},
	},
	ProviderOpenAIResponses: {
		posReqParse:        {capKeep, ""},
		posRespParse:       {capKeep, ""},
		posRespStreamParse: {capKeep, ""},
	},
	ProviderGemini: {
		posReqParse:        {capKeep, ""},
		posRespParse:       {capKeep, ""},
		posRespStreamParse: {capKeep, ""},
	},
}

// outboundMatrix 登记「canonical（openai）→ to 协议」三个出向位置对思考内容的处置。
//
// 出向请求的三个格子是有意的 skip（设计文档 §1.2 官方语义考证）：
//   - anthropic：官方要求 thinking 块的 signature 原样回传，canonical 只有明文、
//     没有合法签名，填格只能伪造——上游默认 prefix_mismatch_behavior="error" 必
//     400；cc-switch 宁可报错、axonhub 宁可丢块，都不伪造。
//   - responses：非加密明文 reasoning item 会被官方无害丢弃，回传无收益；官方推荐
//     形态是 encrypted_content 加密回传（属独立功能）。
//   - gemini：官方强制的是 thoughtSignature（不是 thought 明文），缺的是签名；
//     补签名属独立功能（见设计文档 §5 衍生发现）。
//
// 出向非流式响应的缺口（responses / gemini）已在 P2 补齐（渲染器补产 reasoning
// item / thought part，与流式方向对齐，见设计文档 §3.2）。
var outboundMatrix = map[Provider]map[capPosition]capCell{
	ProviderOpenAI: {
		posReqRender:        {capKeep, ""},
		posRespRender:       {capKeep, ""},
		posRespStreamRender: {capKeep, ""},
	},
	ProviderAnthropic: {
		// skip：伪造 signature 必被 400，cc-switch / axonhub 均不伪造（设计文档 §1.2）。
		posReqRender:        {capSkip, "伪造 thinking signature 必被上游 400，签名无处安放明文"},
		posRespRender:       {capKeep, ""},
		posRespStreamRender: {capKeep, ""},
	},
	ProviderOpenAIResponses: {
		// skip：官方推荐 encrypted_content 加密回传，明文回传无收益（设计文档 §1.2）。
		posReqRender:        {capSkip, "明文 reasoning item 被官方丢弃；encrypted_content 回传属独立功能"},
		posRespRender:       {capKeep, ""},
		posRespStreamRender: {capKeep, ""},
	},
	ProviderGemini: {
		// skip：官方强制 thoughtSignature 而非 thought 明文，属独立功能（设计文档 §1.2）。
		posReqRender:        {capSkip, "官方强制 thoughtSignature，缺的是签名不是明文；补签名属独立功能"},
		posRespRender:       {capKeep, ""},
		posRespStreamRender: {capKeep, ""},
	},
}

// ===== (2) 探针 fixture =====

// capFixture 是一个协议的三份探针 fixture：请求体 / 非流式响应体 / 流式事件序列。
// 三者都携带 reasoningProbe 思考内容；正文用「答」以便探测器区分。
type capFixture struct {
	request      []byte
	response     []byte
	streamEvents [][]byte
}

var capFixtures = map[Provider]capFixture{
	// openai：思考走 canonical 扩展字段 reasoning_content。
	ProviderOpenAI: {
		request: []byte(`{"model":"m","messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","reasoning_content":"思考探针","content":"答"}
		]}`),
		response: []byte(`{"id":"c1","object":"chat.completion","model":"m","choices":[
			{"index":0,"message":{"role":"assistant","content":"答","reasoning_content":"思考探针"},"finish_reason":"stop"}
		]}`),
		streamEvents: [][]byte{
			[]byte(`{"id":"c1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"思考探针"},"finish_reason":null}]}`),
		},
	},
	// anthropic：思考是 content 里的 thinking 块。签名用非桥格式 "sig"——探测器
	// 只看 thinking 明文；入向解析对非桥签名退回明文（anthropic.go:298-302）。
	ProviderAnthropic: {
		request: []byte(`{"model":"m","max_tokens":16,"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[{"type":"thinking","thinking":"思考探针","signature":"sig"},{"type":"text","text":"答"}]}
		]}`),
		response: []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[
			{"type":"thinking","thinking":"思考探针","signature":"sig"},
			{"type":"text","text":"答"}
		],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`),
		streamEvents: [][]byte{
			[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"思考探针"}}`),
		},
	},
	// responses：思考是 input / output 里的 reasoning item（摘要放 summary[]）。
	ProviderOpenAIResponses: {
		request: []byte(`{"model":"m","input":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"思考探针"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"答"}]}
		]}`),
		response: []byte(`{"id":"resp_1","object":"response","status":"completed","model":"m","output":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"思考探针"}]},
			{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"答"}]}
		]}`),
		streamEvents: [][]byte{
			[]byte(`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"思考探针"}`),
		},
	},
	// gemini：思考是 parts 里 thought=true 的 part。
	ProviderGemini: {
		request: []byte(`{"contents":[{"role":"model","parts":[
			{"text":"思考探针","thought":true},{"text":"答"}
		]}]}`),
		response: []byte(`{"candidates":[{"content":{"role":"model","parts":[
			{"text":"思考探针","thought":true},{"text":"答"}
		]},"finishReason":"STOP"}]}`),
		streamEvents: [][]byte{
			[]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"思考探针","thought":true}]}}]}`),
		},
	},
}

// ===== (3) 原生探测器 =====

// detectReasoning 按协议调用对应的原生探测器，检测输出体（请求 / 响应 / 流式 chunk
// 共用形状）里是否携带探针思考内容。走原生形状而非 parse 回环，避免「两端能力互为
// confounder」（见设计文档 §1.3）。
func detectReasoning(p Provider, body []byte) bool {
	switch p {
	case ProviderOpenAI:
		return detectFieldReasoning(body, "reasoning_content")
	case ProviderAnthropic:
		return detectFieldReasoning(body, "thinking")
	case ProviderOpenAIResponses:
		return detectResponsesReasoning(body)
	case ProviderGemini:
		return detectGeminiReasoning(body)
	}
	return false
}

// detectFieldReasoning 检测 JSON 树里是否存在名为 field 且值等于探针的字符串字段
// （openai 的 reasoning_content / anthropic 的 thinking 明文均属此形）。
func detectFieldReasoning(body []byte, field string) bool {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return false
	}
	return jsonHasString(m, field, reasoningProbe)
}

// detectResponsesReasoning 检测 responses 输出：非流式为 output[] / input[] 里
// type=="reasoning" 的 summary / content 文本；流式为 reasoning 类事件的 delta。
func detectResponsesReasoning(body []byte) bool {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return false
	}
	for _, key := range []string{"output", "input"} {
		items, _ := m[key].([]any)
		for _, it := range items {
			item, _ := it.(map[string]any)
			if item["type"] != "reasoning" {
				continue
			}
			if jsonHasString(item["summary"], "text", reasoningProbe) ||
				jsonHasString(item["content"], "text", reasoningProbe) {
				return true
			}
		}
	}
	if typ, _ := m["type"].(string); strings.HasPrefix(typ, "response.reasoning_") {
		if d, _ := m["delta"].(string); d == reasoningProbe {
			return true
		}
	}
	return false
}

// detectGeminiReasoning 检测 gemini 输出里存在 thought=true 且 text 等于探针的 part。
func detectGeminiReasoning(body []byte) bool {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return false
	}
	return geminiHasThoughtProbe(m)
}

func geminiHasThoughtProbe(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if thought, _ := t["thought"].(bool); thought {
			if s, _ := t["text"].(string); s == reasoningProbe {
				return true
			}
		}
		for _, vv := range t {
			if geminiHasThoughtProbe(vv) {
				return true
			}
		}
	case []any:
		for _, item := range t {
			if geminiHasThoughtProbe(item) {
				return true
			}
		}
	}
	return false
}

// jsonHasString 递归遍历 JSON 树，报告是否存在「名为 key 且值恰为 want」的字符串字段。
func jsonHasString(v any, key, want string) bool {
	switch t := v.(type) {
	case map[string]any:
		if s, ok := t[key].(string); ok && s == want {
			return true
		}
		for _, vv := range t {
			if jsonHasString(vv, key, want) {
				return true
			}
		}
	case []any:
		for _, item := range t {
			if jsonHasString(item, key, want) {
				return true
			}
		}
	}
	return false
}

// streamHasReasoning 报告输出序列（单个非流式体或一串流式事件）里是否有任一
// 载荷携带探针思考内容。
func streamHasReasoning(p Provider, outs [][]byte) bool {
	for _, o := range outs {
		if detectReasoning(p, o) {
			return true
		}
	}
	return false
}

// ===== (4) runner + 穷尽检查 =====

// TestCapabilityMatrixRegistryConsistency 穷尽 providerImpls 全 key：每个协议必须在
// 入向 / 出向两张登记表里就六个位置表态，且 drop / skip 均带理由。新协议忘登记即红。
func TestCapabilityMatrixRegistryConsistency(t *testing.T) {
	for p := range providerImpls {
		inbound, ok := inboundMatrix[p]
		if !ok {
			t.Errorf("协议 %s 未在 inboundMatrix 登记：新协议接入必须表态（设计文档 §3.3）", p)
		} else if err := validateInbound(p, inbound); err != nil {
			t.Error(err)
		}
		outbound, ok := outboundMatrix[p]
		if !ok {
			t.Errorf("协议 %s 未在 outboundMatrix 登记：新协议接入必须表态（设计文档 §3.3）", p)
		} else if err := validateOutbound(p, outbound); err != nil {
			t.Error(err)
		}
	}
}

func validateInbound(p Provider, m map[capPosition]capCell) error {
	for _, pos := range []capPosition{posReqParse, posRespParse, posRespStreamParse} {
		if err := validateCell(p, "入向", pos, m); err != nil {
			return err
		}
	}
	return nil
}

func validateOutbound(p Provider, m map[capPosition]capCell) error {
	for _, pos := range []capPosition{posReqRender, posRespRender, posRespStreamRender} {
		if err := validateCell(p, "出向", pos, m); err != nil {
			return err
		}
	}
	return nil
}

func validateCell(p Provider, dir string, pos capPosition, m map[capPosition]capCell) error {
	cell, ok := m[pos]
	if !ok {
		return fmt.Errorf("协议 %s 的%s格子 %s 未登记", p, dir, pos)
	}
	if cell.value != capKeep && cell.reason == "" {
		return fmt.Errorf("协议 %s 的%s格子 %s=%s 缺理由（drop / skip 必须说明出处）", p, dir, pos, cell.value)
	}
	return nil
}

// TestCapabilityMatrixInboundPositions 逐协议钉死三个入向位置的自有能力：用该协议
// 探针经「from → openai」，在 canonical 的 reasoning_content 字段上探测（入向能力
// 与出向无关，故搭档固定 openai）。
func TestCapabilityMatrixInboundPositions(t *testing.T) {
	for _, from := range providerKeys() {
		fx := capFixtures[from]
		cases := []struct {
			name string
			pos  capPosition
			run  func() ([][]byte, error)
		}{
			{"reqParse", posReqParse, func() ([][]byte, error) {
				out, err := ConvertRequest(from, ProviderOpenAI, fx.request)
				return [][]byte{out}, err
			}},
			{"respParse", posRespParse, func() ([][]byte, error) {
				out, err := ConvertResponse(from, ProviderOpenAI, fx.response)
				return [][]byte{out}, err
			}},
			{"respStreamParse", posRespStreamParse, func() ([][]byte, error) {
				return runStreamFace(from, ProviderOpenAI)
			}},
		}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/%s", from, tc.name), func(t *testing.T) {
				outs, err := tc.run()
				if err != nil {
					t.Fatalf("转换失败: %v", err)
				}
				got := streamHasReasoning(ProviderOpenAI, outs)
				want := inboundMatrix[from][tc.pos].value.keeps()
				if got != want {
					t.Errorf("入向[%s].%s 期望思考内容 %v（%s），实际 %v",
						from, tc.pos, want, inboundMatrix[from][tc.pos].value, got)
				}
			})
		}
	}
}

// TestCapabilityMatrixOutboundPositions 逐协议钉死三个出向位置的自有能力：用 openai
// 探针经「openai → to」，在目标协议的原生形状上探测。
func TestCapabilityMatrixOutboundPositions(t *testing.T) {
	fx := capFixtures[ProviderOpenAI]
	for _, to := range providerKeys() {
		to := to
		cases := []struct {
			name string
			pos  capPosition
			run  func() ([][]byte, error)
		}{
			{"reqRender", posReqRender, func() ([][]byte, error) {
				out, err := ConvertRequest(ProviderOpenAI, to, fx.request)
				return [][]byte{out}, err
			}},
			{"respRender", posRespRender, func() ([][]byte, error) {
				out, err := ConvertResponse(ProviderOpenAI, to, fx.response)
				return [][]byte{out}, err
			}},
			{"respStreamRender", posRespStreamRender, func() ([][]byte, error) {
				return runStreamFace(ProviderOpenAI, to)
			}},
		}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/%s", to, tc.name), func(t *testing.T) {
				outs, err := tc.run()
				if err != nil {
					t.Fatalf("转换失败: %v", err)
				}
				got := streamHasReasoning(to, outs)
				want := outboundMatrix[to][tc.pos].value.keeps()
				if got != want {
					t.Errorf("出向[%s].%s 期望思考内容 %v（%s），实际 %v",
						to, tc.pos, want, outboundMatrix[to][tc.pos].value, got)
				}
			})
		}
	}
}

// TestCapabilityMatrixFullProduct 双循环展开 from × to（含直通）× 三面
// （请求 / 非流式响应 / 流式），期望 = 入向[from] ∧ 出向[to]；直通格（from==to）
// 探针原样透传，原生探测必通过，期望恒 keep。全部只用三个公开入口，顺带验证
// canonical 枢纽的降维假设（中间不引入额外损耗，见设计文档 §1.3）。
func TestCapabilityMatrixFullProduct(t *testing.T) {
	providers := providerKeys()
	faces := []struct {
		name string
		in   capPosition
		out  capPosition
		run  func(from, to Provider) ([][]byte, error)
	}{
		{"request", posReqParse, posReqRender, func(from, to Provider) ([][]byte, error) {
			out, err := ConvertRequest(from, to, capFixtures[from].request)
			return [][]byte{out}, err
		}},
		{"response", posRespParse, posRespRender, func(from, to Provider) ([][]byte, error) {
			out, err := ConvertResponse(from, to, capFixtures[from].response)
			return [][]byte{out}, err
		}},
		{"stream", posRespStreamParse, posRespStreamRender, func(from, to Provider) ([][]byte, error) {
			return runStreamFace(from, to)
		}},
	}
	for _, face := range faces {
		face := face
		for _, from := range providers {
			for _, to := range providers {
				from, to := from, to
				t.Run(fmt.Sprintf("%s/%s->%s", face.name, from, to), func(t *testing.T) {
					outs, err := face.run(from, to)
					if err != nil {
						t.Fatalf("转换失败: %v", err)
					}
					got := streamHasReasoning(to, outs)
					want := expectedMatrixKeep(from, to, face.in, face.out)
					if got != want {
						t.Errorf("期望思考内容 %v，实际 %v（入向[%s].%s=%s，出向[%s].%s=%s）",
							want, got,
							from, face.in, inboundMatrix[from][face.in].value,
							to, face.out, outboundMatrix[to][face.out].value)
					}
				})
			}
		}
	}
}

// expectedMatrixKeep 计算 from×to 在某面的期望：直通（from==to）时探针原样透传，
// 原生探测必通过（keep）；跨协议时经 canonical 中转，期望 = 入向[from] ∧ 出向[to]。
func expectedMatrixKeep(from, to Provider, inPos, outPos capPosition) bool {
	if from == to {
		return true
	}
	in, okIn := inboundMatrix[from][inPos]
	out, okOut := outboundMatrix[to][outPos]
	return okIn && okOut && in.value.keeps() && out.value.keeps()
}

// runStreamFace 用 from 协议的流式探针事件驱动 from→to 转换器，返回全部产出载荷。
func runStreamFace(from, to Provider) ([][]byte, error) {
	conv, err := NewStreamConverter(from, to)
	if err != nil {
		return nil, err
	}
	var outs [][]byte
	for _, ev := range capFixtures[from].streamEvents {
		parts, err := conv.Convert(ev)
		if err != nil {
			return nil, err
		}
		outs = append(outs, parts...)
	}
	finals, err := conv.Finish()
	if err != nil {
		return nil, err
	}
	return append(outs, finals...), nil
}

// providerKeys 返回 providerImpls 的全部 key，按字典序稳定排序（子测试名可复现）。
func providerKeys() []Provider {
	keys := make([]Provider, 0, len(providerImpls))
	for p := range providerImpls {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
