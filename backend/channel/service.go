package channel

import (
	"errors"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// ErrInvalid 表示渠道字段校验不通过。
var ErrInvalid = errors.New("渠道字段不合法")

// CreateRequest 创建渠道请求。
type CreateRequest struct {
	Name     string            `json:"name" binding:"required"`
	Type     protocol.Provider `json:"type" binding:"required"`
	BaseURL  string            `json:"base_url" binding:"required"`
	Key      string            `json:"key" binding:"required"`
	Enabled  *bool             `json:"enabled"`
	AutoSync *bool             `json:"auto_sync"`
}

// UpdateRequest 更新渠道请求，仅包含需要变更的字段。
type UpdateRequest struct {
	Name     *string            `json:"name"`
	Type     *protocol.Provider `json:"type"`
	BaseURL  *string            `json:"base_url"`
	Key      *string            `json:"key"`
	Enabled  *bool              `json:"enabled"`
	AutoSync *bool              `json:"auto_sync"`
}

// Response 是渠道的对外响应，Key 已脱敏。
type Response struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Type      protocol.Provider `json:"type"`
	BaseURL   string            `json:"base_url"`
	Key       string            `json:"key"`
	Enabled   bool              `json:"enabled"`
	AutoSync  bool              `json:"auto_sync"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// ToResponse 将实体转为脱敏后的响应。
func (c *Channel) ToResponse() Response {
	return Response{
		ID:        c.ID,
		Name:      c.Name,
		Type:      c.Type,
		BaseURL:   c.BaseURL,
		Key:       maskKey(c.Key),
		Enabled:   c.Enabled,
		AutoSync:  c.AutoSync,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

// maskKey 对上游 key 做脱敏，避免管理面板泄露明文凭据。
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// List 返回所有渠道。
func List() ([]Channel, error) {
	var cs []Channel
	err := shared.DB.Order("id").Find(&cs).Error
	return cs, err
}

// Get 返回单个渠道。
func Get(id int64) (*Channel, error) {
	var c Channel
	if err := shared.DB.First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// Create 创建渠道。
func Create(req CreateRequest) (*Channel, error) {
	c := Channel{
		Name:     req.Name,
		Type:     req.Type,
		BaseURL:  req.BaseURL,
		Key:      req.Key,
		Enabled:  true,
		AutoSync: true,
	}
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if req.AutoSync != nil {
		c.AutoSync = *req.AutoSync
	}
	if !c.Valid() {
		return nil, ErrInvalid
	}
	if err := shared.DB.Create(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// Update 更新渠道，仅应用请求中非 nil 的字段。
func Update(id int64, req UpdateRequest) (*Channel, error) {
	var c Channel
	if err := shared.DB.First(&c, id).Error; err != nil {
		return nil, err
	}
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.Type != nil {
		c.Type = *req.Type
	}
	if req.BaseURL != nil {
		c.BaseURL = *req.BaseURL
	}
	if req.Key != nil {
		c.Key = *req.Key
	}
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if req.AutoSync != nil {
		c.AutoSync = *req.AutoSync
	}
	if !c.Valid() {
		return nil, ErrInvalid
	}
	if err := shared.DB.Save(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// Delete 删除渠道。
// TODO: 级联删除该渠道下的模型，避免产生孤儿记录。
func Delete(id int64) error {
	return shared.DB.Delete(&Channel{}, id).Error
}
