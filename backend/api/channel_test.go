package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func setupCascadeTest(t *testing.T) {
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
	if err := shared.AutoMigrate(&channel.Channel{}, &model.Model{}, &group.Group{}, &group.GroupItem{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
}

func TestDeleteChannelCascades(t *testing.T) {
	setupCascadeTest(t)

	// 渠道 c1 及两个模型
	ch := channel.Channel{Name: "c1", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	m1 := model.Model{ChannelID: ch.ID, Name: "m1"}
	m2 := model.Model{ChannelID: ch.ID, Name: "m2"}
	if err := shared.DB.Create(&m1).Error; err != nil {
		t.Fatalf("建模型 m1 失败: %v", err)
	}
	if err := shared.DB.Create(&m2).Error; err != nil {
		t.Fatalf("建模型 m2 失败: %v", err)
	}

	// 分组 g1 引用 m1/m2，ActiveItemID 指向 it1
	g := group.Group{Name: "g1", Strategy: group.StrategyManual}
	if err := shared.DB.Create(&g).Error; err != nil {
		t.Fatalf("建分组失败: %v", err)
	}
	it1 := group.GroupItem{GroupID: g.ID, ModelID: m1.ID, Priority: 1}
	it2 := group.GroupItem{GroupID: g.ID, ModelID: m2.ID, Priority: 2}
	if err := shared.DB.Create(&it1).Error; err != nil {
		t.Fatalf("建分组项 it1 失败: %v", err)
	}
	if err := shared.DB.Create(&it2).Error; err != nil {
		t.Fatalf("建分组项 it2 失败: %v", err)
	}
	g.ActiveItemID = it1.ID
	if err := shared.DB.Save(&g).Error; err != nil {
		t.Fatalf("设 ActiveItemID 失败: %v", err)
	}

	// 另一渠道 c2 的模型 m3 及其分组项不应受影响
	ch2 := channel.Channel{Name: "c2", Type: protocol.ProviderOpenAI, BaseURL: "http://y", Key: "k"}
	if err := shared.DB.Create(&ch2).Error; err != nil {
		t.Fatalf("建渠道 c2 失败: %v", err)
	}
	m3 := model.Model{ChannelID: ch2.ID, Name: "m3"}
	if err := shared.DB.Create(&m3).Error; err != nil {
		t.Fatalf("建模型 m3 失败: %v", err)
	}
	it3 := group.GroupItem{GroupID: g.ID, ModelID: m3.ID, Priority: 3}
	if err := shared.DB.Create(&it3).Error; err != nil {
		t.Fatalf("建分组项 it3 失败: %v", err)
	}

	// 删除渠道 c1
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/channels/:id", deleteChannel)
	req := httptest.NewRequest(http.MethodDelete, "/channels/"+strconv.FormatInt(ch.ID, 10), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	// 断言：c1 的模型全部被删
	var modelCount int64
	if err := shared.DB.Model(&model.Model{}).Where("channel_id = ?", ch.ID).Count(&modelCount).Error; err != nil {
		t.Fatalf("查模型数失败: %v", err)
	}
	if modelCount != 0 {
		t.Errorf("删除渠道后残留模型数 = %d, want 0", modelCount)
	}

	// 断言：引用 m1/m2 的分组项全部被删
	var itemCount int64
	if err := shared.DB.Model(&group.GroupItem{}).Where("model_id IN ?", []int64{m1.ID, m2.ID}).Count(&itemCount).Error; err != nil {
		t.Fatalf("查分组项失败: %v", err)
	}
	if itemCount != 0 {
		t.Errorf("删除渠道后残留分组项 = %d, want 0", itemCount)
	}

	// 断言：ActiveItemID 指向被删项时被清空
	var gAfter group.Group
	if err := shared.DB.First(&gAfter, g.ID).Error; err != nil {
		t.Fatalf("查分组失败: %v", err)
	}
	if gAfter.ActiveItemID != 0 {
		t.Errorf("ActiveItemID = %d, want 0（指向的分组项已被删）", gAfter.ActiveItemID)
	}

	// 断言：c2 的模型与分组项完好
	if ok, _ := model.Exists(m3.ID); !ok {
		t.Errorf("其他渠道的模型 m3 被误删")
	}
	var it3Count int64
	if err := shared.DB.Model(&group.GroupItem{}).Where("id = ?", it3.ID).Count(&it3Count).Error; err != nil {
		t.Fatalf("查 it3 失败: %v", err)
	}
	if it3Count != 1 {
		t.Errorf("其他渠道的分组项 it3 被误删")
	}
}

func TestPreviewChannelModels(t *testing.T) {
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
		io.WriteString(w, `{"data":[{"id":"gpt-4o","object":"model"},{"id":"gpt-4o-mini","object":"model"}]}`)
	}))
	defer srv.Close()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/channels/preview-models", previewChannelModels)

	body := `{"type":"openai","base_url":"` + srv.URL + `","key":"test-key"}`
	req := httptest.NewRequest(http.MethodPost, "/channels/preview-models", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Models) != 2 || resp.Models[0] != "gpt-4o" || resp.Models[1] != "gpt-4o-mini" {
		t.Errorf("models = %v, want [gpt-4o gpt-4o-mini]", resp.Models)
	}
}

func TestPreviewChannelModelsInvalidType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/channels/preview-models", previewChannelModels)

	body := `{"type":"unknown","base_url":"https://example.com","key":"k"}`
	req := httptest.NewRequest(http.MethodPost, "/channels/preview-models", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}