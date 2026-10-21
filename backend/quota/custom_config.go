package quota

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const maxCustomBodyBytes = 16 << 10

var customIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func customConfigError() error {
	return fmt.Errorf("自定义查询配置无效，请检查请求和字段映射")
}
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) })
}
func hasTemplate(s string) bool { return strings.Contains(s, "{{") || strings.Contains(s, "}}") }
func sensitiveName(s string) bool {
	s = strings.ToLower(s)
	for _, part := range []string{"authorization", "auth", "cookie", "key", "token", "secret", "password", "credential", "signature", "session", "jwt"} {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}
func validateCustomPath(path string) bool {
	if path == "" || len(path) > 2048 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\#") || hasControl(path) || hasTemplate(path) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Opaque != "" {
		return false
	}
	decoded, err := url.PathUnescape(u.EscapedPath())
	if err != nil || strings.ContainsAny(decoded, "\\") || hasControl(decoded) || strings.Contains(decoded, "//") || hasTemplate(decoded) {
		return false
	}
	for _, part := range strings.Split(decoded, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for k, values := range q {
		if sensitiveName(k) || hasControl(k) || hasTemplate(k) {
			return false
		}
		for _, v := range values {
			if hasControl(v) || hasTemplate(v) {
				return false
			}
		}
	}
	return true
}
func validateCustomHeaders(headers map[string]string) bool {
	if len(headers) > 32 {
		return false
	}
	size := 0
	seen := map[string]bool{}
	for name, value := range headers {
		canonical := http.CanonicalHeaderKey(name)
		size += len(name) + len(value)
		if !headerNamePattern.MatchString(name) || seen[canonical] || hasControl(value) || len(value) > 4096 {
			return false
		}
		seen[canonical] = true
		switch canonical {
		case "Host", "Connection", "Content-Length", "Transfer-Encoding", "Trailer", "Upgrade", "Te", "Proxy-Authorization", "Proxy-Connection", "Proxy-Authenticate", "Keep-Alive", "Content-Encoding", "Expect", "Accept-Encoding":
			return false
		}
		plain := strings.ReplaceAll(value, "{{token}}", "")
		if hasTemplate(plain) || (sensitiveName(name) && !strings.Contains(value, "{{token}}")) {
			return false
		}
		if canonical == "Content-Type" && value != "application/json" {
			return false
		}
	}
	return size <= 16384
}
func validCustomUnit(u Unit) bool {
	return (u.Kind == "money" || u.Kind == "count" || u.Kind == "tokens" || u.Kind == "percent") && len(u.Code) > 0 && len(u.Code) <= 24 && strings.TrimSpace(u.Code) == u.Code && !hasControl(u.Code)
}
func validateCustomCondition(c *CustomCondition) bool {
	if c == nil {
		return true
	}
	if !validPointer(c.Path) {
		return false
	}
	v, ok := customDecode([]byte(c.EqualsJSON))
	if !ok {
		return false
	}
	switch v.(type) {
	case nil, bool, string:
		return true
	case json.Number:
		_, err := parseDecimal(string(v.(json.Number)))
		return err == nil
	default:
		return false
	}
}
func validateCustom(c *CustomConfig) error {
	if c == nil || !validateCustomPath(c.Request.Path) || !validateCustomHeaders(c.Request.Headers) || !validateCustomCondition(c.Success) || len(c.Items) == 0 || len(c.Items) > 20 {
		return customConfigError()
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodPost {
		return customConfigError()
	}
	if len(c.Request.Body) > maxCustomBodyBytes || hasTemplate(c.Request.Body) {
		return customConfigError()
	}
	if c.Request.Method == http.MethodGet && c.Request.Body != "" {
		return customConfigError()
	}
	if c.Request.Body != "" && !json.Valid([]byte(c.Request.Body)) {
		return customConfigError()
	}
	seen := map[string]bool{}
	for _, item := range c.Items {
		if !customIDPattern.MatchString(item.ID) || seen[item.ID] || item.Label == "" || len(item.Label) > 256 || hasControl(item.Label) || !validCustomUnit(item.Unit) || !validateCustomCondition(item.Unlimited) {
			return customConfigError()
		}
		seen[item.ID] = true
		if item.Kind != "balance" && item.Kind != "spending_limit" && item.Kind != "usage_window" {
			return customConfigError()
		}
		if item.Scope != "account" && item.Scope != "api_key" && item.Scope != "plan" {
			return customConfigError()
		}
		for _, path := range []string{item.RemainingPath, item.UsedPath, item.TotalPath} {
			if path != "" && !validPointer(path) {
				return customConfigError()
			}
		}
		if item.RemainingPath == "" && item.UsedPath == "" && item.TotalPath == "" && item.Unlimited == nil {
			return customConfigError()
		}
		if item.RemainingFromTotalUsed && (item.RemainingPath != "" || item.TotalPath == "" || item.UsedPath == "") {
			return customConfigError()
		}
		if item.Divisor != "" {
			r, err := parseDecimal(item.Divisor)
			if err != nil || r.Sign() <= 0 || item.OriginalUnit == nil || !validCustomUnit(*item.OriginalUnit) {
				return customConfigError()
			}
		} else if item.OriginalUnit != nil {
			return customConfigError()
		}
	}
	return nil
}
