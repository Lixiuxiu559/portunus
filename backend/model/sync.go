package model

import (
	"context"
	"gorm.io/gorm"
	"log"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
)

// SyncFromChannel 从渠道上游拉取模型列表，把本地缺失的模型同步到模型表。
// 已存在（channel_id + name）的模型不覆盖，保留包括历史价格在内的既有数据。
// 返回新增的模型数量。
func SyncFromChannel(ch *channel.Channel) (int, error) {
	return SyncFromChannelContext(context.Background(), ch)
}

// SyncFromChannelContext 支持调用方取消模型发现，导入在事务中原子完成。
func SyncFromChannelContext(ctx context.Context, ch *channel.Channel) (int, error) {
	if ch == nil {
		return 0, ErrChannelNotFound
	}
	if ch.ConnectionOptions.ModelDiscovery == "manual" {
		return 0, ErrManualDiscovery
	}
	conn, err := upstream.Open(ch.ConnectionConfig())
	if err != nil {
		return 0, err
	}
	names, err := conn.FetchModels(ctx)
	if err != nil {
		return 0, err
	}
	added := 0
	err = shared.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var importErr error
		added, importErr = ImportNamesTx(tx, ch.ID, names)
		return importErr
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// SyncAutoChannels 同步所有开启自动同步的渠道，返回成功同步的渠道数与新增模型总数。
// 单个渠道失败仅记录，不中断整体，保证一个慢渠道不拖垮其余渠道。
func SyncAutoChannels() (synced, added int) {
	cs, err := channel.List()
	if err != nil {
		log.Printf("加载渠道列表失败: %v", err)
		return 0, 0
	}
	for i := range cs {
		c := cs[i]
		if !c.AutoSync || c.ConnectionOptions.ModelDiscovery == "manual" {
			continue
		}
		n, err := SyncFromChannel(&c)
		if err != nil {
			log.Printf("同步渠道 %s 模型失败: %v", c.Name, err)
			continue
		}
		synced++
		added += n
	}
	return synced, added
}
