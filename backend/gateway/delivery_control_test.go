package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

const deliveryRole = "data: {\"id\":\"prelude-only\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"
const deliveryFinish = "data: {\"id\":\"ok\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"

func deliveryConverter(t *testing.T, source, target protocol.Provider) protocol.StreamConverter {
	t.Helper()
	conv, err := protocol.NewStreamConverter(source, target)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func TestDeliveryPreludeFailureRetriesWithoutLeakingAttempt(t *testing.T) {
	for _, ending := range []string{"", "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"failed-id\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"busy\"}}}\n\n"} {
		t.Run(fmt.Sprintf("failed_event=%t", ending != ""), func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 1
			var calls atomic.Int32
			recorder := &attemptRecorder{}
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if calls.Add(1) == 1 {
					io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"failed-id\",\"status\":\"in_progress\",\"output\":[],\"usage\":{\"input_tokens\":99,\"output_tokens\":88,\"total_tokens\":187}}}\n\n"+ending)
					return
				}
				io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"successful-id\",\"status\":\"in_progress\",\"output\":[]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"successful-id\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n")
			}))
			w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hi","stream":true}`, key)
			if w.Code != 200 || calls.Load() != 2 || strings.Contains(w.Body.String(), "failed-id") || !strings.Contains(w.Body.String(), "successful-id") {
				t.Fatalf("attempt leaked or was not retried: calls=%d status=%d body=%s", calls.Load(), w.Code, w.Body.String())
			}
			rows := recorder.entries()
			if len(rows) != 2 || rows[0].Success || !rows[1].Success {
				t.Fatalf("attempt outcomes=%+v", rows)
			}
			if entry := logs.last(); entry == nil || entry.InputToken != 3 || entry.OutputToken != 2 || entry.ConversionPolicy != "" {
				t.Fatalf("final usage/policy=%+v", entry)
			}
		})
	}
}

func TestDeliveryPreludeHeartbeatCannotExtendFirstContentDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	w, c, resp, oldWD := streamRelayTestCtx(t, reader, nil)
	oldWD.Stop()
	ctx, wd := newStreamWatchdog(context.Background(), 40*time.Millisecond, time.Second)
	defer wd.Stop()
	go func() { <-ctx.Done(); writer.CloseWithError(ctx.Err()) }()
	go func() {
		for {
			if _, err := io.WriteString(writer, ": heartbeat\n\n"+deliveryRole); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	start := time.Now()
	_, err := relayStream(c, protocol.ProviderOpenAI, resp, deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderOpenAI), wd, streamRelayOptions{Source: protocol.ProviderOpenAI})
	if err == nil || wd.Fired() == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("metadata extended deadline: err=%v elapsed=%v", err, time.Since(start))
	}
	if c.Writer.Written() || w.Body.Len() != 0 || w.Flushed {
		t.Fatalf("prelude committed before content: body=%q", w.Body.String())
	}
}

func TestDeliveryPreludeBufferLimitStopsRequest(t *testing.T) {
	for _, opts := range []streamRelayOptions{{MaxBufferedBytes: 32}, {MaxBufferedEvents: 1}} {
		conv := deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderOpenAI)
		w, c, resp, wd := streamRelayTestCtx(t, strings.NewReader(deliveryRole+deliveryRole), conv)
		opts.Source = protocol.ProviderOpenAI
		_, err := relayStream(c, protocol.ProviderOpenAI, resp, conv, wd, opts)
		wd.Stop()
		if err == nil || !failSpec(err).StopRequest || failSpec(err).Retryable || failSpec(err).BreakerFail {
			t.Fatalf("buffer overflow must stop without blaming upstream: %v %+v", err, failSpec(err))
		}
		if c.Writer.Written() || w.Body.Len() != 0 || w.Flushed {
			t.Fatalf("buffer overflow leaked metadata: %q", w.Body.String())
		}
	}
}

type deliveryFaultWriter struct {
	header          http.Header
	mode            string
	writes, flushes int
}

func (w *deliveryFaultWriter) Header() http.Header { return w.header }
func (*deliveryFaultWriter) WriteHeader(int)       {}
func (w *deliveryFaultWriter) Write(p []byte) (int, error) {
	w.writes++
	switch w.mode {
	case "partial":
		return len(p) / 2, io.ErrClosedPipe
	case "short":
		return len(p) / 2, nil
	}
	return len(p), nil
}
func (w *deliveryFaultWriter) Flush()            { w.flushes++ }
func (w *deliveryFaultWriter) FlushError() error { w.flushes++; return io.ErrClosedPipe }

func TestDeliveryWriteFailureNeverAllowsReplay(t *testing.T) {
	for _, mode := range []string{"partial", "short", "flush"} {
		t.Run(mode, func(t *testing.T) {
			writer := &deliveryFaultWriter{header: make(http.Header), mode: mode}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(deliveryRole + testChunkLine + deliveryFinish + "data: [DONE]\n\n"))}
			_, wd := newStreamWatchdog(context.Background(), 0, 0)
			defer wd.Stop()
			report := &streamDeliveryReport{}
			_, err := relayStream(c, protocol.ProviderOpenAI, resp, deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderOpenAI), wd, streamRelayOptions{Source: protocol.ProviderOpenAI, Report: report})
			if err == nil || isRetryable(err) || failSpec(err).BreakerFail || !report.Started || !report.WriteFailed || report.TerminalWritten {
				t.Fatalf("write failure lost boundary: err=%v report=%+v decision=%+v", err, report, failSpec(err))
			}
			if writer.writes == 0 {
				t.Fatal("test never reached client write")
			}
		})
	}
}

func TestDeliveryValidEmptyTerminalCommitsOnce(t *testing.T) {
	for _, target := range []protocol.Provider{protocol.ProviderOpenAI, protocol.ProviderOpenAIResponses, protocol.ProviderAnthropic} {
		t.Run(string(target), func(t *testing.T) {
			conv := deliveryConverter(t, protocol.ProviderOpenAI, target)
			w, c, resp, wd := streamRelayTestCtx(t, strings.NewReader(deliveryRole+deliveryFinish+"data: [DONE]\n\n"), conv)
			defer wd.Stop()
			report := &streamDeliveryReport{}
			_, err := relayStream(c, target, resp, conv, wd, streamRelayOptions{Source: protocol.ProviderOpenAI, Report: report})
			if err != nil || !report.Started || !report.TerminalWritten || w.Code != 200 || w.Body.Len() == 0 {
				t.Fatalf("valid empty terminal rejected: err=%v report=%+v body=%s", err, report, w.Body.String())
			}
		})
	}
}

func TestDeliveryContentWithoutTerminalDoesNotSynthesizeSuccess(t *testing.T) {
	conv := deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderOpenAIResponses)
	w, c, resp, wd := streamRelayTestCtx(t, strings.NewReader(testChunkLine), conv)
	defer wd.Stop()
	_, err := relayStream(c, protocol.ProviderOpenAIResponses, resp, conv, wd, streamRelayOptions{Source: protocol.ProviderOpenAI})
	var committed *streamCommittedError
	if !errors.As(err, &committed) || isRetryable(err) || !strings.Contains(w.Body.String(), "hi") || strings.Contains(w.Body.String(), "response.completed") || strings.Contains(w.Body.String(), "data: [DONE]") {
		t.Fatalf("truncated content became success/replayable: err=%v body=%s", err, w.Body.String())
	}
}

func TestDeliveryReplayUnsafePreludeFailureStops(t *testing.T) {
	conv := deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderOpenAI)
	w, c, resp, wd := streamRelayTestCtx(t, strings.NewReader(deliveryRole), conv)
	defer wd.Stop()
	_, err := relayStream(c, protocol.ProviderOpenAI, resp, conv, wd, streamRelayOptions{Source: protocol.ProviderOpenAI, ReplayUnsafe: true})
	if err == nil || !failSpec(err).StopRequest || isRetryable(err) || c.Writer.Written() || w.Body.Len() != 0 {
		t.Fatalf("unsafe prelude failure replayable or leaked: err=%v decision=%+v body=%s", err, failSpec(err), w.Body.String())
	}
}

func TestDeliveryTerminalTailUsageAndTimeout(t *testing.T) {
	for _, tailTimeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout=%t", tailTimeout), func(t *testing.T) {
			conv := deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderOpenAIResponses)
			var body io.Reader = strings.NewReader(testChunkLine + deliveryFinish + "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":5,\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
			var pipeReader *io.PipeReader
			var pipeWriter *io.PipeWriter
			if tailTimeout {
				pipeReader, pipeWriter = io.Pipe()
				body = pipeReader
				defer pipeReader.Close()
				defer pipeWriter.Close()
			}
			w, c, resp, oldWD := streamRelayTestCtx(t, body, conv)
			oldWD.Stop()
			ctx, wd := newStreamWatchdog(context.Background(), time.Second, time.Second)
			defer wd.Stop()
			if tailTimeout {
				resp.Body = pipeReader
				go func() {
					io.WriteString(pipeWriter, testChunkLine+deliveryFinish)
					<-ctx.Done()
					pipeWriter.CloseWithError(ctx.Err())
				}()
			}
			report := &streamDeliveryReport{}
			start := time.Now()
			_, err := relayStream(c, protocol.ProviderOpenAIResponses, resp, conv, wd, streamRelayOptions{Source: protocol.ProviderOpenAI, Report: report, TerminalGrace: 20 * time.Millisecond})
			terminalCount := 0
			for _, line := range strings.Split(w.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				var event struct {
					Type string `json:"type"`
				}
				if parseErr := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); parseErr != nil {
					t.Fatal(parseErr)
				}
				if event.Type == "response.completed" {
					terminalCount++
				}
			}
			if err != nil || !report.TerminalWritten || terminalCount != 1 {
				t.Fatalf("terminal tail corrupted completion: err=%v report=%+v body=%s", err, report, w.Body.String())
			}
			if tailTimeout && time.Since(start) > 500*time.Millisecond {
				t.Fatalf("tail was not bounded: %v", time.Since(start))
			}
			if !tailTimeout && !strings.Contains(w.Body.String(), `"output_tokens":5`) {
				t.Fatalf("late usage missing from target terminal: %s", w.Body.String())
			}
		})
	}
}

func TestDeliveryCancellationBeforeAndAfterContent(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprintf("delivered=%t", delivered), func(t *testing.T) {
			payload := deliveryRole
			if delivered {
				payload += testChunkLine
			}
			conv := deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderAnthropic)
			w, c, resp, wd := streamRelayTestCtx(t, &seqReader{parts: [][]byte{[]byte(payload)}, err: context.Canceled}, conv)
			defer wd.Stop()
			_, err := relayStream(c, protocol.ProviderAnthropic, resp, conv, wd, streamRelayOptions{Source: protocol.ProviderOpenAI})
			decision := failSpec(err)
			if !errors.Is(err, context.Canceled) || decision.Retryable || decision.BreakerFail || strings.Contains(w.Body.String(), "event: error") {
				t.Fatalf("client cancellation misclassified: err=%v decision=%+v body=%s", err, decision, w.Body.String())
			}
			if !delivered && (c.Writer.Written() || w.Body.Len() != 0) {
				t.Fatalf("cancelled prelude leaked: %s", w.Body.String())
			}
		})
	}
}

func TestDeliveryNativeCompleteSSEFrameIsPreserved(t *testing.T) {
	const native = ": vendor comment\r\nevent: vendor.native\r\nid: opaque-42\r\nretry: 123\r\ndata: {\"type\":\"vendor.native\",\r\ndata: \"n\":9007199254740993}\r\n\r\n"
	conv := deliveryConverter(t, protocol.ProviderOpenAI, protocol.ProviderOpenAI)
	w, c, resp, wd := streamRelayTestCtx(t, strings.NewReader(native+deliveryFinish+"data: [DONE]\n\n"), conv)
	defer wd.Stop()
	_, err := relayStream(c, protocol.ProviderOpenAI, resp, conv, wd, streamRelayOptions{Source: protocol.ProviderOpenAI})
	if err != nil || strings.Count(w.Body.String(), native) != 1 {
		t.Fatalf("native event was rebuilt: err=%v body=%q", err, w.Body.String())
	}
}

func TestDeliveryGatewayUnsafeNativeTool503StopsAllTargets(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	var calls atomic.Int32
	recorder := &attemptRecorder{}
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"busy after native execution may have started","type":"server_error"}}`)
	}))
	// A second candidate makes this an assertion against both same-target retry
	// and the outer failover loop, even when the first result is HTTP 503.
	var ch channel.Channel
	var g group.Group
	if err := shared.DB.First(&ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.First(&g).Error; err != nil {
		t.Fatal(err)
	}
	fallback := model.Model{ChannelID: ch.ID, Name: "fallback-model"}
	if err := shared.DB.Create(&fallback).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: fallback.ID, Priority: 1}).Error; err != nil {
		t.Fatal(err)
	}
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"click the button","stream":true,"tools":[{"type":"computer_use_preview","display_width":1024,"display_height":768,"environment":"browser"}]}`, key)
	rows := recorder.entries()
	if calls.Load() != 1 || len(rows) != 1 || w.Code != 503 || rows[0].Success || rows[0].Status != 503 {
		t.Fatalf("unsafe native tool replayed: calls=%d rows=%+v response=%d %s", calls.Load(), rows, w.Code, w.Body.String())
	}
	logs.mu.Lock()
	defer logs.mu.Unlock()
	if len(logs.logs) != 1 || logs.logs[0].Success || logs.logs[0].ErrKind != shared.ErrKindUpstream || logs.logs[0].ConversionPolicy != "" {
		t.Fatalf("unsafe request must stop with one failed diagnostic log: %+v", logs.logs)
	}
}

func TestDeliveryGatewayPersistsTerminalAndTruncatedOutcomes(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed=%t", complete), func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 3
			var calls atomic.Int32
			recorder := &attemptRecorder{}
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, testChunkLine)
				if complete {
					io.WriteString(w, deliveryFinish+"data: [DONE]\n\n")
				}
			}))
			w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hi"}],"stream":true}`, key)
			rows := recorder.entries()
			if w.Code != 200 || calls.Load() != 1 || len(rows) != 1 || rows[0].Success != complete || !strings.Contains(w.Body.String(), `"content":"hi"`) {
				t.Fatalf("delivered stream was replayed/outcome lost: complete=%t calls=%d rows=%+v body=%s", complete, calls.Load(), rows, w.Body.String())
			}
			entry := logs.last()
			wantOutcome := "truncated"
			if complete {
				wantOutcome = "completed"
			}
			if entry == nil || entry.Success != complete || entry.StreamOutcome != wantOutcome || !entry.StreamDeliveryStarted || entry.StreamTerminalObserved != complete || entry.StreamTerminalWritten != complete || entry.StreamWriteFailed || entry.ConversionPolicy != "" {
				t.Fatalf("delivery diagnostics lost: %+v", entry)
			}
			if !complete && (strings.Contains(w.Body.String(), "[DONE]") || entry.ErrKind != shared.ErrKindStreamInterrupt) {
				t.Fatalf("truncated stream became successful: log=%+v body=%s", entry, w.Body.String())
			}
		})
	}
}

func TestDeliveryFailureDecisionTable(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		err                  error
		retry, stop, breaker bool
		kind                 string
	}{
		{"missing_terminal", protocol.ErrStreamIncomplete, true, false, true, shared.ErrKindUpstream},
		{"buffer_limit", &precommitBufferError{}, false, true, false, shared.ErrKindConvert},
		{"oversized_sse", protocol.ErrSSEEventTooLarge, false, true, false, shared.ErrKindConvert},
		{"invalid_stream", protocol.ErrStreamInvalid, false, true, false, shared.ErrKindConvert},
		{"committed_resource", &streamCommittedError{err: protocol.ErrSSEEventTooLarge}, false, false, false, shared.ErrKindStreamInterrupt},
		{"committed_invalid", &streamCommittedError{err: protocol.ErrStreamInvalid}, false, false, false, shared.ErrKindStreamInterrupt},
		{"client_write", &streamCommittedError{err: &deliveryWriteError{err: io.ErrClosedPipe}}, false, false, false, shared.ErrKindClientWrite},
		{"committed_missing_terminal", &streamCommittedError{err: protocol.ErrStreamIncomplete}, false, false, true, shared.ErrKindStreamInterrupt},
		{"unsafe_503", &streamReplayUnsafeError{err: &upstreamStatusError{status: 503}}, false, true, true, shared.ErrKindUpstream},
		{"cancelled", context.Canceled, false, true, false, shared.ErrKindClientCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := failSpec(tc.err)
			if d.Retryable != tc.retry || d.StopRequest != tc.stop || d.BreakerFail != tc.breaker || d.ErrKind != tc.kind {
				t.Fatalf("decision=%+v want retry=%t stop=%t breaker=%t kind=%s", d, tc.retry, tc.stop, tc.breaker, tc.kind)
			}
		})
	}
}

func TestDeliveryNativeErrorAfterContentPreservesFrame(t *testing.T) {
	for _, source := range []protocol.Provider{protocol.ProviderOpenAI, protocol.ProviderAnthropic} {
		t.Run(string(source), func(t *testing.T) {
			content := testChunkLine
			if source == protocol.ProviderAnthropic {
				content = "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"
			}
			errorFrame := "event: error\r\nid: native-error\r\nretry: 25\r\ndata: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"busy\"}}\r\n\r\n"
			conv := deliveryConverter(t, source, source)
			w, c, resp, wd := streamRelayTestCtx(t, strings.NewReader(content+errorFrame), conv)
			defer wd.Stop()
			report := &streamDeliveryReport{}
			_, err := relayStream(c, source, resp, conv, wd, streamRelayOptions{Source: source, Report: report})
			if err == nil || failSpec(err).Retryable || report.Outcome.Status != "failed" || w.Body.String() != content+errorFrame || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("native error lost or replayable: err=%v report=%+v body=%q", err, report, w.Body.String())
			}
		})
	}
}
