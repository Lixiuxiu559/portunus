// 真实管理 API 与 renderer 的隔离测试，全部数据为合成数据。
package main

import (
	"encoding/json"
	"fmt"
	"github.com/Lixiuxiu559/portunus/backend/api"
	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"log"
	"net/http"
	"sync"
	"time"
)

func main() {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	shared.DB = db
	shared.LogDB = db
	if err = db.AutoMigrate(&channel.Channel{}); err != nil {
		panic(err)
	}
	if err = shared.InitLogDB(&shared.Config{}); err != nil {
		panic(err)
	}
	for _, name := range []string{"验证渠道 A", "验证渠道 B"} {
		db.Create(&channel.Channel{Name: name, Type: "openai", BaseURL: "http://127.0.0.1:1"})
	}
	addLog := func(req string, ch int64, status int) {
		row := shared.Log{RequestID: req, GroupName: "coding", ChannelID: ch, ModelName: "shared-model", ClientProtocol: "openai_responses", Success: status == 200, Status: status, CreatedAt: time.Now(), InputToken: 7, OutputToken: 1}
		if status != 200 {
			row.ErrKind = "upstream_error"
			row.ErrMsg = "合成错误"
			row.InputToken = 0
			row.OutputToken = 0
		}
		if err = db.Create(&row).Error; err != nil {
			panic(err)
		}
	}
	addAttempt := func(req string, seq, relay int, reason string, ch int64, status int) {
		field := "max_completion_tokens"
		if reason == "initial" || reason == "failover" {
			field = "max_tokens"
		}
		row := shared.UpstreamAttempt{RequestID: req, Sequence: seq, RelayAttempt: relay, Reason: reason, GroupName: "coding", ChannelID: ch, ModelName: "shared-model", ClientProtocol: "openai_responses", UpstreamProtocol: "openai", BudgetField: field, Status: status, Success: status == 200, StartedAt: time.Now(), DurationMs: 15}
		if status != 200 {
			row.ErrKind = "upstream_error"
			row.ErrMsg = "合成失败\n不包含真实业务数据 <script>window.fixtureUnexpected=true</script>"
		}
		if err = shared.WriteUpstreamAttempt(&row); err != nil {
			panic(err)
		}
	}
	for i := 0; i < 25; i++ {
		addLog(fmt.Sprintf("filler-%02d", i), 1, 200)
	}
	addLog("", 0, 200)
	addLog("", 0, 503)
	addLog("old", 99, 200)
	addLog("gap", 1, 503)
	addLog("gap", 2, 200)
	addAttempt("gap", 1, 1, "initial", 1, 503)
	addAttempt("gap", 3, 2, "failover", 2, 200)
	for i := 1; i <= 25; i++ {
		status := 503
		if i == 25 {
			status = 200
		}
		addLog("long", 1, status)
		reason := "retry"
		if i == 1 {
			reason = "initial"
		}
		addAttempt("long", i, i, reason, 1, status)
	}
	req := "mixed_%+&=中"
	addLog(req, 1, 503)
	addLog(req, 1, 503)
	addLog(req, 2, 200)
	addAttempt(req, 1, 1, "initial", 1, 400)
	addAttempt(req, 2, 1, "budget_compat", 1, 503)
	addAttempt(req, 3, 2, "retry", 1, 503)
	addAttempt(req, 4, 3, "failover", 2, 200)
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	api.Register(r.Group("/api"))
	var mu sync.Mutex
	requests := []string{}
	failNext := false
	mux := http.NewServeMux()
	mux.HandleFunc("/__fixture/requests", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(requests)
	})
	mux.HandleFunc("/__fixture/fail-attempt", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		failNext = true
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		requests = append(requests, req.URL.String())
		fail := failNext && req.URL.Path == "/api/logs/attempts"
		if fail {
			failNext = false
		}
		mu.Unlock()
		if fail {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"合成尝试查询失败"}`)
			return
		}
		r.ServeHTTP(w, req)
	})
	mux.Handle("/", http.FileServer(http.Dir("web/dist/renderer")))
	log.Print("request association fixture http://127.0.0.1:13474")
	log.Fatal(http.ListenAndServe("127.0.0.1:13474", mux))
}
