package quota

import (
	"encoding/json"
	"math/big"
	"net/http"
)

type newAPIAdapter struct{}

func (newAPIAdapter) Describe() Descriptor {
	return Descriptor{ID: "newapi.account", Name: "New API 钱包与订阅", Description: "使用独立账号 Access Token 查询钱包和生效中订阅；按站点配置显式换算额度", ProviderIDs: []string{"newapi"}, CredentialModes: []string{"dedicated"}, Fields: []Field{{Key: "user_id", Label: "用户 ID", Required: true}, {Key: "quota_per_unit", Label: "每展示单位对应额度", Help: "可选正数，按站点配置填写，无默认换算"}, {Key: "display_unit", Label: "展示单位", Help: "填写换算比例时必填，例如 CNY 或 credits"}}, SourceURL: "https://docs.newapi.pro/en/docs/api/management/user-management/user-self-get"}
}
func (a newAPIAdapter) Validate(s Source) error {
	if err := validateAdapterSource(s, a.Describe()); err != nil {
		return err
	}
	return validateNewAPIOptions(s.Options)
}
func (a newAPIAdapter) BuildRequest(s ResolvedSource) (RequestSpec, error) {
	if err := validateNewAPIOptions(s.Options); err != nil {
		return RequestSpec{}, err
	}
	r, err := requestFor(s, a.Describe(), "/api/user/self")
	if err == nil {
		r.Headers.Set("New-Api-User", s.Options.UserID)
	}
	return r, err
}
func (newAPIAdapter) Parse(s ResolvedSource, _ http.Header, b []byte) (*Snapshot, *Failure) {
	if validateNewAPIOptions(s.Options) != nil {
		return nil, &Failure{Code: "invalid_configuration", Category: "configuration", Message: "额度换算参数无效"}
	}
	var v struct {
		Success *bool `json:"success"`
		Data    struct {
			Quota json.RawMessage `json:"quota"`
			Used  json.RawMessage `json:"used_quota"`
		} `json:"data"`
	}
	if !decodeBody(b, &v) || v.Success == nil {
		return nil, invalidResponse()
	}
	if !*v.Success {
		return nil, businessFailure()
	}
	remaining, e1 := amount(v.Data.Quota)
	used, e2 := amount(v.Data.Used)
	if e1 != nil || e2 != nil {
		return nil, invalidResponse()
	}
	unit := Unit{Kind: "count", Code: "QUOTA"}
	var original *OriginalAmounts
	if s.Options.QuotaPerUnit != "" {
		original = &OriginalAmounts{Unit: unit, Remaining: remaining, Used: used}
		ratio, _ := parseDecimal(s.Options.QuotaPerUnit)
		r, _ := parseDecimal(*remaining)
		u, _ := parseDecimal(*used)
		rr := decimalString(new(big.Rat).Quo(r, ratio))
		uu := decimalString(new(big.Rat).Quo(u, ratio))
		remaining = &rr
		used = &uu
		unit.Code = s.Options.DisplayUnit
		if unit.Code == "USD" || unit.Code == "CNY" {
			unit.Kind = "money"
		}
	}
	item := balanceItem("account_quota", "钱包额度", unit.Code, remaining)
	item.Unit = unit
	item.Used = used
	item.Original = original
	return &Snapshot{Items: []Item{item}}, nil
}
