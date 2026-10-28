package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/quota"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// setupDeletionTest 在独立临时库上迁移日志表，并让 shared.LogDB 指向测试库。
func setupDeletionTest(t *testing.T) {
	t.Helper()
	setupCascadeTest(t)
	previousLogDB := shared.LogDB
	shared.LogDB = shared.DB
	t.Cleanup(func() { shared.LogDB = previousLogDB })
	if err := shared.DB.AutoMigrate(&shared.Log{}); err != nil {
		t.Fatalf("迁移日志表失败: %v", err)
	}
}

// deletionTestRouter 注册模型与渠道路由；quotaService 非空时注入配额服务上下文。
func deletionTestRouter(quotaService *quota.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	if quotaService != nil {
		api.Use(func(c *gin.Context) {
			c.Set("quota_service", quotaService)
			c.Next()
		})
	}
	registerModelRoutes(api)
	registerChannelRoutes(api)
	return r
}

func doDeletionRequest(t *testing.T, r http.Handler, method, path string, want int) []byte {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	return w.Body.Bytes()
}

type impactGroupJSON struct {
	GroupID        int64   `json:"group_id"`
	Name           string  `json:"name"`
	Strategy       string  `json:"strategy"`
	RemovedItemIDs []int64 `json:"removed_item_ids"`
	ClearsActive   bool    `json:"clears_active"`
	BecomesEmpty   bool    `json:"becomes_empty"`
}

type impactResponseJSON struct {
	Groups         []impactGroupJSON `json:"groups"`
	ModelCount     *int              `json:"model_count"`
	HasQuotaSource *bool             `json:"has_quota_source"`
}

func decodeImpact(t *testing.T, body []byte) impactResponseJSON {
	t.Helper()
	var resp impactResponseJSON
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应失败: %v body=%s", err, body)
	}
	return resp
}

type deletionFixture struct {
	ch1, ch2 channel.Channel
	m1, m2   model.Model
	m3       model.Model
	g1, g2   group.Group
	it1      group.GroupItem
	it2      group.GroupItem
	it3      group.GroupItem
	it4      group.GroupItem
}

// seedDeletionFixture 建两渠道三模型；g1(manual, 激活 m1) 跨渠道引用 m1/m2/m3，
// g2(failover) 只引用 m2。用于预览与删除的一致性断言。
func seedDeletionFixture(t *testing.T) *deletionFixture {
	t.Helper()
	f := &deletionFixture{}
	f.ch1 = channel.Channel{Name: "c1", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "secret-c1"}
	f.ch2 = channel.Channel{Name: "c2", Type: protocol.ProviderOpenAI, BaseURL: "http://y", Key: "secret-c2"}
	for _, ch := range []*channel.Channel{&f.ch1, &f.ch2} {
		if err := shared.DB.Create(ch).Error; err != nil {
			t.Fatalf("建渠道失败: %v", err)
		}
	}
	f.m1 = model.Model{ChannelID: f.ch1.ID, Name: "m1"}
	f.m2 = model.Model{ChannelID: f.ch1.ID, Name: "m2"}
	f.m3 = model.Model{ChannelID: f.ch2.ID, Name: "m3"}
	for _, m := range []*model.Model{&f.m1, &f.m2, &f.m3} {
		if err := shared.DB.Create(m).Error; err != nil {
			t.Fatalf("建模型失败: %v", err)
		}
	}
	f.g1 = group.Group{Name: "g1", Strategy: group.StrategyManual}
	f.g2 = group.Group{Name: "g2", Strategy: group.StrategyFailover}
	for _, g := range []*group.Group{&f.g1, &f.g2} {
		if err := shared.DB.Create(g).Error; err != nil {
			t.Fatalf("建分组失败: %v", err)
		}
	}
	f.it1 = group.GroupItem{GroupID: f.g1.ID, ModelID: f.m1.ID, Priority: 0}
	f.it2 = group.GroupItem{GroupID: f.g1.ID, ModelID: f.m2.ID, Priority: 1}
	f.it3 = group.GroupItem{GroupID: f.g1.ID, ModelID: f.m3.ID, Priority: 2}
	f.it4 = group.GroupItem{GroupID: f.g2.ID, ModelID: f.m2.ID, Priority: 0}
	for _, it := range []*group.GroupItem{&f.it1, &f.it2, &f.it3, &f.it4} {
		if err := shared.DB.Create(it).Error; err != nil {
			t.Fatalf("建分组项失败: %v", err)
		}
	}
	if err := shared.DB.Model(&f.g1).Update("active_item_id", f.it1.ID).Error; err != nil {
		t.Fatalf("设置激活项失败: %v", err)
	}
	f.g1.ActiveItemID = f.it1.ID
	return f
}

func seedQuotaSource(t *testing.T, channelID int64) {
	t.Helper()
	if err := shared.AutoMigrate(&quota.Source{}, &quota.State{}); err != nil {
		t.Fatalf("迁移配额表失败: %v", err)
	}
	src := quota.Source{ChannelID: channelID, Name: "balance", AdapterID: "custom", SchemaVersion: 1, ConfigRevision: 1, Enabled: true}
	if err := shared.DB.Create(&src).Error; err != nil {
		t.Fatalf("建余额来源失败: %v", err)
	}
}

func seedDeletionLog(t *testing.T) shared.Log {
	t.Helper()
	row := shared.Log{GroupName: "g1", ChannelID: 1, ModelName: "m1", Status: 200, Success: true, InputToken: 10, OutputToken: 20}
	if err := shared.LogDB.Create(&row).Error; err != nil {
		t.Fatalf("写历史日志失败: %v", err)
	}
	return row
}

func countRows(t *testing.T, entity any, query string, args ...any) int64 {
	t.Helper()
	var count int64
	q := shared.DB.Model(entity)
	if query != "" {
		q = q.Where(query, args...)
	}
	if err := q.Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	return count
}

func loadGroup(t *testing.T, id int64) group.Group {
	t.Helper()
	var g group.Group
	if err := shared.DB.First(&g, id).Error; err != nil {
		t.Fatalf("查分组失败: %v", err)
	}
	return g
}

func TestChannelDeletionImpactPreviewIsReadOnly(t *testing.T) {
	setupDeletionTest(t)
	f := seedDeletionFixture(t)
	seedQuotaSource(t, f.ch1.ID)
	logRow := seedDeletionLog(t)
	// 预览与删除使用同一判断：生产恒注入配额服务，测试也按同样接线，避免预览承诺删除不执行的动作。
	r := deletionTestRouter(quota.NewService(shared.DB, nil, func(*gorm.DB, int64) (string, error) { return "", nil }))

	body := doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/channels/%d/deletion-impact", f.ch1.ID), http.StatusOK)
	got := decodeImpact(t, body)

	if len(got.Groups) != 2 {
		t.Fatalf("受影响分组数 = %d, want 2: %s", len(got.Groups), body)
	}
	g1 := got.Groups[0]
	if g1.GroupID != f.g1.ID || g1.Name != "g1" || g1.Strategy != string(group.StrategyManual) {
		t.Fatalf("g1 元数据不正确: %+v", g1)
	}
	if len(g1.RemovedItemIDs) != 2 || g1.RemovedItemIDs[0] != f.it1.ID || g1.RemovedItemIDs[1] != f.it2.ID {
		t.Fatalf("g1 将移除成员不正确: %+v", g1.RemovedItemIDs)
	}
	if !g1.ClearsActive || g1.BecomesEmpty {
		t.Fatalf("g1 应清除激活且保留 m3 成员: %+v", g1)
	}
	g2 := got.Groups[1]
	if g2.GroupID != f.g2.ID || len(g2.RemovedItemIDs) != 1 || g2.RemovedItemIDs[0] != f.it4.ID {
		t.Fatalf("g2 影响不正确: %+v", g2)
	}
	if g2.ClearsActive || !g2.BecomesEmpty {
		t.Fatalf("g2 应变成空组且无激活项: %+v", g2)
	}
	if got.ModelCount == nil || *got.ModelCount != 2 {
		t.Fatalf("model_count = %v, want 2", got.ModelCount)
	}
	if got.HasQuotaSource == nil || !*got.HasQuotaSource {
		t.Fatalf("has_quota_source = %v, want true", got.HasQuotaSource)
	}
	if strings.Contains(string(body), "secret") || strings.Contains(string(body), `"key"`) {
		t.Fatalf("预览响应泄露凭据: %s", body)
	}

	// 只读：逐项核对数据库行数与激活项未变。
	if n := countRows(t, &model.Model{}, "channel_id = ?", f.ch1.ID); n != 2 {
		t.Fatalf("预览后 c1 模型数 = %d, want 2", n)
	}
	if n := countRows(t, &group.GroupItem{}, ""); n != 4 {
		t.Fatalf("预览后分组项数 = %d, want 4", n)
	}
	if g := loadGroup(t, f.g1.ID); g.ActiveItemID != f.it1.ID {
		t.Fatalf("预览后 g1.ActiveItemID = %d, want %d", g.ActiveItemID, f.it1.ID)
	}
	if g := loadGroup(t, f.g2.ID); g.ActiveItemID != 0 {
		t.Fatalf("预览后 g2.ActiveItemID = %d, want 0", g.ActiveItemID)
	}
	if n := countRows(t, &quota.Source{}, "channel_id = ?", f.ch1.ID); n != 1 {
		t.Fatalf("预览后余额来源数 = %d, want 1", n)
	}
	var logAfter shared.Log
	if err := shared.LogDB.First(&logAfter, logRow.ID).Error; err != nil {
		t.Fatalf("预览后历史日志丢失: %v", err)
	}
	if logAfter.InputToken != logRow.InputToken || logAfter.ModelName != logRow.ModelName {
		t.Fatalf("预览后历史日志被修改: %+v", logAfter)
	}
}

func TestModelDeletionImpactPreviewIsReadOnly(t *testing.T) {
	setupDeletionTest(t)
	ch := channel.Channel{Name: "c", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	m1 := model.Model{ChannelID: ch.ID, Name: "referenced"}
	m3 := model.Model{ChannelID: ch.ID, Name: "unreferenced"}
	for _, m := range []*model.Model{&m1, &m3} {
		if err := shared.DB.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
	g := group.Group{Name: "only", Strategy: group.StrategyManual}
	if err := shared.DB.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	it := group.GroupItem{GroupID: g.ID, ModelID: m1.ID, Priority: 0}
	if err := shared.DB.Create(&it).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Model(&g).Update("active_item_id", it.ID).Error; err != nil {
		t.Fatal(err)
	}
	r := deletionTestRouter(nil)

	body := doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/models/%d/deletion-impact", m1.ID), http.StatusOK)
	got := decodeImpact(t, body)
	if len(got.Groups) != 1 || got.Groups[0].GroupID != g.ID {
		t.Fatalf("模型预览分组不正确: %s", body)
	}
	if len(got.Groups[0].RemovedItemIDs) != 1 || got.Groups[0].RemovedItemIDs[0] != it.ID {
		t.Fatalf("模型预览移除成员不正确: %+v", got.Groups[0])
	}
	if !got.Groups[0].ClearsActive || !got.Groups[0].BecomesEmpty {
		t.Fatalf("唯一成员且为激活项时应清激活并变空组: %+v", got.Groups[0])
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["model_count"]; ok {
		t.Fatalf("模型预览不应返回 model_count: %s", body)
	}

	emptyBody := doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/models/%d/deletion-impact", m3.ID), http.StatusOK)
	if !strings.Contains(string(emptyBody), `"groups":[]`) {
		t.Fatalf("无引用时 groups 应为空数组而非 null: %s", emptyBody)
	}

	// 只读：模型、分组项与激活项保持原样。
	if ok, err := model.Exists(m1.ID); err != nil || !ok {
		t.Fatalf("预览后模型 m1 丢失: ok=%v err=%v", ok, err)
	}
	if n := countRows(t, &group.GroupItem{}, ""); n != 1 {
		t.Fatalf("预览后分组项数 = %d, want 1", n)
	}
	if after := loadGroup(t, g.ID); after.ActiveItemID != it.ID {
		t.Fatalf("预览后激活项被修改: %d", after.ActiveItemID)
	}
}

func TestDeleteModelCascadesReferencingItems(t *testing.T) {
	setupDeletionTest(t)
	f := seedDeletionFixture(t)
	logRow := seedDeletionLog(t)
	r := deletionTestRouter(nil)

	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", f.m1.ID), http.StatusOK)

	if ok, err := model.Exists(f.m1.ID); err != nil || ok {
		t.Fatalf("m1 应被删除: ok=%v err=%v", ok, err)
	}
	if n := countRows(t, &group.GroupItem{}, "id = ?", f.it1.ID); n != 0 {
		t.Fatalf("引用 m1 的分组项 it1 残留 %d 行", n)
	}
	if n := countRows(t, &group.GroupItem{}, "id = ?", f.it2.ID); n != 1 {
		t.Fatalf("it2 被误删")
	}
	if n := countRows(t, &group.GroupItem{}, "id = ?", f.it3.ID); n != 1 {
		t.Fatalf("it3 被误删")
	}
	var it2, it3 group.GroupItem
	if err := shared.DB.First(&it2, f.it2.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.First(&it3, f.it3.ID).Error; err != nil {
		t.Fatal(err)
	}
	if it2.Priority != 1 || it3.Priority != 2 {
		t.Fatalf("剩余成员优先级被改动: it2=%d it3=%d", it2.Priority, it3.Priority)
	}
	if g := loadGroup(t, f.g1.ID); g.ActiveItemID != 0 {
		t.Fatalf("g1.ActiveItemID = %d, want 0", g.ActiveItemID)
	}
	if n := countRows(t, &group.Group{}, ""); n != 2 {
		t.Fatalf("分组数 = %d, want 2（分组必须保留）", n)
	}
	if ok, _ := model.Exists(f.m2.ID); !ok {
		t.Fatalf("m2 被误删")
	}
	if ok, _ := model.Exists(f.m3.ID); !ok {
		t.Fatalf("其他渠道模型 m3 被误删")
	}
	if n := countRows(t, &group.GroupItem{}, "group_id = ?", f.g2.ID); n != 1 {
		t.Fatalf("g2 成员数 = %d, want 1", n)
	}
	var logAfter shared.Log
	if err := shared.LogDB.First(&logAfter, logRow.ID).Error; err != nil {
		t.Fatalf("历史日志被删除: %v", err)
	}
	if logAfter.InputToken != logRow.InputToken || logAfter.OutputToken != logRow.OutputToken {
		t.Fatalf("历史日志被修改: %+v", logAfter)
	}
}

func TestDeleteModelKeepsEmptyGroup(t *testing.T) {
	setupDeletionTest(t)
	ch := channel.Channel{Name: "c", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	m := model.Model{ChannelID: ch.ID, Name: "only"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	g := group.Group{Name: "solo", Strategy: group.StrategyManual}
	if err := shared.DB.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	it := group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: 0}
	if err := shared.DB.Create(&it).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Model(&g).Update("active_item_id", it.ID).Error; err != nil {
		t.Fatal(err)
	}
	r := deletionTestRouter(nil)

	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", m.ID), http.StatusOK)

	after := loadGroup(t, g.ID)
	if after.ActiveItemID != 0 || after.Name != "solo" {
		t.Fatalf("空组状态不正确: %+v", after)
	}
	if n := countRows(t, &group.GroupItem{}, "group_id = ?", g.ID); n != 0 {
		t.Fatalf("分组项数 = %d, want 0", n)
	}
	loaded, err := group.GetByName("solo")
	if err != nil {
		t.Fatalf("分组应保留: %v", err)
	}
	if len(loaded.Items) != 0 {
		t.Fatalf("分组应为空: %+v", loaded.Items)
	}
	if _, err := router.Resolve(loaded); !errors.Is(err, router.ErrEmptyGroup) {
		t.Fatalf("空组 Resolve 应返回 ErrEmptyGroup, got %v", err)
	}
}

func TestDeleteModelCascadesAcrossMultipleGroups(t *testing.T) {
	setupDeletionTest(t)
	ch := channel.Channel{Name: "c", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	target := model.Model{ChannelID: ch.ID, Name: "target"}
	kept := model.Model{ChannelID: ch.ID, Name: "kept"}
	for _, m := range []*model.Model{&target, &kept} {
		if err := shared.DB.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
	manual := group.Group{Name: "manual", Strategy: group.StrategyManual}
	failover := group.Group{Name: "failover", Strategy: group.StrategyFailover}
	for _, g := range []*group.Group{&manual, &failover} {
		if err := shared.DB.Create(g).Error; err != nil {
			t.Fatal(err)
		}
	}
	onlyItem := group.GroupItem{GroupID: manual.ID, ModelID: target.ID, Priority: 0}
	removedItem := group.GroupItem{GroupID: failover.ID, ModelID: target.ID, Priority: 0}
	keptItem := group.GroupItem{GroupID: failover.ID, ModelID: kept.ID, Priority: 1}
	for _, item := range []*group.GroupItem{&onlyItem, &removedItem, &keptItem} {
		if err := shared.DB.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := shared.DB.Model(&manual).Update("active_item_id", onlyItem.ID).Error; err != nil {
		t.Fatal(err)
	}
	r := deletionTestRouter(nil)

	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", target.ID), http.StatusOK)

	if n := countRows(t, &group.GroupItem{}, "model_id = ?", target.ID); n != 0 {
		t.Fatalf("跨组清理后仍残留 %d 个引用目标模型的分组项", n)
	}
	if after := loadGroup(t, manual.ID); after.ActiveItemID != 0 {
		t.Fatalf("manual.ActiveItemID = %d, want 0", after.ActiveItemID)
	}
	if after := loadGroup(t, failover.ID); after.ActiveItemID != 0 {
		t.Fatalf("failover.ActiveItemID = %d, want 0", after.ActiveItemID)
	}
	if n := countRows(t, &group.GroupItem{}, "id = ?", keptItem.ID); n != 1 {
		t.Fatalf("其他成员被误删")
	}
	var keptAfter group.GroupItem
	if err := shared.DB.First(&keptAfter, keptItem.ID).Error; err != nil {
		t.Fatal(err)
	}
	if keptAfter.Priority != 1 {
		t.Fatalf("保留成员优先级被改动: %d", keptAfter.Priority)
	}
	if n := countRows(t, &group.Group{}, ""); n != 2 {
		t.Fatalf("分组数 = %d, want 2", n)
	}
}

func TestDeleteModelRollsBackWhenGroupItemCleanupFails(t *testing.T) {
	setupDeletionTest(t)
	f := seedDeletionFixture(t)
	logRow := seedDeletionLog(t)
	if err := shared.DB.Exec(`CREATE TRIGGER reject_group_item_delete BEFORE DELETE ON group_items BEGIN SELECT RAISE(ABORT, 'deny'); END`).Error; err != nil {
		t.Fatal(err)
	}
	r := deletionTestRouter(nil)

	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", f.m1.ID), http.StatusInternalServerError)

	if ok, _ := model.Exists(f.m1.ID); !ok {
		t.Fatalf("回滚后 m1 应保留")
	}
	for _, item := range []group.GroupItem{f.it1, f.it2, f.it3, f.it4} {
		if n := countRows(t, &group.GroupItem{}, "id = ?", item.ID); n != 1 {
			t.Fatalf("回滚后分组项 %d 残留 %d 行", item.ID, n)
		}
	}
	if g := loadGroup(t, f.g1.ID); g.ActiveItemID != f.it1.ID {
		t.Fatalf("回滚后 g1.ActiveItemID = %d, want %d", g.ActiveItemID, f.it1.ID)
	}
	var logAfter shared.Log
	if err := shared.LogDB.First(&logAfter, logRow.ID).Error; err != nil {
		t.Fatalf("回滚后历史日志丢失: %v", err)
	}
}

func TestDeleteChannelRollsBackWhenModelCleanupFails(t *testing.T) {
	setupDeletionTest(t)
	f := seedDeletionFixture(t)
	seedQuotaSource(t, f.ch1.ID)
	logRow := seedDeletionLog(t)
	if err := shared.DB.Exec(`CREATE TRIGGER reject_model_delete BEFORE DELETE ON models BEGIN SELECT RAISE(ABORT, 'deny'); END`).Error; err != nil {
		t.Fatal(err)
	}
	svc := quota.NewService(shared.DB, nil, func(*gorm.DB, int64) (string, error) { return "", nil })
	r := deletionTestRouter(svc)

	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/channels/%d", f.ch1.ID), http.StatusInternalServerError)

	if ok, _ := channel.Exists(f.ch1.ID); !ok {
		t.Fatalf("回滚后渠道 c1 应保留")
	}
	for _, m := range []model.Model{f.m1, f.m2} {
		if ok, _ := model.Exists(m.ID); !ok {
			t.Fatalf("回滚后模型 %d 应保留", m.ID)
		}
	}
	for _, item := range []group.GroupItem{f.it1, f.it2, f.it4} {
		if n := countRows(t, &group.GroupItem{}, "id = ?", item.ID); n != 1 {
			t.Fatalf("回滚后分组项 %d 残留 %d 行", item.ID, n)
		}
	}
	if g := loadGroup(t, f.g1.ID); g.ActiveItemID != f.it1.ID {
		t.Fatalf("回滚后 g1.ActiveItemID = %d, want %d", g.ActiveItemID, f.it1.ID)
	}
	if n := countRows(t, &quota.Source{}, "channel_id = ?", f.ch1.ID); n != 1 {
		t.Fatalf("回滚后余额来源数 = %d, want 1", n)
	}
	var logAfter shared.Log
	if err := shared.LogDB.First(&logAfter, logRow.ID).Error; err != nil {
		t.Fatalf("回滚后历史日志丢失: %v", err)
	}
}

func TestDeletionBoundaries(t *testing.T) {
	setupDeletionTest(t)
	ch := channel.Channel{Name: "c", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	m := model.Model{ChannelID: ch.ID, Name: "free"}
	other := model.Model{ChannelID: ch.ID, Name: "other"}
	for _, item := range []*model.Model{&m, &other} {
		if err := shared.DB.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := deletionTestRouter(nil)

	// 无引用模型可正常删除，其他模型不变。
	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", m.ID), http.StatusOK)
	if ok, _ := model.Exists(m.ID); ok {
		t.Fatalf("无引用模型应被删除")
	}
	if ok, _ := model.Exists(other.ID); !ok {
		t.Fatalf("其他模型被误删")
	}

	// 不存在目标返回 404，非法 ID 返回 400。
	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", m.ID), http.StatusNotFound)
	doDeletionRequest(t, r, http.MethodDelete, "/api/models/abc", http.StatusBadRequest)
	doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/models/%d/deletion-impact", m.ID), http.StatusNotFound)
	doDeletionRequest(t, r, http.MethodGet, "/api/models/abc/deletion-impact", http.StatusBadRequest)
	doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/channels/%d/deletion-impact", ch.ID+999), http.StatusNotFound)
	doDeletionRequest(t, r, http.MethodGet, "/api/channels/abc/deletion-impact", http.StatusBadRequest)
}

func TestDeleteModelPreservesFailoverOrder(t *testing.T) {
	setupDeletionTest(t)
	ch := channel.Channel{Name: "c", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	m1 := model.Model{ChannelID: ch.ID, Name: "m1"}
	m2 := model.Model{ChannelID: ch.ID, Name: "m2"}
	m3 := model.Model{ChannelID: ch.ID, Name: "m3"}
	for _, m := range []*model.Model{&m1, &m2, &m3} {
		if err := shared.DB.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
	g := group.Group{Name: "fo", Strategy: group.StrategyFailover}
	if err := shared.DB.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	for i, m := range []model.Model{m1, m2, m3} {
		if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: i}).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := deletionTestRouter(nil)

	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", m1.ID), http.StatusOK)

	loaded, err := group.GetByName("fo")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := router.Resolve(loaded)
	if err != nil {
		t.Fatalf("删除后 Resolve 失败: %v", err)
	}
	if len(targets) != 2 || targets[0].Model.Name != "m2" || targets[1].Model.Name != "m3" {
		t.Fatalf("failover 剩余顺序不正确: %+v", targets)
	}
	if loaded.Items[0].Priority != 1 || loaded.Items[1].Priority != 2 {
		t.Fatalf("不应自动重排优先级: %+v", loaded.Items)
	}
}

func TestDeletionImpactPreviewWithoutReferences(t *testing.T) {
	setupDeletionTest(t)
	ch := channel.Channel{Name: "empty", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	m := model.Model{ChannelID: ch.ID, Name: "free"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	r := deletionTestRouter(nil)

	body := doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/channels/%d/deletion-impact", ch.ID), http.StatusOK)
	got := decodeImpact(t, body)
	if got.Groups == nil || len(got.Groups) != 0 {
		t.Fatalf("无成员渠道影响应为空数组: %s", body)
	}
	if !strings.Contains(string(body), `"groups":[]`) {
		t.Fatalf("groups 不能序列化为 null: %s", body)
	}
	if got.ModelCount == nil || *got.ModelCount != 1 {
		t.Fatalf("model_count = %v, want 1", got.ModelCount)
	}
	if got.HasQuotaSource == nil || *got.HasQuotaSource {
		t.Fatalf("未启用配额时 has_quota_source 应为 false: %v", got.HasQuotaSource)
	}

	// 配额表已存在但没有该渠道来源时仍为 false。
	if err := shared.AutoMigrate(&quota.Source{}, &quota.State{}); err != nil {
		t.Fatal(err)
	}
	body = doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/channels/%d/deletion-impact", ch.ID), http.StatusOK)
	got = decodeImpact(t, body)
	if got.HasQuotaSource == nil || *got.HasQuotaSource {
		t.Fatalf("配额表存在但无来源时 has_quota_source 应为 false: %v", got.HasQuotaSource)
	}

	// 无引用预览后仍可继续删除流程。
	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", m.ID), http.StatusOK)
}

func TestDeletionImpactCountsDanglingHistoricalItems(t *testing.T) {
	setupDeletionTest(t)
	ch := channel.Channel{Name: "c", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	m := model.Model{ChannelID: ch.ID, Name: "m"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	g := group.Group{Name: "mixed", Strategy: group.StrategyManual}
	if err := shared.DB.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	live := group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: 0}
	dangling := group.GroupItem{GroupID: g.ID, ModelID: 99999, Priority: 1} // 引用已缺失模型
	for _, item := range []*group.GroupItem{&live, &dangling} {
		if err := shared.DB.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := shared.DB.Model(&g).Update("active_item_id", live.ID).Error; err != nil {
		t.Fatal(err)
	}
	r := deletionTestRouter(nil)

	body := doDeletionRequest(t, r, http.MethodGet, fmt.Sprintf("/api/models/%d/deletion-impact", m.ID), http.StatusOK)
	got := decodeImpact(t, body)
	if len(got.Groups) != 1 {
		t.Fatalf("受影响分组数 = %d, want 1: %s", len(got.Groups), body)
	}
	if len(got.Groups[0].RemovedItemIDs) != 1 || got.Groups[0].RemovedItemIDs[0] != live.ID {
		t.Fatalf("移除成员不正确: %+v", got.Groups[0])
	}
	if !got.Groups[0].ClearsActive {
		t.Fatalf("激活项应被清除: %+v", got.Groups[0])
	}
	if got.Groups[0].BecomesEmpty {
		t.Fatalf("仍有 dangling 历史项时不应判定为空组: %+v", got.Groups[0])
	}

	doDeletionRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/models/%d", m.ID), http.StatusOK)
	if n := countRows(t, &group.GroupItem{}, "id = ?", dangling.ID); n != 1 {
		t.Fatalf("dangling 历史项被误删")
	}
	if gAfter := loadGroup(t, g.ID); gAfter.ActiveItemID != 0 {
		t.Fatalf("删除后 ActiveItemID = %d, want 0", gAfter.ActiveItemID)
	}
}
