package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// fixedConv 固定产出的转换器替身，供帧器单测使用。
type fixedConv struct {
	convert [][]byte
	finish  [][]byte
}

func (c *fixedConv) Convert([]byte) ([][]byte, error) { return c.convert, nil }
func (c *fixedConv) Finish() ([][]byte, error)        { return c.finish, nil }
func (c *fixedConv) Usage() *Usage                    { return nil }

func TestStreamFramerUnframe(t *testing.T) {
	f := NewStreamFramer(ProviderOpenAI, &fixedConv{})
	rows := []struct {
		name          string
		line          string
		wantPayload   string
		wantOK, wantD bool
	}{
		{"data 行", `data: {"a":1}`, `{"a":1}`, true, false},
		{"data 行含空格", `data:   {"a":1}  `, `{"a":1}`, true, false},
		{"DONE", `data: [DONE]`, "", false, true},
		{"非 data 行（event:）", `event: message_start`, "", false, false},
		{"注释行", `: keepalive`, "", false, false},
		{"空载荷", `data:  `, "", false, false},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			payload, ok, done := f.Unframe([]byte(tc.line))
			if ok != tc.wantOK || done != tc.wantD {
				t.Fatalf("Unframe ok=%v done=%v, want ok=%v done=%v", ok, done, tc.wantOK, tc.wantD)
			}
			if tc.wantOK && string(payload) != tc.wantPayload {
				t.Fatalf("payload = %q, want %q", payload, tc.wantPayload)
			}
		})
	}
}

func TestStreamFramerFrame(t *testing.T) {
	t.Run("anthropic 附 event 行", func(t *testing.T) {
		f := NewStreamFramer(ProviderAnthropic, &fixedConv{convert: [][]byte{[]byte(`{"type":"content_block_delta","x":1}`)}})
		frames, err := f.ConvertPayload([]byte(`{"in":1}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 1 || string(frames[0]) != "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"x\":1}\n\n" {
			t.Fatalf("anthropic 帧形状错误: %q", frames)
		}
	})
	t.Run("anthropic 无 type 不发 event 行", func(t *testing.T) {
		f := NewStreamFramer(ProviderAnthropic, &fixedConv{convert: [][]byte{[]byte(`{"x":1}`)}})
		frames, _ := f.ConvertPayload([]byte(`{"in":1}`))
		if len(frames) != 1 || string(frames[0]) != "data: {\"x\":1}\n\n" {
			t.Fatalf("无 type 载荷不应有 event 行: %q", frames)
		}
	})
	t.Run("openai 只发 data 行", func(t *testing.T) {
		f := NewStreamFramer(ProviderOpenAI, &fixedConv{convert: [][]byte{[]byte(`{"x":1}`)}})
		frames, _ := f.ConvertPayload([]byte(`{"in":1}`))
		if len(frames) != 1 || string(frames[0]) != "data: {\"x\":1}\n\n" {
			t.Fatalf("openai 帧形状错误: %q", frames)
		}
	})
}

func TestStreamFramerFinishFrames(t *testing.T) {
	t.Run("openai 补 [DONE]", func(t *testing.T) {
		f := NewStreamFramer(ProviderOpenAI, &fixedConv{finish: [][]byte{[]byte(`{"final":true}`)}})
		frames, err := f.FinishFrames()
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 2 || !strings.Contains(string(frames[1]), "[DONE]") {
			t.Fatalf("openai 收尾应带 [DONE]: %q", frames)
		}
	})
	t.Run("responses 补 [DONE]", func(t *testing.T) {
		f := NewStreamFramer(ProviderOpenAIResponses, &fixedConv{})
		frames, _ := f.FinishFrames()
		if len(frames) != 1 || !strings.Contains(string(frames[0]), "[DONE]") {
			t.Fatalf("responses 收尾应带 [DONE]: %q", frames)
		}
	})
	t.Run("anthropic 不补 [DONE]", func(t *testing.T) {
		f := NewStreamFramer(ProviderAnthropic, &fixedConv{finish: [][]byte{[]byte(`{"type":"message_stop"}`)}})
		frames, _ := f.FinishFrames()
		if len(frames) != 1 || strings.Contains(string(frames[0]), "[DONE]") {
			t.Fatalf("anthropic 收尾不应补 [DONE]: %q", frames)
		}
	})
}

func TestStreamFramerErrorFrame(t *testing.T) {
	// anthropic：嵌套错误对象必须在 "error" 键下（官方 SDK 只读 body.error.type），
	// 且带 event: error 行（SDK 按 sse.event === 'error' 分支）。
	f := NewStreamFramer(ProviderAnthropic, &fixedConv{})
	frame := f.ErrorFrame("overloaded_error", "上游静默超时")
	s := string(frame)
	if !strings.HasPrefix(s, "event: error\n") {
		t.Fatalf("error 帧应带 event: error 行: %q", s)
	}
	var ev struct {
		Type  string `json:"type"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(s), "event: error\ndata: ")), &ev); err != nil {
		t.Fatalf("error 帧载荷不是合法 JSON: %v", err)
	}
	if ev.Type != "error" || ev.Error == nil || ev.Error.Type != "overloaded_error" || ev.Error.Message != "上游静默超时" {
		t.Fatalf("error 帧形状错误: %s", s)
	}

	// openai / responses：无流内错误标准形式，返回 nil（保持截断）。
	for _, p := range []Provider{ProviderOpenAI, ProviderOpenAIResponses, ProviderGemini} {
		if frame := NewStreamFramer(p, &fixedConv{}).ErrorFrame("api_error", "x"); frame != nil {
			t.Fatalf("%s 客户端不应有流内 error 帧: %q", p, frame)
		}
	}
}
