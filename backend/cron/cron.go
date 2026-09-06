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

// cleanupEveryMinutes 是日志清理的执行周期（小时级即可：清理是分批删除，
// 不需要分钟级跟进；每小时一次保证服务重启后最多延迟一小时补上）。
const cleanupEveryMinutes = 60

// Start 启动后台定时任务：按全局同步间隔自动同步各渠道模型，并按
// log_retention_days 设置定期清理过期调用日志。
// 阻塞前以 goroutine 运行，不占用调用方。
func Start() {
	go func() {
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		minutes := 0
		for range ticker.C {
			minutes++
			syncTick()
			if minutes%cleanupEveryMinutes == 0 {
				cleanupTick()
			}
		}
	}()
}

// syncTick 渠道模型自动同步：达到 sync_interval 间隔才真正同步（0 = 关闭）。
func syncTick() {
	interval := shared.GetSyncInterval()
	if interval <= 0 {
		return
	}
	last := shared.GetLastSyncAt()
	now := time.Now()
	if now.Unix()-last < int64(interval)*60 {
		return
	}
	synced, added := model.SyncAutoChannels()
	if err := shared.SetLastSyncAt(now); err != nil {
		log.Printf("记录同步时间失败: %v", err)
	}
	log.Printf("自动同步完成: 渠道 %d 个, 新增模型 %d 个", synced, added)
}

// cleanupTick 过期日志清理：按 log_retention_days 设置（0 = 禁用）分批删除。
func cleanupTick() {
	days := shared.GetLogRetentionDays()
	if days <= 0 {
		return
	}
	n, err := shared.DeleteOldLogs(time.Now().AddDate(0, 0, -days), 500)
	if err != nil {
		log.Printf("清理过期日志失败: %v", err)
		return
	}
	if n > 0 {
		log.Printf("已清理 %d 条过期日志（保留 %d 天）", n, days)
	}
}
