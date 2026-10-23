package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
)

// SSEEvent retains the complete wire event, including comments and unknown fields.
// Data joins data lines with LF, as required by SSE. Raw is never logged.
type SSEEvent struct {
	Event   string
	ID      string
	Retry   *uint64
	Data    []byte
	Raw     []byte
	HasData bool
}

var ErrSSEEventTooLarge = errors.New("SSE event exceeds resource limit")

// SSEReader bounds raw allocation before appending each byte, including a line
// without a newline. Its small buffered reader supports arbitrary network splits.
type SSEReader struct {
	r         *bufio.Reader
	max       int
	eof       bool
	pendingLF bool
	firstLine bool
}

func NewSSEReader(r io.Reader, maxBytes int) *SSEReader {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	return &SSEReader{r: bufio.NewReaderSize(r, 4096), max: maxBytes, firstLine: true}
}
func (r *SSEReader) ReadEvent() (SSEEvent, error) {
	var ev SSEEvent
	if r.eof {
		return ev, io.EOF
	}
	var line []byte
	appendRaw := func(b byte) error {
		if len(ev.Raw) >= r.max {
			return ErrSSEEventTooLarge
		}
		ev.Raw = append(ev.Raw, b)
		return nil
	}
	consume := func() {
		if r.firstLine {
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
			r.firstLine = false
		}
		if len(line) == 0 || line[0] == ':' {
			return
		}
		key, value, hasColon := bytes.Cut(line, []byte(":"))
		if !hasColon {
			value = nil
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch string(key) {
		case "event":
			ev.Event = string(value)
		case "id":
			if !bytes.ContainsRune(value, 0) {
				ev.ID = string(value)
			}
		case "retry":
			valid := len(value) > 0
			for _, b := range value {
				if b < '0' || b > '9' {
					valid = false
				}
			}
			if valid {
				if n, err := strconv.ParseUint(string(value), 10, 64); err == nil {
					ev.Retry = &n
				}
			}
		case "data":
			if ev.HasData {
				ev.Data = append(ev.Data, '\n')
			}
			ev.HasData = true
			ev.Data = append(ev.Data, value...)
		}
	}
	for {
		b, err := r.r.ReadByte()
		if err != nil {
			if err != io.EOF {
				return SSEEvent{}, err
			}
			r.eof = true
			if len(ev.Raw) == 0 {
				return SSEEvent{}, io.EOF
			}
			consume()
			return ev, nil
		}
		if err = appendRaw(b); err != nil {
			return SSEEvent{}, err
		}
		if r.pendingLF {
			r.pendingLF = false
			if b == '\n' {
				continue
			}
		}
		if b != '\r' && b != '\n' {
			line = append(line, b)
			continue
		}
		if b == '\r' {
			// A CR already terminates the line. Never block to guess whether a
			// future byte is LF: bare-CR streams may keep the connection open.
			// A split optional LF is preserved at the start of the next Raw.
			if r.r.Buffered() > 0 {
				next, _ := r.r.Peek(1)
				if next[0] == '\n' {
					_, _ = r.r.ReadByte()
					if err = appendRaw('\n'); err != nil {
						return SSEEvent{}, err
					}
				}
			} else {
				r.pendingLF = true
			}
		}
		if len(line) == 0 {
			r.firstLine = false
			return ev, nil
		}
		consume()
		line = line[:0]
	}
}
