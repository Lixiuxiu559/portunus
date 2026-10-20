package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
)

func TestModelAPIRetiresPriceFieldsWithoutChangingStoredPrices(t *testing.T) {
	setupCascadeTest(t)
	ch := channel.Channel{Name: "legacy-channel", Type: protocol.ProviderOpenAI, BaseURL: "http://unused", Key: "unused"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	old := model.Model{ChannelID: ch.ID, Name: "legacy", Currency: model.CurrencyCNY, InputPrice: 1.25, OutputPrice: 9.5, CacheReadPrice: .4, CacheWritePrice: 2.3}
	if err := shared.DB.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	Register(r.Group("/api"))
	request := func(method, path, body string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Host = "localhost:3060"
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	check := func(response map[string]any) {
		t.Helper()
		for _, key := range []string{"currency", "input_price", "output_price", "cache_read_price", "cache_write_price"} {
			if _, ok := response[key]; ok {
				t.Fatalf("API暴露历史字段%s", key)
			}
		}
	}
	idPath := fmt.Sprintf("/api/models/%d", old.ID)
	check(request(http.MethodPut, idPath, `{"name":"renamed","currency":"USD","input_price":0,"output_price":999,"cache_read_price":null,"cache_write_price":"obsolete"}`, 200))
	check(request(http.MethodGet, idPath, "", 200))
	created := request(http.MethodPost, "/api/models", fmt.Sprintf(`{"channel_id":%d,"name":"gpt-4o","currency":"EUR","input_price":999,"output_price":999,"cache_read_price":999,"cache_write_price":999}`, ch.ID), 201)
	check(created)
	listed := request(http.MethodGet, "/api/models", "", 200)
	for _, row := range listed["data"].([]any) {
		check(row.(map[string]any))
	}
	var stored model.Model
	if err := shared.DB.First(&stored, old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Name != "renamed" || stored.Currency != old.Currency || stored.InputPrice != old.InputPrice || stored.OutputPrice != old.OutputPrice || stored.CacheReadPrice != old.CacheReadPrice || stored.CacheWritePrice != old.CacheWritePrice {
		t.Fatalf("旧字段被改写: %+v", stored)
	}
	var newModel model.Model
	if err := shared.DB.First(&newModel, int64(created["id"].(float64))).Error; err != nil {
		t.Fatal(err)
	}
	if newModel.InputPrice != 0 || newModel.OutputPrice != 0 || newModel.CacheReadPrice != 0 || newModel.CacheWritePrice != 0 {
		t.Fatalf("新模型仍在计价: %+v", newModel)
	}
}

func TestLogAPIRetiresCostsAndPreservesTokenDiagnostics(t *testing.T) {
	setupCascadeTest(t)
	if err := shared.InitLogDB(&shared.Config{}); err != nil {
		t.Fatal(err)
	}
	legacy := shared.Log{GroupName: "coding", Currency: "CNY", Cost: 18.75, InputToken: 10, OutputToken: 20, CacheReadToken: 30, CacheWriteToken: 40, Status: 500, ErrKind: "upstream_error", ErrMsg: "example failure", RequestID: "historical-request"}
	if err := shared.LogDB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	Register(r.Group("/api"))
	for _, path := range []string{"/api/logs?group_name=coding", "/api/logs/stat?group_name=coding"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Host = "localhost:3060"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		record := response
		if strings.Contains(path, "/stat") {
			if response["input_tokens"] != float64(10) || response["output_tokens"] != float64(20) || response["cache_read_tokens"] != float64(30) || response["cache_write_tokens"] != float64(40) {
				t.Fatalf("用量汇总失效: %v", response)
			}
		} else {
			record = response["data"].([]any)[0].(map[string]any)
			if record["request_id"] != "historical-request" || record["err_kind"] != "upstream_error" || record["input_token"] != float64(10) || record["cache_read_token"] != float64(30) {
				t.Fatalf("诊断信息丢失: %v", record)
			}
		}
		for _, field := range []string{"currency", "cost", "total_cost_usd", "total_cost_cny"} {
			if _, ok := record[field]; ok {
				t.Fatalf("%s 暴露退役字段%s", path, field)
			}
		}
	}
	var preserved shared.Log
	if err := shared.LogDB.First(&preserved, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.Cost != legacy.Cost || preserved.Currency != legacy.Currency {
		t.Fatalf("历史费用被改写: %+v", preserved)
	}
}
