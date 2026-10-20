// 日志界面隔离验证：只用内存 SQLite，不读取正式或 Dev 配置。
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
	if err = db.AutoMigrate(&channel.Channel{}, &shared.Log{}); err != nil {
		panic(err)
	}
	for _, name := range []string{"同名渠道 A", "同名渠道 B"} {
		db.Create(&channel.Channel{Name: name, Type: "openai", BaseURL: "http://127.0.0.1:1"})
	}
	for i := 1; i <= 45; i++ {
		req := "req_%+&=中"
		if i > 25 {
			req = "req_%+&=中-prefix"
		}
		row := shared.Log{ChannelID: int64(1 + i%2), GroupName: "coding", ModelName: "shared-model", RequestID: req, Status: 200, Success: true, InputToken: 10, OutputToken: 2, CacheReadToken: 3, DurationMs: 420, FirstTokenMs: 80, Stream: true, ClientProtocol: "openai_responses", CreatedAt: time.Now()}
		if i == 25 {
			row.Success = false
			row.Status = 429
			row.ErrKind = "upstream_error"
			row.ErrMsg = "合成上游限流 <script>window.unexpected=true</script>\n请稍后重试：" + fmt.Sprintf("%0300d", 1)
		}
		if i == 45 {
			row.ChannelID = 99
			row.ErrKind = "future_error"
			row.ErrMsg = "已删除渠道的合成错误"
			row.Success = false
			row.Status = 503
		}
		if err = db.Create(&row).Error; err != nil {
			panic(err)
		}
	}
	db.Create(&shared.Log{GroupName: "legacy", ModelName: "old-model", CreatedAt: time.Now()})
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	api.Register(r.Group("/api"))
	var mu sync.Mutex
	requests := []string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/__fixture/requests", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(requests)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		requests = append(requests, req.URL.String())
		mu.Unlock()
		r.ServeHTTP(w, req)
	})
	mux.Handle("/", http.FileServer(http.Dir("web/dist/renderer")))
	log.Print("log fixture http://127.0.0.1:13472")
	log.Fatal(http.ListenAndServe("127.0.0.1:13472", mux))
}
