package model

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func setupSyncTest(t *testing.T) {
	t.Helper()
	if shared.DB != nil {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	}
	cfg := &shared.Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if _, err := shared.InitDB(cfg); err != nil {
		t.Fatalf("初始化 DB 失败: %v", err)
	}
	if err := shared.AutoMigrate(&channel.Channel{}, &Model{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
}

func TestSyncFromChannel(t *testing.T) {
	setupSyncTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"gpt-4o","object":"model"},{"id":"gpt-4o-mini","object":"model"},{"id":"some-custom-model","object":"model"}]}`)
	}))
	defer srv.Close()

	ch := channel.Channel{Name: "c1", Type: protocol.ProviderOpenAI, BaseURL: srv.URL, Key: "test-key"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}

	added, err := SyncFromChannel(&ch)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if added != 3 {
		t.Fatalf("新增数 = %d, want 3", added)
	}

	var ms []Model
	if err := shared.DB.Where("channel_id = ?", ch.ID).Find(&ms).Error; err != nil {
		t.Fatalf("查询模型失败: %v", err)
	}
	if len(ms) != 3 {
		t.Fatalf("模型数 = %d, want 3", len(ms))
	}

	byName := map[string]Model{}
	for _, m := range ms {
		byName[m.Name] = m
	}
	if g := byName["gpt-4o"]; g.InputPrice != 2.5 || g.OutputPrice != 10 {
		t.Errorf("gpt-4o 默认价错误: %+v", g)
	}
	if c := byName["some-custom-model"]; c.InputPrice != 0 || c.OutputPrice != 0 {
		t.Errorf("未匹配模型价格应为 0: %+v", c)
	}

	// 重复同步不应重复插入
	added2, err := SyncFromChannel(&ch)
	if err != nil {
		t.Fatalf("二次同步失败: %v", err)
	}
	if added2 != 0 {
		t.Fatalf("二次同步新增数 = %d, want 0", added2)
	}
}

func TestSyncFromChannelUnauthorized(t *testing.T) {
	setupSyncTest(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ch := channel.Channel{Name: "c2", Type: protocol.ProviderOpenAI, BaseURL: srv.URL, Key: "bad"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}

	if _, err := SyncFromChannel(&ch); err == nil {
		t.Fatal("上游返回 401 时应返回错误")
	}
}

func TestSyncAutoChannels(t *testing.T) {
	setupSyncTest(t)

	// 一个正常返回 2 个模型的上游
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`)
	}))
	defer okSrv.Close()

	// 一个 401 的上游（应被吞掉不阻断整体）
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer badSrv.Close()

	// 一个 auto_sync=false 的渠道（应被跳过）
	autoOn := channel.Channel{Name: "auto-on", Type: protocol.ProviderOpenAI, BaseURL: okSrv.URL, Key: "k", AutoSync: true}
	autoOff := channel.Channel{Name: "auto-off", Type: protocol.ProviderOpenAI, BaseURL: badSrv.URL, Key: "k", AutoSync: false}
	bad := channel.Channel{Name: "bad", Type: protocol.ProviderOpenAI, BaseURL: badSrv.URL, Key: "k", AutoSync: true}
	for _, c := range []*channel.Channel{&autoOn, &autoOff, &bad} {
		if err := shared.DB.Create(c).Error; err != nil {
			t.Fatalf("建渠道失败: %v", err)
		}
	}

	synced, added := SyncAutoChannels()
	if synced != 1 {
		t.Fatalf("成功同步渠道数 = %d, want 1", synced)
	}
	if added != 2 {
		t.Fatalf("新增模型数 = %d, want 2", added)
	}

	// auto-on 渠道应有 2 个模型
	var count int64
	if err := shared.DB.Model(&Model{}).Where("channel_id = ?", autoOn.ID).Count(&count).Error; err != nil {
		t.Fatalf("查询模型失败: %v", err)
	}
	if count != 2 {
		t.Fatalf("auto-on 模型数 = %d, want 2", count)
	}
}
