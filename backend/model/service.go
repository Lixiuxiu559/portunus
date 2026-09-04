package model

import (
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// ErrInvalid 表示模型字段校验不通过。
var ErrInvalid = &shared.StatusError{Status: 400, Message: "模型字段不合法"}

// ErrChannelNotFound 表示指定的渠道不存在。
var ErrChannelNotFound = &shared.StatusError{Status: 400, Message: "渠道不存在"}

// CreateRequest 创建模型请求。
type CreateRequest struct {
	ChannelID       int64    `json:"channel_id" binding:"required"`
	Name            string   `json:"name" binding:"required"`
	InputPrice      *float64 `json:"input_price"`       // 不传则用内置默认价
	OutputPrice     *float64 `json:"output_price"`      // 不传则用内置默认价
	CacheReadPrice  *float64 `json:"cache_read_price"`  // 不传则用内置默认价
	CacheWritePrice *float64 `json:"cache_write_price"` // 不传则用内置默认价
}

// UpdateRequest 更新模型请求，仅包含需要变更的字段。
type UpdateRequest struct {
	ChannelID       *int64   `json:"channel_id"`
	Name            *string  `json:"name"`
	InputPrice      *float64 `json:"input_price"`
	OutputPrice     *float64 `json:"output_price"`
	CacheReadPrice  *float64 `json:"cache_read_price"`
	CacheWritePrice *float64 `json:"cache_write_price"`
}

// Response 是模型的对外响应。
type Response struct {
	ID              int64     `json:"id"`
	ChannelID       int64     `json:"channel_id"`
	Name            string    `json:"name"`
	InputPrice      float64   `json:"input_price"`
	OutputPrice     float64   `json:"output_price"`
	CacheReadPrice  float64   `json:"cache_read_price"`
	CacheWritePrice float64   `json:"cache_write_price"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ToResponse 将实体转为响应。
func (m *Model) ToResponse() Response {
	return Response{
		ID:              m.ID,
		ChannelID:       m.ChannelID,
		Name:            m.Name,
		InputPrice:      m.InputPrice,
		OutputPrice:     m.OutputPrice,
		CacheReadPrice:  m.CacheReadPrice,
		CacheWritePrice: m.CacheWritePrice,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

// List 返回分页模型列表与总数；支持按渠道与名称关键词过滤（均可选）。
func List(channelID int64, name string, page, pageSize int) ([]Model, int64, error) {
	q := shared.DB
	if channelID > 0 {
		q = q.Where("channel_id = ?", channelID)
	}
	// 名称不区分大小写的模糊匹配
	if name != "" {
		q = q.Where("lower(name) LIKE ?", "%"+strings.ToLower(name)+"%")
	}
	var total int64
	if err := q.Model(&Model{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	var ms []Model
	err := q.Order("lower(name), id").Offset((page - 1) * pageSize).Limit(pageSize).Find(&ms).Error
	return ms, total, err
}

// Get 返回单个模型。
func Get(id int64) (*Model, error) {
	var m Model
	if err := shared.DB.First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// Create 创建模型；未显式指定的价格字段用内置默认价补齐。
func Create(req CreateRequest) (*Model, error) {
	if req.Name == "" {
		return nil, ErrInvalid
	}
	ok, err := channel.Exists(req.ChannelID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrChannelNotFound
	}

	def := defaultPrice[req.Name]
	m := Model{
		ChannelID:       req.ChannelID,
		Name:            req.Name,
		InputPrice:      resolvePrice(req.InputPrice, def.Input),
		OutputPrice:     resolvePrice(req.OutputPrice, def.Output),
		CacheReadPrice:  resolvePrice(req.CacheReadPrice, def.CacheRead),
		CacheWritePrice: resolvePrice(req.CacheWritePrice, def.CacheWrite),
	}
	if err := shared.DB.Create(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// Update 更新模型，仅应用请求中非 nil 的字段。
func Update(id int64, req UpdateRequest) (*Model, error) {
	var m Model
	if err := shared.DB.First(&m, id).Error; err != nil {
		return nil, err
	}
	if req.Name != nil {
		if *req.Name == "" {
			return nil, ErrInvalid
		}
		m.Name = *req.Name
	}
	if req.ChannelID != nil {
		ok, err := channel.Exists(*req.ChannelID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrChannelNotFound
		}
		m.ChannelID = *req.ChannelID
	}
	if req.InputPrice != nil {
		m.InputPrice = *req.InputPrice
	}
	if req.OutputPrice != nil {
		m.OutputPrice = *req.OutputPrice
	}
	if req.CacheReadPrice != nil {
		m.CacheReadPrice = *req.CacheReadPrice
	}
	if req.CacheWritePrice != nil {
		m.CacheWritePrice = *req.CacheWritePrice
	}
	if err := shared.DB.Save(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// Delete 删除模型。
func Delete(id int64) error {
	return shared.DB.Delete(&Model{}, id).Error
}

// DeleteByChannelTx 在事务中删除指定渠道下的全部模型。
func DeleteByChannelTx(tx *gorm.DB, channelID int64) error {
	return tx.Where("channel_id = ?", channelID).Delete(&Model{}).Error
}

// resolvePrice 返回请求中显式指定的价格，否则返回内置默认价（无则 0）。
func resolvePrice(req *float64, def float64) float64 {
	if req != nil {
		return *req
	}
	return def
}

// Exists 校验模型是否存在。
func Exists(id int64) (bool, error) {
	var count int64
	err := shared.DB.Model(&Model{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}
