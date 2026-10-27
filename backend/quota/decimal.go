package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Numbers are limited to 128 input/output characters and exponent magnitude 128.
var decimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

func parseDecimal(s string) (*big.Rat, error) {
	if len(s) == 0 || len(s) > 128 || !decimalPattern.MatchString(s) {
		return nil, fmt.Errorf("无效数值")
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exp, err := strconv.Atoi(s[i+1:])
		if err != nil || exp < -128 || exp > 128 {
			return nil, fmt.Errorf("数值超限")
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("无效数值")
	}
	return r, nil
}

// decimalString preserves terminating decimal values exactly; repeating quotients
// are rounded to 18 fractional digits (half away from zero), solely for
// observation display. Values below this precision may round to zero; finite
// decimal quotients retain all digits within the snapshot size bound. No result
// is used for billing or routing, and no arithmetic passes through binary floats.
func decimalString(r *big.Rat) string {
	den := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	count2, count5 := 0, 0
	for new(big.Int).Mod(den, two).Sign() == 0 {
		den.Div(den, two)
		count2++
	}
	for new(big.Int).Mod(den, five).Sign() == 0 {
		den.Div(den, five)
		count5++
	}
	digits := 18
	if den.Cmp(big.NewInt(1)) == 0 {
		digits = max(count2, count5)
	}
	s := r.FloatString(digits)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}
func amount(raw json.RawMessage) (*string, error) {
	var s string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
	} else {
		s = string(raw)
	}
	r, err := parseDecimal(s)
	if err != nil {
		return nil, err
	}
	value := decimalString(r)
	if len(value) > 128 {
		return nil, fmt.Errorf("数值超限")
	}
	return &value, nil
}
func decodeBody(body []byte, value any) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	if d.Decode(value) != nil {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}
func invalidResponse() *Failure {
	return &Failure{Code: "invalid_response", Category: "invalid_response", Message: "上游返回的额度数据无效或缺少必要字段"}
}
func businessFailure() *Failure {
	return &Failure{Code: "upstream_business_error", Category: "business", Message: "上游未返回成功的额度查询结果"}
}
func balanceItem(id, label, code string, remaining *string) Item {
	return Item{ID: id, Label: label, Kind: "balance", Scope: "account", Unit: Unit{Kind: "money", Code: code}, Remaining: remaining, LimitState: "unknown"}
}
func validateSnapshot(s *Snapshot) bool {
	maxItems := 100
	if s != nil && s.NewAPI != nil {
		maxItems++ // 100 subscriptions plus the independently queried wallet.
	}
	if s == nil || len(s.Items) > maxItems || (len(s.Items) == 0 && (s.NewAPI == nil || (s.NewAPI.Wallet.Status != "ok" && s.NewAPI.Subscriptions.Status != "ok"))) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range s.Items {
		if item.ID == "" || len(item.ID) > 128 || seen[item.ID] || item.Label == "" || len(item.Label) > 256 {
			return false
		}
		seen[item.ID] = true
		if item.Kind != "balance" && item.Kind != "spending_limit" && item.Kind != "usage_window" {
			return false
		}
		if item.Scope != "account" && item.Scope != "api_key" && item.Scope != "plan" {
			return false
		}
		if item.Unit.Kind != "money" && item.Unit.Kind != "count" && item.Unit.Kind != "tokens" && item.Unit.Kind != "percent" {
			return false
		}
		if item.Unit.Code == "" || len(item.Unit.Code) > 24 {
			return false
		}
		if item.LimitState != "unknown" && item.LimitState != "limited" && item.LimitState != "unlimited" {
			return false
		}
		if item.Remaining == nil && item.Used == nil && item.Total == nil && item.UsedPercent == nil && item.LimitState != "unlimited" {
			return false
		}
		for _, v := range []*string{item.Remaining, item.Used, item.Total} {
			if v != nil {
				if _, err := parseDecimal(*v); err != nil {
					return false
				}
			}
		}
		if original := item.Original; original != nil {
			if original.Unit.Kind != "money" && original.Unit.Kind != "count" && original.Unit.Kind != "tokens" && original.Unit.Kind != "percent" {
				return false
			}
			if original.Unit.Code == "" || len(original.Unit.Code) > 24 || (original.Remaining == nil && original.Used == nil && original.Total == nil) {
				return false
			}
			for _, value := range []*string{original.Remaining, original.Used, original.Total} {
				if value != nil {
					if _, err := parseDecimal(*value); err != nil {
						return false
					}
				}
			}
		}
		if item.UsedPercent != nil && (math.IsNaN(*item.UsedPercent) || math.IsInf(*item.UsedPercent, 0)) {
			return false
		}
		if item.AccountStatus != "" && item.AccountStatus != "unknown" && item.AccountStatus != "active" && item.AccountStatus != "expired" && item.AccountStatus != "suspended" {
			return false
		}
	}
	return true
}
