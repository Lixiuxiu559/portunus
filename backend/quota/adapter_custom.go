package quota

import (
	"fmt"
	"net/http"
	"strings"
)

type customAdapter struct{}

func (customAdapter) Describe() Descriptor {
	return Descriptor{ID: "custom.balance", Name: "自定义余额 / 额度", Description: "声明一次 JSON 查询与字段映射，不执行脚本", CredentialModes: []string{"inherit_channel_key", "dedicated", "none"}, RequestMethods: []string{http.MethodGet, http.MethodPost}}
}
func (a customAdapter) Validate(s Source) error {
	if err := validateAdapterSource(s, a.Describe()); err != nil {
		return err
	}
	if s.Options.UserID != "" || s.Options.QuotaPerUnit != "" || s.Options.DisplayUnit != "" {
		return fmt.Errorf("自定义查询不接受预置适配器参数")
	}
	base, err := validateBase(s.QueryBaseURL, a.Describe())
	if err != nil || strings.ContainsAny(base, "\\\r\n\t") {
		return fmt.Errorf("查询地址无效")
	}
	if err := validateCustom(s.Options.Custom); err != nil {
		return err
	}
	if s.CredentialMode == CredentialNone {
		for _, value := range s.Options.Custom.Request.Headers {
			if strings.Contains(value, "{{token}}") {
				return fmt.Errorf("无鉴权模式不能引用查询凭据")
			}
		}
	}
	return nil
}
func (a customAdapter) BuildRequest(s ResolvedSource) (RequestSpec, error) {
	if err := a.Validate(Source{QueryBaseURL: s.QueryBaseURL, CredentialMode: "dedicated", Options: s.Options}); err != nil {
		return RequestSpec{}, err
	}
	needsToken := false
	for _, value := range s.Options.Custom.Request.Headers {
		needsToken = needsToken || strings.Contains(value, "{{token}}")
	}
	if needsToken && (strings.TrimSpace(s.Token) == "" || hasControl(s.Token)) {
		return RequestSpec{}, fmt.Errorf("查询凭据无效")
	}
	base, err := validateBase(s.QueryBaseURL, a.Describe())
	if err != nil {
		return RequestSpec{}, err
	}
	if strings.ContainsAny(base, "\\\r\n\t") {
		return RequestSpec{}, fmt.Errorf("查询地址无效")
	}
	c := s.Options.Custom
	headers := http.Header{"Accept": {"application/json"}}
	size := 0
	for k, v := range c.Request.Headers {
		expanded := strings.ReplaceAll(v, "{{token}}", s.Token)
		size += len(k) + len(expanded)
		if len(expanded) > 4096 || size > 16384 {
			return RequestSpec{}, fmt.Errorf("查询请求头超出大小限制")
		}
		headers.Set(k, expanded)
	}
	if c.Request.Method == http.MethodPost {
		headers.Set("Content-Type", "application/json")
	}
	return RequestSpec{URL: base + c.Request.Path, Method: c.Request.Method, Headers: headers, Body: []byte(c.Request.Body)}, nil
}
