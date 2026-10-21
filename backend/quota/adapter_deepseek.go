package quota

import (
	"encoding/json"
	"net/http"
)

type deepSeekAdapter struct{}

func (deepSeekAdapter) Describe() Descriptor {
	return Descriptor{ID: "deepseek.balance", Name: "DeepSeek 账户余额", Description: "官方账户余额，按币种分别展示；零余额不代表 Key 失效", ProviderIDs: []string{"deepseek"}, DefaultBaseURL: "https://api.deepseek.com", CredentialModes: []string{"inherit_channel_key", "dedicated"}, Fields: []Field{}, SourceURL: "https://api-docs.deepseek.com/zh-cn/api/get-user-balance/"}
}
func (a deepSeekAdapter) Validate(s Source) error { return validateAdapterSource(s, a.Describe()) }
func (a deepSeekAdapter) BuildRequest(s ResolvedSource) (RequestSpec, error) {
	return requestFor(s, a.Describe(), "/user/balance")
}
func (deepSeekAdapter) Parse(_ ResolvedSource, _ http.Header, b []byte) (*Snapshot, *Failure) {
	var v struct {
		Available *bool `json:"is_available"`
		Infos     []struct {
			Currency string          `json:"currency"`
			Total    json.RawMessage `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if !decodeBody(b, &v) || v.Available == nil || len(v.Infos) == 0 {
		return nil, invalidResponse()
	}
	out := &Snapshot{}
	seen := map[string]bool{}
	for _, i := range v.Infos {
		if (i.Currency != "USD" && i.Currency != "CNY") || seen[i.Currency] {
			return nil, invalidResponse()
		}
		seen[i.Currency] = true
		n, err := amount(i.Total)
		if err != nil {
			return nil, invalidResponse()
		}
		out.Items = append(out.Items, balanceItem("balance_"+i.Currency, i.Currency+" 账户余额", i.Currency, n))
	}
	return out, nil
}
