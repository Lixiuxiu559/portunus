package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// setupLogDBForTest 初始化独立的临时日志库（与 relay 测试的全套网关环境解耦，
// 只跑 logCall / classifyErr 这类纯落库断言）。
func setupLogDBForTest(t *testing.T) {
	t.Helper()
	closeDB := func() {
		if shared.DB != nil {
			if sqlDB, err := shared.DB.DB(); err == nil {
				sqlDB.Close()
			}
		}
	}
	closeDB()
	cfg := &shared.Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if _, err := shared.InitDB(cfg); err != nil {
		t.Fatalf("初始化 DB 失败: %v", err)
	}
	t.Cleanup(closeDB)
	if err := shared.InitLogDB(cfg); err != nil {
		t.Fatalf("初始化日志库失败: %v", err)
	}
}

// TestLogCallErrorAttribution 锁定失败归因落库：失败调用的日志必须带 err_kind
// （固定类别，可过滤统计）与 err_msg（错误原文），成功调用两者为空。
// 此前 status=0 / success=0 的日志无法区分「用户取消」与「真故障」，排障只能瞎猜。
func TestLogCallErrorAttribution(t *testing.T) {
	setupLogDBForTest(t)

	g := &group.Group{Name: "g"}
	tt := router.Target{Model: model.Model{Name: "m"}, Channel: channel.Channel{Name: "c"}}

	cases := []struct {
		name     string
		err      error
		wantKind string
	}{
		{"client cancel", context.Canceled, "client_cancel"},
		{"upstream status", &upstreamStatusError{status: 429, body: []byte(`{}`)}, "upstream_error"},
		{"watchdog stall", &streamStallError{stage: "静默", wait: time.Minute}, "watchdog_timeout"},
		{"committed cancel", &streamCommittedError{err: context.Canceled}, "client_cancel"},
		{"committed break", &streamCommittedError{err: io.ErrUnexpectedEOF}, "stream_interrupted"},
		{"network", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "network"},
		{"convert", &convertError{err: errors.New("解析 anthropic 请求失败")}, "convert_error"},
		{"internal", errors.New("boom"), "internal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logCall(1, g, tt, 0, false, nil, 1, 0, c.err)
			var entry shared.Log
			if err := shared.LogDB.Order("id desc").First(&entry).Error; err != nil {
				t.Fatalf("查日志失败: %v", err)
			}
			if entry.ErrKind != c.wantKind {
				t.Errorf("err_kind = %q, want %q", entry.ErrKind, c.wantKind)
			}
			if entry.ErrMsg == "" {
				t.Errorf("err_msg 不应为空")
			}
		})
	}

	// 成功调用不带归因字段
	logCall(1, g, tt, 200, true, nil, 1, 0, nil)
	var entry shared.Log
	if err := shared.LogDB.Order("id desc").First(&entry).Error; err != nil {
		t.Fatalf("查日志失败: %v", err)
	}
	if entry.ErrKind != "" || entry.ErrMsg != "" {
		t.Errorf("成功调用 err_kind/err_msg 应为空，实际: %q/%q", entry.ErrKind, entry.ErrMsg)
	}
}

// TestTruncateErrUtf8Safe 错误信息截断必须落在合法 UTF-8 边界上
// （多字节字符不被切半），结果总长不超过上限（含省略号），
// 且短于上限的原文原样保留。
func TestTruncateErrUtf8Safe(t *testing.T) {
	long := strings.Repeat("失败原因", 100) // 每个汉字 3 字节，共 1200 字节
	cut := truncateErr(long, 256)
	if len(cut) > 256 {
		t.Errorf("截断后长度 %d 超限（含省略号应 ≤ 256）", len(cut))
	}
	if !utf8.ValidString(cut) {
		t.Errorf("截断产生非法 UTF-8: %q", cut)
	}
	if !strings.HasSuffix(cut, "…") {
		t.Errorf("截断结果应以省略号结尾: %q", cut)
	}
	if truncateErr("short", 256) != "short" {
		t.Errorf("短于上限的错误信息不应被改动")
	}
	if truncateErr("", 256) != "" {
		t.Errorf("空串应原样返回")
	}
}

func TestComputeCost(t *testing.T) {
	m := model.Model{InputPrice: 2.5, OutputPrice: 10, CacheReadPrice: 1.25, CacheWritePrice: 2.5}

	// 1M 输入 + 1M 输出 → (1e6*2.5 + 1e6*10)/1e6 = 12.5
	u := &protocol.Usage{PromptTokens: 1000000, CompletionTokens: 1000000}
	if cost := computeCost(u, m); cost != 12.5 {
		t.Errorf("computeCost = %f, want 12.5", cost)
	}

	// 加上缓存读/写 → 12.5 + (1e6*1.25 + 1e6*2.5)/1e6 = 16.25
	u = &protocol.Usage{PromptTokens: 1000000, CompletionTokens: 1000000, CacheReadTokens: 1000000, CacheWriteTokens: 1000000}
	if cost := computeCost(u, m); cost != 16.25 {
		t.Errorf("computeCost with cache = %f, want 16.25", cost)
	}

	// 空用量 → 0
	if cost := computeCost(&protocol.Usage{}, m); cost != 0 {
		t.Errorf("空用量应为 0, got %f", cost)
	}
}
