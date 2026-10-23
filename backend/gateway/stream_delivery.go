package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/gin-gonic/gin"
)

const (
	defaultPrecommitBytes  = 256 * 1024
	defaultPrecommitEvents = 128
	defaultTerminalGrace   = time.Second
)

type streamDeliveryReport struct {
	Started               bool
	TerminalWritten       bool
	WriteFailed           bool
	TailLimited           bool
	Outcome               protocol.StreamOutcome
	ResponseCompatibility protocol.ConversionReport
}

type streamRelayOptions struct {
	OnRateLimit       func() // 源429已识别时先观察，不能被后续客户端写失败覆盖
	Source            protocol.Provider
	ReplayUnsafe      bool
	Report            *streamDeliveryReport
	MaxBufferedBytes  int
	MaxBufferedEvents int
	TerminalGrace     time.Duration
}

type precommitBufferError struct{}

func (*precommitBufferError) Error() string { return "流式预提交缓存超过上限" }

type deliveryWriteError struct{ err error }

func (e *deliveryWriteError) Error() string { return "客户端流写入失败" }
func (e *deliveryWriteError) Unwrap() error { return e.err }

type streamReplayUnsafeError struct{ err error }

func (e *streamReplayUnsafeError) Error() string {
	return "上游请求可能已执行，禁止自动重放"
}
func (e *streamReplayUnsafeError) Unwrap() error { return e.err }

// deliveryGate 是唯一客户端写入口。缓存的元数据属于当前尝试，失败时整批丢弃。
type deliveryGate struct {
	c                   *gin.Context
	report              *streamDeliveryReport
	frames              [][]byte
	bytes, events       int
	maxBytes, maxEvents int
	first               time.Time
}

func newDeliveryGate(c *gin.Context, opt streamRelayOptions) *deliveryGate {
	if opt.Report == nil {
		opt.Report = &streamDeliveryReport{}
	}
	if opt.MaxBufferedBytes <= 0 {
		opt.MaxBufferedBytes = defaultPrecommitBytes
	}
	if opt.MaxBufferedEvents <= 0 {
		opt.MaxBufferedEvents = defaultPrecommitEvents
	}
	return &deliveryGate{c: c, report: opt.Report, maxBytes: opt.MaxBufferedBytes, maxEvents: opt.MaxBufferedEvents}
}

func (g *deliveryGate) push(batch protocol.StreamBatch, sourceBytes ...int) error {
	if err := requestControlError(g.c.Request.Context()); err != nil {
		return err
	}
	if !g.report.Started && (!batch.Meaningful && !batch.Terminal || len(batch.Frames) == 0) {
		g.events++
		if g.events > g.maxEvents {
			return &precommitBufferError{}
		}
		encodedBytes := 0
		for _, frame := range batch.Frames {
			encodedBytes += len(frame)
		}
		chargedBytes := encodedBytes
		if len(sourceBytes) > 0 && sourceBytes[0] > chargedBytes {
			chargedBytes = sourceBytes[0]
		}
		if chargedBytes > g.maxBytes-g.bytes {
			return &precommitBufferError{}
		}
		g.bytes += chargedBytes
		for _, frame := range batch.Frames {
			g.frames = append(g.frames, frame)
		}
		return nil
	}
	if len(batch.Frames) == 0 {
		return nil
	}
	if !g.report.Started {
		// 必须在首次 WriteHeader/Write 前进入 Committing；短写和 Flush 失败都不可重放。
		if control := controlFrom(g.c.Request.Context()); control != nil {
			if err := control.commit(); err != nil {
				return err
			}
		}
		g.report.Started = true
		g.first = time.Now()
		writeStreamHeaders(g.c)
		if err := g.write(g.frames); err != nil {
			return err
		}
		g.frames = nil
		g.bytes = 0
	}
	if err := g.write(batch.Frames); err != nil {
		return err
	}
	if err := flushStreamWriter(g.c.Writer); err != nil {
		g.report.WriteFailed = true
		return &deliveryWriteError{err: err}
	}
	if batch.Terminal {
		g.report.TerminalWritten = true
	}
	return nil
}

func (g *deliveryGate) write(frames [][]byte) error {
	for _, frame := range frames {
		if err := requestControlError(g.c.Request.Context()); err != nil {
			return err
		}
		n, err := g.c.Writer.Write(frame)
		if err == nil && n != len(frame) {
			err = io.ErrShortWrite
		}
		if err != nil {
			g.report.WriteFailed = true
			return &deliveryWriteError{err: err}
		}
	}
	return nil
}

// Gin 的 Flush 无返回值；先解开 wrapper，才能观察底层 FlushError。
func flushStreamWriter(w http.ResponseWriter) error {
	for {
		if flusher, ok := w.(interface{ FlushError() error }); ok {
			return flusher.FlushError()
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return http.NewResponseController(w).Flush()
		}
		next := unwrapper.Unwrap()
		if next == nil {
			return http.ErrNotSupported
		}
		w = next
	}
}

// streamReadProgress 只在交付后的正常内容阶段续静默计时。元数据/心跳不能续首内容期限。
type streamReadProgress struct {
	r       io.Reader
	wd      *streamWatchdog
	report  *streamDeliveryReport
	tailing *bool
}

func (r *streamReadProgress) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 && r.report.Started && !*r.tailing {
		r.wd.Reset()
	}
	return n, err
}

func streamSucceeded(outcome protocol.StreamOutcome) bool {
	return outcome.TerminalObserved && (outcome.Status == "completed" || outcome.Status == "incomplete" || outcome.Status == "blocked")
}

func finishStreamError(c *gin.Context, fr *protocol.StreamFramer, gate *deliveryGate, unsafe bool, err error, kind string) error {
	if gate.report.Started {
		var writeErr *deliveryWriteError
		if !gate.report.WriteFailed && !gate.report.TerminalWritten && !errors.As(err, &writeErr) && !errors.Is(err, context.Canceled) && c.Request.Context().Err() == nil {
			if frame := fr.ErrorFrame(kind, "上游流处理失败"); len(frame) > 0 {
				_ = gate.write([][]byte{frame})
				if flushErr := flushStreamWriter(c.Writer); flushErr != nil {
					gate.report.WriteFailed = true
				}
			}
		}
		return &streamCommittedError{err: err}
	}
	if unsafe && !errors.Is(err, context.Canceled) {
		return &streamReplayUnsafeError{err: err}
	}
	return err
}
