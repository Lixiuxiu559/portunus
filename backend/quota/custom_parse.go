package quota

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

func customDecode(body []byte) (any, bool) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil, false
	}
	var extra any
	return v, d.Decode(&extra) == io.EOF
}
func validPointer(p string) bool {
	if p == "" {
		return true
	}
	if !strings.HasPrefix(p, "/") || len(p) > 1024 || hasControl(p) {
		return false
	}
	segments := strings.Split(p[1:], "/")
	if len(segments) > 32 {
		return false
	}
	for _, s := range segments {
		for i := 0; i < len(s); i++ {
			if s[i] == '~' {
				if i+1 == len(s) || (s[i+1] != '0' && s[i+1] != '1') {
					return false
				}
				i++
			}
		}
	}
	return true
}
func pointerValue(root any, p string) (any, bool) {
	if p == "" {
		return root, true
	}
	current := root
	for _, raw := range strings.Split(p[1:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch value := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = value[key]
			if !ok {
				return nil, false
			}
		case []any:
			if key == "" || (len(key) > 1 && key[0] == '0') || strings.ContainsFunc(key, func(r rune) bool { return r < '0' || r > '9' }) {
				return nil, false
			}
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(value) {
				return nil, false
			}
			current = value[i]
		default:
			return nil, false
		}
	}
	return current, true
}
func conditionMatches(root any, c *CustomCondition) bool {
	if c == nil {
		return false
	}
	actual, exists := pointerValue(root, c.Path)
	if !exists {
		return false
	}
	expected, ok := customDecode([]byte(c.EqualsJSON))
	if !ok {
		return false
	}
	switch e := expected.(type) {
	case nil:
		return actual == nil
	case bool:
		a, ok := actual.(bool)
		return ok && a == e
	case string:
		a, ok := actual.(string)
		return ok && a == e
	case json.Number:
		a, ok := actual.(json.Number)
		if !ok {
			return false
		}
		ar, err := parseDecimal(string(a))
		er, eerr := parseDecimal(string(e))
		return err == nil && eerr == nil && ar.Cmp(er) == 0
	}
	return false
}
func mappedAmount(root any, path string) (*string, bool) {
	if path == "" {
		return nil, true
	}
	v, exists := pointerValue(root, path)
	if !exists || v == nil {
		return nil, true
	}
	var text string
	switch value := v.(type) {
	case json.Number:
		text = string(value)
	case string:
		text = value
	default:
		return nil, false
	}
	r, err := parseDecimal(text)
	if err != nil {
		return nil, false
	}
	s := decimalString(r)
	return &s, len(s) <= 128
}
func parseCustomItem(root any, c CustomItem) (Item, bool) {
	i := Item{ID: c.ID, Label: c.Label, Kind: c.Kind, Scope: c.Scope, Unit: c.Unit, LimitState: "unknown"}
	var ok bool
	if i.Remaining, ok = mappedAmount(root, c.RemainingPath); !ok {
		return i, false
	}
	if i.Used, ok = mappedAmount(root, c.UsedPath); !ok {
		return i, false
	}
	if i.Total, ok = mappedAmount(root, c.TotalPath); !ok {
		return i, false
	}
	if c.RemainingFromTotalUsed && i.Total != nil && i.Used != nil {
		t, _ := parseDecimal(*i.Total)
		u, _ := parseDecimal(*i.Used)
		s := decimalString(new(big.Rat).Sub(t, u))
		i.Remaining = &s
	}
	if conditionMatches(root, c.Unlimited) {
		i.LimitState = "unlimited"
	} else if i.Total != nil {
		i.LimitState = "limited"
	}
	if c.Divisor != "" && (i.Remaining != nil || i.Used != nil || i.Total != nil) {
		i.Original = &OriginalAmounts{Unit: *c.OriginalUnit, Remaining: i.Remaining, Used: i.Used, Total: i.Total}
		divisor, _ := parseDecimal(c.Divisor)
		for _, field := range []**string{&i.Remaining, &i.Used, &i.Total} {
			if *field != nil {
				r, _ := parseDecimal(**field)
				s := decimalString(new(big.Rat).Quo(r, divisor))
				*field = &s
			}
		}
	}
	return i, true
}
func (customAdapter) Parse(s ResolvedSource, _ http.Header, body []byte) (*Snapshot, *Failure) {
	if validateCustom(s.Options.Custom) != nil {
		return nil, &Failure{Code: "invalid_configuration", Category: "configuration", Message: "自定义查询配置无效"}
	}
	root, ok := customDecode(body)
	if !ok {
		return nil, invalidResponse()
	}
	c := s.Options.Custom
	if c.Success != nil && !conditionMatches(root, c.Success) {
		return nil, businessFailure()
	}
	snapshot := &Snapshot{}
	for _, mapping := range c.Items {
		item, ok := parseCustomItem(root, mapping)
		if !ok {
			return nil, invalidResponse()
		}
		snapshot.Items = append(snapshot.Items, item)
	}
	if !validateSnapshot(snapshot) {
		return nil, invalidResponse()
	}
	return snapshot, nil
}
