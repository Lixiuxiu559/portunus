package quota

import (
	"encoding/json"
	"net/http"
)

type siliconFlowAdapter struct{}

func (siliconFlowAdapter) Describe() Descriptor {
	return Descriptor{ID: "siliconflow.balance", Name: "SiliconFlow 账户余额", Description: "中国站账户总可用余额（含充值与赠金）", ProviderIDs: []string{"siliconflow"}, DefaultBaseURL: "https://api.siliconflow.cn/v1", CredentialModes: []string{"inherit_channel_key", "dedicated"}, Fields: []Field{}, SourceURL: "https://siliconflow.readme.io/reference/user-info"}
}
func (a siliconFlowAdapter) Validate(s Source) error { return validateAdapterSource(s, a.Describe()) }
func (a siliconFlowAdapter) BuildRequest(s ResolvedSource) (RequestSpec, error) {
	return requestFor(s, a.Describe(), "/user/info")
}
func (siliconFlowAdapter) Parse(_ ResolvedSource, _ http.Header, b []byte) (*Snapshot, *Failure) {
	var v struct {
		Code   *int  `json:"code"`
		Status *bool `json:"status"`
		Data   struct {
			Total json.RawMessage `json:"totalBalance"`
		} `json:"data"`
	}
	if !decodeBody(b, &v) || v.Code == nil || v.Status == nil {
		return nil, invalidResponse()
	}
	if *v.Code != 20000 || !*v.Status {
		return nil, businessFailure()
	}
	n, err := amount(v.Data.Total)
	if err != nil {
		return nil, invalidResponse()
	}
	return &Snapshot{Items: []Item{balanceItem("balance_CNY", "账户余额", "CNY", n)}}, nil
}
