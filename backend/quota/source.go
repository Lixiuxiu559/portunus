package quota

import (
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"strings"
)

const (
	CredentialInherit   = "inherit_channel_key"
	CredentialDedicated = "dedicated"
	CredentialNone      = "none"
)

func statusError(status int, message string) error {
	return &shared.StatusError{Status: status, Message: message}
}
func sourceFromInput(old Source, in SourceInput, key string, requireToken bool) (Source, Adapter, error) {
	s := old
	if old.AdapterID != in.AdapterID || (old.AdapterID == "custom.balance" && strings.TrimRight(strings.TrimSpace(old.QueryBaseURL), "/") != strings.TrimRight(strings.TrimSpace(in.QueryBaseURL), "/")) {
		s.Credentials = Secrets{}
	}
	s.Name = strings.TrimSpace(in.Name)
	s.AdapterID = in.AdapterID
	s.SchemaVersion = in.SchemaVersion
	if s.SchemaVersion == 0 {
		s.SchemaVersion = 1
	}
	s.Enabled = in.Enabled
	s.QueryBaseURL = strings.TrimSpace(in.QueryBaseURL)
	s.CredentialMode = in.CredentialMode
	s.Options = in.Options
	if in.Options.Custom != nil {
		// JSON omitempty 会将空请求头恢复为 nil；统一两者，避免无鉴权来源无改动保存时失效。
		config := *in.Options.Custom
		if len(config.Request.Headers) == 0 {
			config.Request.Headers = nil
		}
		s.Options.Custom = &config
	}
	s.AutoRefreshMinutes = in.AutoRefreshMinutes
	if s.Name == "" || len(s.Name) > 200 {
		return s, nil, statusError(400, "查询来源名称不能为空且不能超过 200 字符")
	}
	if s.SchemaVersion != 1 || s.AutoRefreshMinutes < 0 || s.AutoRefreshMinutes > 60 {
		return s, nil, statusError(400, "配置版本或自动刷新间隔无效")
	}
	a, ok := LookupAdapter(s.AdapterID)
	if !ok {
		return s, nil, statusError(400, "不支持的查询适配器")
	}
	if s.CredentialMode != CredentialInherit && s.CredentialMode != CredentialDedicated && s.CredentialMode != CredentialNone {
		return s, nil, statusError(400, "凭据模式无效")
	}
	allowed := false
	for _, m := range a.Describe().CredentialModes {
		if m == s.CredentialMode {
			allowed = true
		}
	}
	if !allowed {
		return s, nil, statusError(400, "适配器不支持此凭据模式")
	}
	if in.Credentials != nil {
		if in.Credentials.Clear {
			s.Credentials.Token = ""
		} else if in.Credentials.Token != nil && strings.TrimSpace(*in.Credentials.Token) != "" {
			s.Credentials.Token = strings.TrimSpace(*in.Credentials.Token)
		}
	}
	if s.CredentialMode == CredentialInherit || s.CredentialMode == CredentialNone {
		s.Credentials = Secrets{}
	}
	if s.QueryBaseURL == "" {
		s.QueryBaseURL = a.Describe().DefaultBaseURL
	}
	if err := a.Validate(s); err != nil {
		return s, nil, statusError(400, "查询配置无效："+err.Error())
	}
	token := s.Credentials.Token
	if s.CredentialMode == CredentialInherit {
		token = key
	}
	if s.CredentialMode != CredentialNone && (s.Enabled || requireToken) && strings.TrimSpace(token) == "" {
		return s, nil, statusError(400, "查询凭据不能为空")
	}
	return s, a, nil
}
func resolvedSource(s Source, key string) ResolvedSource {
	token := s.Credentials.Token
	if s.CredentialMode == CredentialInherit {
		token = key
	}
	return ResolvedSource{AdapterID: s.AdapterID, QueryBaseURL: s.QueryBaseURL, Options: s.Options, Token: token}
}
