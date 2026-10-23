package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func groupRequest(t *testing.T, method, path, body string, want int) []byte {
	t.Helper()
	r := gin.New()
	registerGroupRoutes(r.Group("/groups-api"))
	req := httptest.NewRequest(method, "/groups-api"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestGroupCreationIncludesMemberMetadataBeyondModelPage(t *testing.T) {
	setupCascadeTest(t)
	// 模型 ID 大于 1000 且不在首次 1000 条加载内；同名成员必须区分渠道。
	models := make([]model.Model, 1002)
	for i := range models {
		models[i] = model.Model{ChannelID: 11, Name: fmt.Sprintf("filler-%04d", i)}
	}
	models[1000].Name = "shared-name"
	models[1001].Name = "shared-name"
	models[1001].ChannelID = 22
	if err := shared.DB.CreateInBatches(&models, 100).Error; err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"batch","strategy":"manual","active_model_id":%d,"items":[{"model_id":%d,"priority":9},{"model_id":%d,"priority":2}]}`, models[1000].ID, models[1000].ID, models[1001].ID)
	created := groupRequest(t, "POST", "/groups", body, 201)
	var result group.Response
	if err := json.Unmarshal(created, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].ModelID != models[1001].ID || result.Items[1].ModelID != models[1000].ID || result.ActiveItemID != result.Items[1].ID {
		t.Fatalf("顺序或激活项不正确: %+v", result)
	}
	checkMetadata := func(data []byte, list bool) {
		t.Helper()
		var row map[string]any
		if list {
			var rows []map[string]any
			if err := json.Unmarshal(data, &rows); err != nil {
				t.Fatal(err)
			}
			row = rows[0]
		} else if err := json.Unmarshal(data, &row); err != nil {
			t.Fatal(err)
		}
		items := row["items"].([]any)
		for i, channelID := range []float64{22, 11} {
			item := items[i].(map[string]any)
			if item["model_name"] != "shared-name" || item["channel_id"] != channelID {
				t.Fatalf("成员元数据缺失或混淆: %v", item)
			}
		}
	}
	checkMetadata(created, false)
	path := fmt.Sprintf("/groups/%d", result.ID)
	checkMetadata(groupRequest(t, "GET", path, "", 200), false)
	queryCount := 0
	callbackName := "test:group_metadata_queries"
	if err := shared.DB.Callback().Query().Before("gorm:query").Register(callbackName, func(_ *gorm.DB) { queryCount++ }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shared.DB.Callback().Query().Remove(callbackName) })
	checkMetadata(groupRequest(t, "GET", "/groups", "", 200), true)
	if queryCount != 2 {
		t.Fatalf("加载分组和成员应批量执行两次查询，实际 %d 次", queryCount)
	}
	shared.DB.Callback().Query().Remove(callbackName)
	checkMetadata(groupRequest(t, "PUT", path, `{"name":"renamed"}`, 200), false)
	if shared.DB.Migrator().HasColumn(&group.GroupItem{}, "model_name") || shared.DB.Migrator().HasColumn(&group.GroupItem{}, "channel_id") {
		t.Fatal("派生元数据不应新增数据库列")
	}
}

func TestGroupCreationActiveModelValidationAndAtomicity(t *testing.T) {
	setupCascadeTest(t)
	m := model.Model{ChannelID: 1, Name: "m"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, strategy string
		active         int64
		items          string
		want           int
	}{
		{"outside-items", "manual", m.ID + 1, fmt.Sprintf(`[{"model_id":%d}]`, m.ID), 400},
		{"empty-items", "manual", m.ID, `[]`, 400},
		{"negative", "manual", -1, fmt.Sprintf(`[{"model_id":%d}]`, m.ID), 400},
		{"round-robin", "round_robin", m.ID, fmt.Sprintf(`[{"model_id":%d}]`, m.ID), 400},
		{"failover", "failover", m.ID, fmt.Sprintf(`[{"model_id":%d}]`, m.ID), 400},
		{"missing-model", "manual", m.ID + 1, fmt.Sprintf(`[{"model_id":%d}]`, m.ID+1), 400},
		{"duplicate-rollback", "manual", m.ID, fmt.Sprintf(`[{"model_id":%d},{"model_id":%d}]`, m.ID, m.ID), 409},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":%q,"strategy":%q,"active_model_id":%d,"items":%s}`, tc.name, tc.strategy, tc.active, tc.items)
			groupRequest(t, "POST", "/groups", body, tc.want)
			var groups, items int64
			if err := shared.DB.Model(&group.Group{}).Count(&groups).Error; err != nil {
				t.Fatal(err)
			}
			if err := shared.DB.Model(&group.GroupItem{}).Count(&items).Error; err != nil {
				t.Fatal(err)
			}
			if groups != 0 || items != 0 {
				t.Fatalf("失败请求留下半成品: groups=%d items=%d", groups, items)
			}
		})
	}
	// 旧客户端省略 active_model_id，保持未激活；非 manual 显式 0 同样兼容。
	for _, strategy := range []string{"manual", "failover"} {
		field := ""
		if strategy != "manual" {
			field = `,"active_model_id":0`
		}
		data := groupRequest(t, "POST", "/groups", fmt.Sprintf(`{"name":%q,"strategy":%q,"items":[{"model_id":%d}]%s}`, strategy, strategy, m.ID, field), 201)
		var created group.Response
		if err := json.Unmarshal(data, &created); err != nil {
			t.Fatal(err)
		}
		if created.ActiveItemID != 0 || len(created.Items) != 1 {
			t.Fatalf("旧请求兼容性丢失: %+v", created)
		}
	}
}

func TestGroupCreationRollsBackWhenSettingActiveItemFails(t *testing.T) {
	setupCascadeTest(t)
	m := model.Model{ChannelID: 1, Name: "m"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	// 模拟创建成员后的激活写入失败，所有写入必须在同一事务中撤销。
	if err := shared.DB.Exec(`CREATE TRIGGER reject_group_activation BEFORE UPDATE OF active_item_id ON groups WHEN NEW.active_item_id != 0 BEGIN SELECT RAISE(ABORT, 'activation failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	groupRequest(t, "POST", "/groups", fmt.Sprintf(`{"name":"activation-failure","strategy":"manual","active_model_id":%d,"items":[{"model_id":%d}]}`, m.ID, m.ID), 500)
	var groups, items int64
	if err := shared.DB.Model(&group.Group{}).Count(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Model(&group.GroupItem{}).Count(&items).Error; err != nil {
		t.Fatal(err)
	}
	if groups != 0 || items != 0 {
		t.Fatalf("激活失败留下半成品: groups=%d items=%d", groups, items)
	}
}
