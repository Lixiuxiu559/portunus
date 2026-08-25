package model

import (
	"errors"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// ErrInvalid 表示模型字段校验不通过。
var ErrInvalid = errors.New("模型字段不合法")

// ErrChannelNotFound 表示指定的渠道不存在。
var ErrChannelNotFound = errors.New("渠道不存在")

// CreateRequest 创建模型请求。
type CreateRequest struct {
	ChannelID       int64    `json:"channel_id" binding:"required"`
	Name            string   `json:"name" binding:"required"`
	InputPrice      *float64 `json:"input_price"`       // 不传则用内置默认价
	OutputPrice     *float64 `json:"output_price"`      // 不传则用内置默认价
	CacheReadPrice  *float64 `json:"cache_read_price"`  // 不传则用内置默认价
	CacheWritePrice *float64 `json:"cache_write_price"` // 不传则用内置默认价
	Enabled         *bool    `json:"enabled"`
}

// UpdateRequest 更新模型请求，仅包含需要变更的字段。
type UpdateRequest struct {
	ChannelID       *int64   `json:"channel_id"`
	Name            *string  `json:"name"`
	InputPrice      *float64 `json:"input_price"`
	OutputPrice     *float64 `json:"output_price"`
	CacheReadPrice  *float64 `json:"cache_read_price"`
	CacheWritePrice *float64 `json:"cache_write_price"`
	Enabled         *bool    `json:"enabled"`
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
	Enabled         bool      `json:"enabled"`
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
		Enabled:         m.Enabled,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

// List 返回模型列表；channelID > 0 时仅返回该渠道下的模型。
func List(channelID int64) ([]Model, error) {
	q := shared.DB
	if channelID > 0 {
		q = q.Where("channel_id = ?", channelID)
	}
	var ms []Model
	err := q.Order("id").Find(&ms).Error
	return ms, err
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
		Enabled:         true,
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
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
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
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
