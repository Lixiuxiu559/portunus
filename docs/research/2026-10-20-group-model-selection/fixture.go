// 本地隔离验证服务：仅内存数据库，绝不读取正式或 Dev 数据。
package main

import (
	"encoding/json"
	"fmt"
	"github.com/Lixiuxiu559/portunus/backend/api"
	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
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
	if err = db.AutoMigrate(&channel.Channel{}, &model.Model{}, &group.Group{}, &group.GroupItem{}); err != nil {
		panic(err)
	}
	for _, name := range []string{"验证渠道 A", "验证渠道 B"} {
		db.Create(&channel.Channel{Name: name, Type: "openai", BaseURL: "http://127.0.0.1:1", AutoSync: false})
	}
	models := make([]model.Model, 1102)
	for i := range models {
		models[i] = model.Model{ChannelID: 1, Name: fmt.Sprintf("catalog-%04d", i)}
	}
	models[1100] = model.Model{ChannelID: 1, Name: "zz-shared"}
	models[1101] = model.Model{ChannelID: 2, Name: "zz-shared"}
	if err = db.CreateInBatches(&models, 100).Error; err != nil {
		panic(err)
	}
	if _, err = group.Create(group.CreateRequest{Name: "目录外成员", Strategy: group.StrategyManual, ActiveModelID: models[1099].ID, Items: []group.ItemAddRequest{{ModelID: models[1099].ID}}}); err != nil {
		panic(err)
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	api.Register(r.Group("/api"))
	var mu sync.Mutex
	requests := []string{}
	failSave := false
	failQuery := false
	mux := http.NewServeMux()
	mux.HandleFunc("/__fixture/state", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(requests)
	})
	mux.HandleFunc("/__fixture/fail-save", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		failSave = true
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/__fixture/fail-query", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		failQuery = true
		mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		requests = append(requests, req.Method+" "+req.URL.String())
		saveFailure := failSave && req.Method == "POST" && req.URL.Path == "/api/groups"
		queryFailure := failQuery && req.URL.Path == "/api/models"
		if saveFailure {
			failSave = false
		}
		if queryFailure {
			failQuery = false
		}
		mu.Unlock()
		if saveFailure || queryFailure {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"隔离验证：一次性失败"}`)
			return
		}
		// 旧搜索故意晚返回，用于验证前端忽略过期响应。
		if req.URL.Path == "/api/models" && req.URL.Query().Get("name") == "catalog-109" {
			time.Sleep(1800 * time.Millisecond)
		}
		r.ServeHTTP(w, req)
	})
	mux.Handle("/", http.FileServer(http.Dir("web/dist/renderer")))
	log.Print("group fixture http://127.0.0.1:13471")
	log.Fatal(http.ListenAndServe("127.0.0.1:13471", mux))
}
