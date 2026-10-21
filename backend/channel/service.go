package channel

import (
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
)

// ErrInvalid 表示渠道字段校验不通过。
var ErrInvalid = &shared.StatusError{Status: 400, Message: "渠道字段不合法"}

// CreateRequest 创建渠道请求。
type CreateRequest struct {
	ProviderID        string            `json:"provider_id"`
	ProfileID         string            `json:"profile_id"`
	AdapterID         string            `json:"adapter_id"`
	ConnectionOptions upstream.Options  `json:"connection_options"`
	Name              string            `json:"name" binding:"required"`
	Type              protocol.Provider `json:"type"`
	BaseURL           string            `json:"base_url"`
	Key               string            `json:"key" binding:"required"`
	AutoSync          *bool             `json:"auto_sync"`
	ThinkingCompat    *bool             `json:"thinking_compat"`
}

// UpdateRequest 更新渠道请求，仅包含需要变更的字段。
type UpdateRequest struct {
	ProviderID        *string            `json:"provider_id"`
	ProfileID         *string            `json:"profile_id"`
	AdapterID         *string            `json:"adapter_id"`
	ConnectionOptions *upstream.Options  `json:"connection_options"`
	Name              *string            `json:"name"`
	Type              *protocol.Provider `json:"type"`
	BaseURL           *string            `json:"base_url"`
	Key               *string            `json:"key"`
	AutoSync          *bool              `json:"auto_sync"`
	ThinkingCompat    *bool              `json:"thinking_compat"`
}

// Response 是渠道的对外响应，Key 已脱敏。
type Response struct {
	ProviderID        string            `json:"provider_id"`
	ProfileID         string            `json:"profile_id"`
	AdapterID         string            `json:"adapter_id"`
	ConnectionOptions upstream.Options  `json:"connection_options"`
	ID                int64             `json:"id"`
	Name              string            `json:"name"`
	Type              protocol.Provider `json:"type"`
	BaseURL           string            `json:"base_url"`
	Key               string            `json:"key"`
	AutoSync          bool              `json:"auto_sync"`
	ThinkingCompat    bool              `json:"thinking_compat"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

// ToResponse 将实体转为脱敏后的响应。
func (c *Channel) ToResponse() Response {
	return Response{
		ProviderID: c.ProviderID, ProfileID: c.ProfileID, AdapterID: c.AdapterID, ConnectionOptions: c.ConnectionOptions,
		ID:             c.ID,
		Name:           c.Name,
		Type:           c.Type,
		BaseURL:        c.BaseURL,
		Key:            maskKey(c.Key),
		AutoSync:       c.AutoSync,
		ThinkingCompat: c.ThinkingCompat,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
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

// Exists 校验渠道是否存在。
func Exists(id int64) (bool, error) {
	var count int64
	err := shared.DB.Model(&Channel{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

// Create 创建渠道，默认值解析和写入在一个事务中完成。
func Create(req CreateRequest) (c *Channel, err error) {
	err = shared.DB.Transaction(func(tx *gorm.DB) error { c, err = CreateTx(tx, &req); return err })
	return
}

// CreateTx 创建渠道；调用方负责包含模型导入等操作的事务。
func CreateTx(tx *gorm.DB, req *CreateRequest) (*Channel, error) {
	if req == nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Key) == "" {
		return nil, ErrInvalid
	}
	cfg, err := resolveConnection(upstream.Config{ProviderID: req.ProviderID, ProfileID: req.ProfileID, AdapterID: req.AdapterID, Type: req.Type, BaseURL: req.BaseURL, Key: req.Key, Options: req.ConnectionOptions})
	if err != nil {
		return nil, err
	}
	c := Channel{Name: req.Name, AutoSync: true, ThinkingCompat: true}
	c.applyConnection(cfg)
	if req.AutoSync != nil {
		c.AutoSync = *req.AutoSync
	}
	if req.ThinkingCompat != nil {
		c.ThinkingCompat = *req.ThinkingCompat
	}
	if c.ConnectionOptions.ModelDiscovery == "manual" {
		c.AutoSync = false
	}
	// GORM 的 default:true 会覆盖 false；布尔值显式写入以保留用户选择。
	autoSync, thinkingCompat := c.AutoSync, c.ThinkingCompat
	if err := tx.Omit("AutoSync", "ThinkingCompat").Create(&c).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&c).Updates(map[string]interface{}{"auto_sync": autoSync, "thinking_compat": thinkingCompat}).Error; err != nil {
		return nil, err
	}
	c.AutoSync, c.ThinkingCompat = autoSync, thinkingCompat
	return &c, nil
}

// OverlayConnection 将非缺失的连接字段叠加到已有快照；空 Key 表示保留凭据。
// 该方法仅合并配置，校验和预设默认值展开由调用方执行。
func (req UpdateRequest) OverlayConnection(cfg upstream.Config) upstream.Config {
	if req.ProviderID != nil {
		cfg.ProviderID = *req.ProviderID
	}
	if req.ProfileID != nil {
		cfg.ProfileID = *req.ProfileID
	}
	if req.AdapterID != nil {
		cfg.AdapterID = *req.AdapterID
	}
	if req.Type != nil {
		// 兼容只了解旧 Type 字段的调用方；显式提供 profile 时仍严格校验。
		if *req.Type != cfg.Type && req.ProfileID == nil {
			cfg.ProfileID = ""
		}
		cfg.Type = *req.Type
	}
	if req.BaseURL != nil {
		cfg.BaseURL = *req.BaseURL
	}
	if req.Key != nil && *req.Key != "" {
		cfg.Key = *req.Key
	}
	if req.ConnectionOptions != nil && req.ConnectionOptions.Effective() != cfg.Options.Effective() {
		cfg.Options = *req.ConnectionOptions
	}
	return cfg
}

// Update 更新渠道；缺失字段保留。空 Key 不清除已有凭据。
func Update(id int64, req UpdateRequest) (c *Channel, err error) {
	err = shared.DB.Transaction(func(tx *gorm.DB) error { c, err = UpdateTx(tx, id, req); return err })
	return
}

// UpdateTx 更新渠道。仅连接字段发生变更时重新校验连接，兼容旧渠道的纯元数据编辑。
func UpdateTx(tx *gorm.DB, id int64, req UpdateRequest) (*Channel, error) {
	if req.BaseURL != nil && strings.TrimSpace(*req.BaseURL) == "" {
		return nil, &shared.StatusError{Status: 400, Message: "API 根地址不能为空"}
	}
	if req.Type != nil && !req.Type.Valid() {
		return nil, ErrInvalid
	}
	var c Channel
	if err := tx.First(&c, id).Error; err != nil {
		return nil, err
	}
	if req.Name != nil {
		c.Name = *req.Name
	}
	if strings.TrimSpace(c.Name) == "" {
		return nil, ErrInvalid
	}
	cfg := req.OverlayConnection(c.ConnectionConfig())
	if cfg != c.ConnectionConfig() {
		if strings.TrimSpace(cfg.Key) == "" {
			return nil, ErrInvalid
		}
		resolved, err := resolveConnection(cfg)
		if err != nil {
			return nil, err
		}
		c.applyConnection(resolved)
	}
	if req.AutoSync != nil {
		c.AutoSync = *req.AutoSync
	}
	if req.ThinkingCompat != nil {
		c.ThinkingCompat = *req.ThinkingCompat
	}
	if c.ConnectionOptions.ModelDiscovery == "manual" {
		c.AutoSync = false
	}
	if err := tx.Save(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func resolveConnection(cfg upstream.Config) (upstream.Config, error) {
	resolved, err := upstream.Resolve(cfg)
	if err != nil {
		return upstream.Config{}, &shared.StatusError{Status: 400, Message: err.Error()}
	}
	return resolved, nil
}

// DeleteTx 在事务中删除渠道。级联清理由调用方（api 层）在同一个事务内完成。
func DeleteTx(tx *gorm.DB, id int64) error {
	return tx.Delete(&Channel{}, id).Error
}
