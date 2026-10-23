package gateway

import (
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// relayAttemptTrace 独占于一轮 relay；sequence 由同一客户端请求内串行执行的
// relay 共用。不记录正文、URL 或鉴权头，也不将 HTTP 200 等同于模型 completed。
type relayAttemptTrace struct {
	routing  *routingTargetTrace
	sequence *int
	template shared.UpstreamAttempt
	active   *shared.UpstreamAttempt
}

func (a *relayAttemptTrace) begin(body []byte, reason string) {
	*a.sequence = *a.sequence + 1
	entry := a.template
	entry.Sequence = *a.sequence
	if reason != "" {
		entry.Reason = reason
	}
	entry.StartedAt = time.Now()
	entry.BudgetField = budgetFieldNames(body)
	a.active = &entry
}

func (s *relayServer) finishAttempt(a *relayAttemptTrace, status int, success bool, callErr error) {
	if a == nil || a.active == nil {
		return
	}
	entry := *a.active
	a.active = nil // 一次 Do 只写一次，兼容重试与 relay defer 不重复计入。
	entry.Status = status
	entry.Success = success
	entry.DurationMs = time.Since(entry.StartedAt).Milliseconds()
	entry.CreatedAt = time.Now()
	if callErr != nil {
		entry.ErrKind = failSpec(callErr).ErrKind
		// url.Error 会把 URL（可能含 query 凭据）拼进 Error()，不存这部分。
		var urlErr *url.Error
		if errors.As(callErr, &urlErr) {
			entry.ErrMsg = truncateErr(urlErr.Err.Error(), 256)
		} else {
			entry.ErrMsg = truncateErr(callErr.Error(), 256)
		}
	}
	if s.deps.AttemptWrite != nil {
		if err := s.deps.AttemptWrite(&entry); err != nil {
			log.Printf("上游尝试记录写入失败: request_id=%s sequence=%d err=%v", entry.RequestID, entry.Sequence, err)
		}
	}
}

func budgetFieldNames(body []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return ""
	}
	var names []string
	for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if _, ok := fields[field]; ok {
			names = append(names, field)
		}
	}
	return strings.Join(names, ",")
}
