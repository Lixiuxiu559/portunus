package model

import (
	"log"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// SyncFromChannel 从渠道上游拉取模型列表，把本地缺失的模型同步到模型表。
// 已存在（channel_id + name）的模型不覆盖，保留用户手动设置的价格。
// 返回新增的模型数量。
func SyncFromChannel(ch *channel.Channel) (int, error) {
	upstream, err := protocol.NewUpstream(ch.Type, ch.BaseURL, ch.Key)
	if err != nil {
		return 0, err
	}
	names, err := upstream.FetchModels()
	if err != nil {
		return 0, err
	}
	added := 0
	for _, name := range names {
		var count int64
		if err := shared.DB.Model(&Model{}).Where("channel_id = ? AND name = ?", ch.ID, name).Count(&count).Error; err != nil {
			return added, err
		}
		if count > 0 {
			continue
		}
		m := Model{ChannelID: ch.ID, Name: name, Currency: CurrencyUSD}
		m.ApplyDefaultPrice()
		if err := shared.DB.Create(&m).Error; err != nil {
			return added, err
		}
		added++
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
		if !c.AutoSync {
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
