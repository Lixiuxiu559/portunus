package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/gateway"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
)

// connectionDraft 是管理面草稿；只能在后端复用已存凭据，不回传原始配置。
type connectionDraft struct {
	channel.UpdateRequest
	ChannelID int64 `json:"channel_id"`
}

func connectionError(message string) error {
	return &shared.StatusError{Status: http.StatusBadRequest, Message: message}
}

type draftConnection struct {
	upstream.Config
	ThinkingCompat bool
	strict         bool
}

func (d connectionDraft) config() (draftConnection, error) {
	cfg := draftConnection{ThinkingCompat: true, strict: true}
	if d.ChannelID != 0 {
		if d.BaseURL != nil && strings.TrimSpace(*d.BaseURL) == "" {
			return cfg, connectionError("API 根地址不能为空")
		}
		ch, err := channel.Get(d.ChannelID)
		if err != nil {
			return cfg, err
		}
		cfg.Config = ch.ConnectionConfig()
		cfg.ThinkingCompat = ch.ThinkingCompat
	}
	if d.Type != nil && !d.Type.Valid() {
		return cfg, connectionError("不支持的协议类型")
	}
	original := cfg.Config
	cfg.Config = d.UpdateRequest.OverlayConnection(original)
	cfg.strict = d.ChannelID == 0 || cfg.Config != original
	if d.ThinkingCompat != nil {
		cfg.ThinkingCompat = *d.ThinkingCompat
	}
	return cfg, nil
}

func (d connectionDraft) open(requireKey bool) (*upstream.Connection, draftConnection, error) {
	cfg, err := d.config()
	if err != nil {
		return nil, cfg, err
	}
	// 新建/变更连接严格校验；未修改连接的已存渠道保持历史行为。
	if cfg.strict {
		cfg.Config, err = upstream.Resolve(cfg.Config)
		if err != nil {
			return nil, cfg, connectionError(err.Error())
		}
	}
	if requireKey && cfg.Key == "" {
		return nil, cfg, connectionError("请填写 API Key")
	}
	conn, err := upstream.Open(cfg.Config)
	if err != nil {
		return nil, cfg, connectionError(err.Error())
	}
	return conn, cfg, err
}

func listUpstreamPresets(c *gin.Context) {
	c.JSON(http.StatusOK, upstream.Presets())
}

func resolveChannelConnection(c *gin.Context) {
	var req connectionDraft
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "连接配置格式不合法"})
		return
	}
	conn, cfg, err := req.open(false)
	if err != nil {
		respondError(c, err)
		return
	}
	// 白名单响应，不能序列化含 Key 的 Config。
	c.JSON(http.StatusOK, gin.H{
		"type": cfg.Type, "provider_id": cfg.ProviderID, "profile_id": cfg.ProfileID,
		"adapter_id": cfg.AdapterID, "base_url": diagnosticURL(cfg.BaseURL),
		"chat_url": diagnosticURL(conn.ChatURL("{model}", false)), "model_discovery": cfg.Options.Effective().ModelDiscovery,
	})
}

func diagnosticURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "历史地址无法安全展示"
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	return u.String()
}

func testChannelConnection(c *gin.Context) {
	var req struct {
		connectionDraft
		Model           string `json:"model"`
		Stream          bool   `json:"stream"`
		MaxOutputTokens *int   `json:"max_output_tokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "调用验证参数不合法"})
		return
	}
	modelName := strings.TrimSpace(req.Model)
	limit := 32
	if req.MaxOutputTokens != nil {
		limit = *req.MaxOutputTokens
	}
	if modelName == "" || len(modelName) > 512 || limit < 1 || limit > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定模型，输出上限必须为 1–128 token"})
		return
	}
	_, cfg, err := req.open(true)
	if err != nil {
		respondError(c, err)
		return
	}
	ch := channel.Channel{
		Name: "渠道调用验证", Type: cfg.Type, BaseURL: cfg.BaseURL, Key: cfg.Key,
		ProviderID: cfg.ProviderID, ProfileID: cfg.ProfileID, AdapterID: cfg.AdapterID,
		ConnectionOptions: cfg.Options, ThinkingCompat: cfg.ThinkingCompat,
	}
	result := gateway.ProbeConnection(c.Request.Context(), ch, gateway.ProbeOptions{
		Model: modelName, Stream: req.Stream, MaxOutputTokens: limit,
	})
	c.JSON(http.StatusOK, result)
}
