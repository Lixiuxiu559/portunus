package quota

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// registry is the single catalog for both execution and configuration metadata.
var registry = []Adapter{deepSeekAdapter{}, siliconFlowAdapter{}, openRouterAdapter{}, newAPIAdapter{}, customAdapter{}}

func Descriptors() []Descriptor {
	out := make([]Descriptor, 0, len(registry))
	for _, a := range registry {
		d := a.Describe()
		d.ProviderIDs = append([]string{}, d.ProviderIDs...)
		d.CredentialModes = append([]string{}, d.CredentialModes...)
		d.Fields = append([]Field{}, d.Fields...)
		d.RequestMethods = append([]string{}, d.RequestMethods...)
		out = append(out, d)
	}
	return out
}
func LookupAdapter(id string) (Adapter, bool) {
	for _, a := range registry {
		if a.Describe().ID == id {
			return a, true
		}
	}
	return nil, false
}

func validateBase(base string, d Descriptor) (string, error) {
	if base == "" {
		base = d.DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("查询地址必须是无凭据、查询参数和片段的 HTTP(S) 根地址")
	}
	if d.DefaultBaseURL != "" && strings.TrimRight(base, "/") != d.DefaultBaseURL {
		return "", fmt.Errorf("此适配器只允许官方查询根地址")
	}
	if u.RawPath != "" || strings.Contains(u.Path, "//") {
		return "", fmt.Errorf("查询地址路径无效")
	}
	for _, p := range strings.Split(u.Path, "/") {
		if p == "." || p == ".." {
			return "", fmt.Errorf("查询地址路径无效")
		}
	}
	return strings.TrimRight(base, "/"), nil
}
func validateAdapterSource(s Source, d Descriptor) error {
	if _, err := validateBase(s.QueryBaseURL, d); err != nil {
		return err
	}
	allowed := false
	for _, mode := range d.CredentialModes {
		allowed = allowed || s.CredentialMode == mode
	}
	if !allowed {
		return fmt.Errorf("不支持此凭据模式")
	}
	if d.ID != "custom.balance" && s.Options.Custom != nil {
		return fmt.Errorf("此适配器不接受自定义查询配置")
	}
	if d.ID != "newapi.account" && d.ID != "custom.balance" && s.Options != (QueryOptions{}) {
		return fmt.Errorf("此适配器不接受额外参数")
	}
	return nil
}
func requestFor(s ResolvedSource, d Descriptor, path string) (RequestSpec, error) {
	base, err := validateBase(s.QueryBaseURL, d)
	if err != nil {
		return RequestSpec{}, err
	}
	if strings.TrimSpace(s.Token) == "" || strings.ContainsAny(s.Token, "\r\n") {
		return RequestSpec{}, fmt.Errorf("查询凭据无效")
	}
	return RequestSpec{URL: base + path, Method: http.MethodGet, Headers: http.Header{"Authorization": {"Bearer " + s.Token}, "Accept": {"application/json"}}}, nil
}
func validateNewAPIOptions(o QueryOptions) error {
	id, err := strconv.ParseInt(o.UserID, 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("用户 ID 必须是正整数")
	}
	if o.QuotaPerUnit == "" {
		if o.DisplayUnit != "" {
			return fmt.Errorf("展示单位需要同时配置换算比例")
		}
		return nil
	}
	r, err := parseNewAPIQuotaPerUnit(o.QuotaPerUnit)
	if err != nil || r.Sign() <= 0 {
		return fmt.Errorf("每单位额度必须是正数或有效正数分数")
	}
	if !validNewAPIDisplayUnit(o.DisplayUnit) {
		return fmt.Errorf("换算时必须填写有效展示单位")
	}
	return nil
}
