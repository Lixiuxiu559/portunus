package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
)

// 本文件是 SSE 传输帧的唯一权威：解帧（上游 data: 行 → 纯 JSON 载荷、[DONE]
// 终止）与装帧（按客户端协议补 event: 行 / [DONE] 收尾 / 流内 error 事件形状）。
// 传输编码与语义映射是两层关注点——StreamConverter 只管纯 JSON 载荷的语义转换，
// 帧知识此前散在 gateway/stream.go（parseSSEDataLine / writeSSE / [DONE] 按客户端
// 协议分叉 / 手拼 anthropic error JSON），现收敛于 StreamFramer。

// StreamFramer 把一个 StreamConverter 包成「SSE 帧进出」：喂入上游原始 SSE 行，
// 产出可直接写回客户端的帧字节。逐请求构造，包装该请求的转换器。
type StreamFramer struct {
	clientProto Provider
	conv        StreamConverter
}

// NewStreamFramer 包装 conv：解帧按上游 SSE 通用格式（data: 行），装帧按客户端协议。
func NewStreamFramer(clientProto Provider, conv StreamConverter) *StreamFramer {
	return &StreamFramer{clientProto: clientProto, conv: conv}
}

// Unframe 解析一行上游 SSE：data: 行返回载荷（ok=true）；`[DONE]` 返回 done=true
// （上游宣告结束，调用方应停止读取、转入收尾）；其余行（event: / 注释 / 空行 /
// 空载荷）ok=false 可跳过。
func (f *StreamFramer) Unframe(line []byte) (payload []byte, ok bool, done bool) {
	if !strings.HasPrefix(string(line), "data:") {
		return nil, false, false
	}
	p := strings.TrimSpace(string(line)[len("data:"):])
	if p == "" {
		return nil, false, false
	}
	if p == "[DONE]" {
		return nil, false, true
	}
	return []byte(p), true, false
}

// ConvertPayload 把一个上游载荷经转换器映射为目标协议载荷，并装帧为完整 SSE 帧。
func (f *StreamFramer) ConvertPayload(payload []byte) ([][]byte, error) {
	chunks, err := f.conv.Convert(payload)
	if err != nil {
		return nil, err
	}
	return f.frameAll(chunks), nil
}

// FinishFrames 转换器收尾：finals 装帧后，openai / responses 客户端补 [DONE]
// 终止帧；anthropic 客户端已在 message_stop 事件里结束，不再补。
func (f *StreamFramer) FinishFrames() ([][]byte, error) {
	finals, err := f.conv.Finish()
	if err != nil {
		return nil, err
	}
	frames := f.frameAll(finals)
	if f.clientProto != ProviderAnthropic {
		frames = append(frames, []byte("data: [DONE]\n\n"))
	}
	return frames, nil
}

// ErrorFrame 流内错误事件帧。Anthropic 的 error 是终止事件，形如
// {"type":"error","error":{"type":...,"message":...}}——嵌套错误对象必须在
// "error" 键下（官方 SDK 只读 body.error.type 判类型，写别的键名客户端识别不了，
// 只能退化为非流式回退）。Claude Code v2.1.260 实测（/tmp 实验室复现，2026-09）：
// 收到流内 error 后——已有文本内容时静默把残缺内容定稿为完整回答（无警告；
// 工具调用等无法定稿时才显示「Server error mid-response」）；无内容时显示重试
// 横幅（错误原文可见）重试 2 次，耗尽后自动降级为非流式请求重发——用户视角即
// 「卡住 → 重试 → 报检查网关/网络」，流已被掐断这件事客户端不会明说。
// OpenAI / Responses 客户端无对应的流内错误标准形式，返回 nil（保持截断，由
// 客户端按断流处理）。何时发（committed 且非客户端取消）、发哪类（转换失败
// api_error / 静默断连 overloaded_error）由调用方编排层决定。
func (f *StreamFramer) ErrorFrame(kind, message string) []byte {
	if f.clientProto != ProviderAnthropic {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": kind, "message": message},
	})
	if err != nil {
		return nil
	}
	return f.frame(payload)
}

// frameAll 批量装帧。
func (f *StreamFramer) frameAll(chunks [][]byte) [][]byte {
	if len(chunks) == 0 {
		return nil
	}
	frames := make([][]byte, 0, len(chunks))
	for _, c := range chunks {
		frames = append(frames, f.frame(c))
	}
	return frames
}

// frame 把一个载荷装帧：anthropic 按载荷顶层 type 字段附 event: 行（无 type 不发），
// 全协议统一 data: 行 + 空行终止。
func (f *StreamFramer) frame(payload []byte) []byte {
	var b bytes.Buffer
	if f.clientProto == ProviderAnthropic {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &ev) == nil && ev.Type != "" {
			b.WriteString("event: ")
			b.WriteString(ev.Type)
			b.WriteByte('\n')
		}
	}
	b.WriteString("data: ")
	b.Write(payload)
	b.WriteString("\n\n")
	return b.Bytes()
}
