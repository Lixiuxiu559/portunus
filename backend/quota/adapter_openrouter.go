package quota

import (
	"encoding/json"
	"math/big"
	"net/http"
)

type openRouterAdapter struct{}

func (openRouterAdapter) Describe() Descriptor {
	return Descriptor{ID: "openrouter.account_balance", Name: "OpenRouter 账户余额", Description: "账户级余额，需要 Management Key；普通推理 Key 可能无查询权限", ProviderIDs: []string{"openrouter"}, DefaultBaseURL: "https://openrouter.ai/api/v1", CredentialModes: []string{"dedicated"}, Fields: []Field{}, SourceURL: "https://openrouter.ai/docs/api/api-reference/credits/get-credits"}
}
func (a openRouterAdapter) Validate(s Source) error { return validateAdapterSource(s, a.Describe()) }
func (a openRouterAdapter) BuildRequest(s ResolvedSource) (RequestSpec, error) {
	return requestFor(s, a.Describe(), "/credits")
}
func (openRouterAdapter) Parse(_ ResolvedSource, _ http.Header, b []byte) (*Snapshot, *Failure) {
	var v struct {
		Data struct {
			Total json.RawMessage `json:"total_credits"`
			Used  json.RawMessage `json:"total_usage"`
		} `json:"data"`
	}
	if !decodeBody(b, &v) {
		return nil, invalidResponse()
	}
	total, e1 := amount(v.Data.Total)
	used, e2 := amount(v.Data.Used)
	if e1 != nil || e2 != nil {
		return nil, invalidResponse()
	}
	t, _ := parseDecimal(*total)
	u, _ := parseDecimal(*used)
	remaining := decimalString(new(big.Rat).Sub(t, u))
	item := balanceItem("account_balance", "账户余额", "USD", &remaining)
	item.Total = total
	item.Used = used
	item.LimitState = "limited"
	return &Snapshot{Items: []Item{item}}, nil
}
