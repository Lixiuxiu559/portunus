// Package upstream 管理厂商预设和连接配置，协议语义仍由 protocol 负责。
package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

type Options struct {
	SchemaVersion  int    `json:"schema_version"`
	ModelDiscovery string `json:"model_discovery"`
}

// Effective 将旧渠道零值解释为原有默认行为，不修改持久化快照。
func (o Options) Effective() Options {
	if o.SchemaVersion == 0 {
		o.SchemaVersion = 1
	}
	if o.ModelDiscovery == "" {
		o.ModelDiscovery = "api"
	}
	return o
}

type Config struct {
	ProviderID string            `json:"provider_id"`
	ProfileID  string            `json:"profile_id"`
	AdapterID  string            `json:"adapter_id"`
	Type       protocol.Provider `json:"type"`
	BaseURL    string            `json:"base_url"`
	Key        string            `json:"key"`
	Options    Options           `json:"connection_options"`
}

// Resolve 校验新建或修改中的连接，展开一次性默认值。运行时使用 Open。
func Resolve(cfg Config) (Config, error) {
	if cfg.ProviderID == "" {
		cfg.ProviderID = "generic"
	}
	var preset *Preset
	for _, item := range Presets() {
		if item.ID == cfg.ProviderID {
			preset = &item
			break
		}
	}
	if preset == nil {
		return Config{}, errors.New("未知厂商预设")
	}
	var profile *Profile
	for _, item := range preset.Profiles {
		match := item.ID == cfg.ProfileID
		if cfg.ProfileID == "" {
			match = item.ID == preset.DefaultProfile
			if cfg.Type != "" {
				match = item.Type == cfg.Type
			}
		}
		if match {
			profile = &item
			break
		}
	}
	if profile == nil {
		return Config{}, errors.New("未知或不兼容的厂商配置档")
	}
	cfg.ProfileID = profile.ID
	if cfg.Type == "" {
		cfg.Type = profile.Type
	}
	if cfg.Type != profile.Type {
		return Config{}, errors.New("协议与厂商配置档不匹配")
	}
	if cfg.AdapterID == "" {
		cfg.AdapterID = profile.AdapterID
	}
	if cfg.AdapterID != profile.AdapterID {
		return Config{}, errors.New("连接适配器与配置档不匹配")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = profile.BaseURL
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if err := validateRoot(cfg.BaseURL); err != nil {
		return Config{}, err
	}
	if cfg.Options.ModelDiscovery == "" {
		cfg.Options.ModelDiscovery = profile.ModelDiscovery
	}
	if err := normalizeOptions(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateRoot(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return errors.New("API 根地址必须是有效的 HTTP 或 HTTPS 地址")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return errors.New("API 根地址不能包含凭据、查询参数或片段")
	}
	path := strings.TrimRight(strings.ToLower(u.Path), "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages", "/models"} {
		if strings.HasSuffix(path, suffix) {
			return errors.New("请输入 API 根地址，移除末尾的具体操作路径")
		}
	}
	if strings.Contains(path, ":generatecontent") || strings.Contains(path, ":streamgeneratecontent") {
		return errors.New("请输入 API 根地址，移除模型生成操作路径")
	}
	return nil
}

func normalizeOptions(cfg *Config) error {
	if !cfg.Type.Valid() {
		return errors.New("未知上游协议")
	}
	if cfg.AdapterID == "" {
		cfg.AdapterID = "generic"
	}
	if cfg.AdapterID != "generic" {
		return errors.New("未知连接适配器")
	}
	cfg.Options = cfg.Options.Effective()
	if cfg.Options.SchemaVersion != 1 {
		return errors.New("不支持的连接设置版本")
	}
	if cfg.Options.ModelDiscovery != "api" && cfg.Options.ModelDiscovery != "manual" {
		return errors.New("未知模型发现模式")
	}
	return nil
}

// Connection 只持有已保存配置及协议接入器，不隐式应用厂商目录默认值。
type Connection struct {
	config Config
	impl   protocol.Upstream
}

// Open 保留历史配置的地址拼接行为，不对已保存地址执行新建校验。
func Open(cfg Config) (*Connection, error) {
	if err := normalizeOptions(&cfg); err != nil {
		return nil, err
	}
	impl, err := protocol.NewUpstream(cfg.Type, cfg.BaseURL, cfg.Key)
	if err != nil {
		return nil, errors.New("无法构建上游连接")
	}
	return &Connection{config: cfg, impl: impl}, nil
}

func (c *Connection) FetchModels(ctx context.Context) ([]string, error) {
	if c.config.Options.ModelDiscovery == "manual" {
		return nil, errors.New("该连接使用手动模型配置，不支持模型发现")
	}
	return protocol.FetchUpstreamModels(ctx, c.config.Type, c.config.BaseURL, c.config.Key)
}

func (c *Connection) ChatHeaders() http.Header { return c.impl.ChatHeaders() }

func (c *Connection) ChatURL(model string, stream bool) string { return c.impl.ChatURL(model, stream) }
