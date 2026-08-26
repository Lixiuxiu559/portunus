package cron

import (
	"log"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// tickInterval 是调度器的检查粒度；外层的同步间隔设置远大于它，
// 且每次 tick 都会重读设置，使运行时改间隔能很快生效。
const tickInterval = time.Minute

// Start 启动后台定时任务：按全局同步间隔自动同步各渠道模型。
// 阻塞前以 goroutine 运行，不占用调用方。
func Start() {
	go func() {
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for range ticker.C {
			interval := shared.GetSyncInterval()
			if interval <= 0 {
				continue
			}
			last := shared.GetLastSyncAt()
			now := time.Now()
			if now.Unix()-last < int64(interval)*60 {
				continue
			}
			synced, added := model.SyncAutoChannels()
			if err := shared.SetLastSyncAt(now); err != nil {
				log.Printf("记录同步时间失败: %v", err)
			}
			log.Printf("自动同步完成: 渠道 %d 个, 新增模型 %d 个", synced, added)
		}
	}()
}