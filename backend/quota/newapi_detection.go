package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// DetectNewAPIUnits reads public display settings only. It never loads credentials,
// saves a source, or requests account balances.
func (s *Service) DetectNewAPIUnits(ctx context.Context, base string) (QueryOptions, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	base, err := validateBase(base, newAPIAdapter{}.Describe())
	if err != nil {
		return QueryOptions{}, statusError(400, "请填写有效的 NewAPI 查询根地址")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return QueryOptions{}, statusError(429, "额度查询并发已满，请稍后重试")
	}
	budget := maxResponseBytes
	body, _, _, failure := executeRequest(ctx, s.client, newAPIAdapter{}.Describe(), RequestSpec{
		URL: base + "/api/status", Method: http.MethodGet, Headers: http.Header{"Accept": {"application/json"}},
	}, &budget)
	if failure != nil {
		return QueryOptions{}, statusError(422, "无法自动识别："+failure.Message+"；已保留手动配置")
	}
	options, err := detectNewAPIUnits(body)
	if err != nil {
		return QueryOptions{}, statusError(422, "站点未提供完整有效的额度单位配置，请保留手动配置")
	}
	return options, nil
}

func detectNewAPIUnits(body []byte) (QueryOptions, error) {
	var response struct {
		Success *bool `json:"success"`
		Data    struct {
			QuotaPerUnit       json.RawMessage `json:"quota_per_unit"`
			DisplayType        string          `json:"quota_display_type"`
			USDExchangeRate    json.RawMessage `json:"usd_exchange_rate"`
			CustomSymbol       string          `json:"custom_currency_symbol"`
			CustomExchangeRate json.RawMessage `json:"custom_currency_exchange_rate"`
		} `json:"data"`
	}
	invalid := fmt.Errorf("缺少有效站点单位配置")
	if !decodeBody(body, &response) || response.Success == nil || !*response.Success {
		return QueryOptions{}, invalid
	}
	quota, err := positiveStatusAmount(response.Data.QuotaPerUnit)
	if err != nil {
		return QueryOptions{}, invalid
	}
	var unit string
	switch response.Data.DisplayType {
	case "USD":
		unit = "USD"
	case "CNY", "CUSTOM":
		raw := response.Data.USDExchangeRate
		unit = "CNY"
		if response.Data.DisplayType == "CUSTOM" {
			raw, unit = response.Data.CustomExchangeRate, response.Data.CustomSymbol
		}
		rate, err := positiveStatusAmount(raw)
		if err != nil || !validNewAPIDisplayUnit(unit) {
			return QueryOptions{}, invalid
		}
		quota.Quo(quota, rate)
	case "TOKENS":
		// The site displays raw quota counts; those are not measured model tokens.
		unit = "QUOTA"
		quota.SetInt64(1)
	default:
		// display_in_currency alone cannot distinguish USD, CNY and custom units.
		return QueryOptions{}, invalid
	}
	ratio := newAPIQuotaRatioString(quota)
	if _, err := parseNewAPIQuotaPerUnit(ratio); err != nil {
		return QueryOptions{}, invalid
	}
	return QueryOptions{QuotaPerUnit: ratio, DisplayUnit: unit}, nil
}

func positiveStatusAmount(raw json.RawMessage) (*big.Rat, error) {
	value, err := amount(raw)
	if err != nil {
		return nil, err
	}
	valueRat, err := parseDecimal(*value)
	if err != nil || valueRat.Sign() <= 0 {
		return nil, fmt.Errorf("换算参数必须是正数")
	}
	return valueRat, nil
}

// NewAPI accepts exact fractions for display conversion without rounding an
// intermediate divisor (for example 500000/7 for CNY). Other adapters keep the
// original decimal-only parser.
func parseNewAPIQuotaPerUnit(value string) (*big.Rat, error) {
	if len(value) == 0 || len(value) > 128 {
		return nil, fmt.Errorf("换算比例无效")
	}
	parts := strings.Split(value, "/")
	if len(parts) > 2 {
		return nil, fmt.Errorf("换算比例无效")
	}
	numerator, err := parseDecimal(parts[0])
	if err != nil || numerator.Sign() <= 0 {
		return nil, fmt.Errorf("换算比例必须是正数")
	}
	if len(parts) == 1 {
		return numerator, nil
	}
	denominator, err := parseDecimal(parts[1])
	if err != nil || denominator.Sign() <= 0 {
		return nil, fmt.Errorf("换算比例必须是正数")
	}
	return numerator.Quo(numerator, denominator), nil
}

func newAPIQuotaRatioString(ratio *big.Rat) string {
	den := new(big.Int).Set(ratio.Denom())
	for _, prime := range []int64{2, 5} {
		factor := big.NewInt(prime)
		for new(big.Int).Mod(den, factor).Sign() == 0 {
			den.Quo(den, factor)
		}
	}
	if den.Cmp(big.NewInt(1)) == 0 {
		return decimalString(ratio)
	}
	return ratio.RatString()
}

func validNewAPIDisplayUnit(unit string) bool {
	return len(unit) > 0 && len(unit) <= 24 && strings.TrimSpace(unit) == unit && !strings.ContainsAny(unit, "\r\n\t")
}
