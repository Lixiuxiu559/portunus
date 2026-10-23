package api

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestRemovedGroupStrategiesRejectedWithoutWrites(t *testing.T) {
	setupCascadeTest(t)
	m := model.Model{ChannelID: 1, Name: "m"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	g, err := group.Create(group.CreateRequest{Name: "existing", Strategy: group.StrategyManual, Items: []group.ItemAddRequest{{ModelID: m.ID}}, ActiveModelID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, strategy := range []string{"round_robin", "adaptive", "Round_robin", ""} {
		for _, active := range []string{"", `,"active_model_id":0`} {
			body := fmt.Sprintf(`{"name":"rejected","strategy":%q,"items":[{"model_id":%d}]%s}`, strategy, m.ID, active)
			groupRequest(t, "POST", "/groups", body, 400)
		}
		groupRequest(t, "PUT", fmt.Sprintf("/groups/%d", g.ID), fmt.Sprintf(`{"name":"must-not-change","strategy":%q,"active_item_id":0}`, strategy), 400)
		got, err := group.Get(g.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, g) {
			t.Fatalf("拒绝更新修改了分组: got=%+v want=%+v", got, g)
		}
		var groups, items int64
		if err := shared.DB.Model(&group.Group{}).Count(&groups).Error; err != nil {
			t.Fatal(err)
		}
		if err := shared.DB.Model(&group.GroupItem{}).Count(&items).Error; err != nil {
			t.Fatal(err)
		}
		if groups != 1 || items != 1 {
			t.Fatalf("拒绝创建产生写入: groups=%d items=%d", groups, items)
		}
	}
	for _, strategy := range []string{"failover", "manual"} {
		groupRequest(t, "PUT", fmt.Sprintf("/groups/%d", g.ID), fmt.Sprintf(`{"strategy":%q}`, strategy), 200)
		got, err := group.Get(g.ID)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Strategy) != strategy || got.ActiveItemID != g.ActiveItemID {
			t.Fatalf("切换合法策略不应丢失激活项: %+v", got)
		}
	}
}
