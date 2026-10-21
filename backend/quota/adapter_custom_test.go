package quota

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func customFixture() *CustomConfig {
	return &CustomConfig{Request: CustomRequest{Method: "GET", Path: "/api/balance?currency=USD", Headers: map[string]string{"Authorization": "Bearer {{token}}"}}, Success: &CustomCondition{Path: "/success", EqualsJSON: `true`}, Items: []CustomItem{{ID: "wallet", Label: "余额", Kind: "balance", Scope: "account", Unit: Unit{Kind: "money", Code: "USD"}, RemainingPath: "/data/balance"}}}
}
func customSource(c *CustomConfig) ResolvedSource {
	return ResolvedSource{QueryBaseURL: "https://example.test", Token: "fixture-secret", Options: QueryOptions{Custom: c}}
}
func TestCustomParseExactValuesAndPointers(t *testing.T) {
	c := customFixture()
	c.Items[0].RemainingPath = "/data/a~1b/~0wallet/0"
	s, f := (customAdapter{}).Parse(customSource(c), nil, []byte(`{"success":true,"data":{"a/b":{"~wallet":[9007199254740993.0001]}}}`))
	if f != nil || *s.Items[0].Remaining != "9007199254740993.0001" {
		t.Fatalf("snapshot=%+v failure=%+v", s, f)
	}
	for _, tc := range []struct{ body, want string }{{`{"success":true,"data":{"balance":-0}}`, "0"}, {`{"success":true,"data":{"balance":"-1.20"}}`, "-1.2"}} {
		s, f = (customAdapter{}).Parse(customSource(customFixture()), nil, []byte(tc.body))
		if f != nil || *s.Items[0].Remaining != tc.want {
			t.Fatalf("%s: %+v", tc.body, f)
		}
	}
}
func TestCustomParseMultipleItemsConvertAndUnlimited(t *testing.T) {
	c := customFixture()
	c.Success = &CustomCondition{Path: "/code", EqualsJSON: `200.0`}
	c.Items[0].RemainingPath = ""
	c.Items[0].TotalPath = "/data/total"
	c.Items[0].UsedPath = "/data/used"
	c.Items[0].RemainingFromTotalUsed = true
	c.Items[0].Divisor = "100"
	c.Items[0].OriginalUnit = &Unit{Kind: "count", Code: "QUOTA"}
	c.Items = append(c.Items, CustomItem{ID: "limit", Label: "额度", Kind: "spending_limit", Scope: "api_key", Unit: Unit{Kind: "tokens", Code: "tokens"}, Unlimited: &CustomCondition{Path: "/unlimited", EqualsJSON: `true`}})
	s, f := (customAdapter{}).Parse(customSource(c), nil, []byte(`{"code":200,"data":{"total":"100","used":"125"},"unlimited":true}`))
	if f != nil {
		t.Fatal(f)
	}
	i := s.Items[0]
	if *i.Remaining != "-0.25" || *i.Original.Remaining != "-25" || *i.Original.Total != "100" || s.Items[1].LimitState != "unlimited" {
		t.Fatalf("%+v %+v", i, s.Items[1])
	}
}
func TestCustomParseMissingNullAndInvalid(t *testing.T) {
	for _, value := range []string{`null`, `true`, `{}`, `[]`, `"NaN"`, `""`, `"1/2"`, `1e129`} {
		s, f := (customAdapter{}).Parse(customSource(customFixture()), nil, []byte(`{"success":true,"data":{"balance":`+value+`}}`))
		if f == nil || s != nil {
			t.Fatalf("accepted %s", value)
		}
	}
	c := customFixture()
	c.Items[0].TotalPath = "/data/total"
	s, f := (customAdapter{}).Parse(customSource(c), nil, []byte(`{"success":true,"data":{"balance":null,"total":0}}`))
	if f != nil || s.Items[0].Remaining != nil || *s.Items[0].Total != "0" {
		t.Fatal(s, f)
	}
	s, f = (customAdapter{}).Parse(customSource(c), nil, []byte(`{"success":true,"data":{"total":0}}`))
	if f != nil || s.Items[0].Remaining != nil {
		t.Fatal(s, f)
	}
	for _, body := range []string{`{"success":false,"message":"fixture-secret"}`, `{"success":"true"}`, `{}`, `{} {}`, `<html>`} {
		_, f = (customAdapter{}).Parse(customSource(c), nil, []byte(body))
		if f == nil || strings.Contains(f.Message, "fixture-secret") {
			t.Fatal(f)
		}
	}
}
func TestCustomConditionsAndPointers(t *testing.T) {
	root, _ := customDecode([]byte(`{"n":9007199254740993,"null":null,"s":"x","b":false}`))
	for _, tc := range []struct {
		path, equals string
		want         bool
	}{{"/n", `9007199254740993.0`, true}, {"/n", `9007199254740992`, false}, {"/null", `null`, true}, {"/missing", `null`, false}, {"/s", `"x"`, true}, {"/b", `false`, true}, {"/s", `true`, false}} {
		if got := conditionMatches(root, &CustomCondition{Path: tc.path, EqualsJSON: tc.equals}); got != tc.want {
			t.Fatal(tc)
		}
	}
	for _, p := range []string{"data.x", "$.data.balance", "/x~2", "/x~", strings.Repeat("/x", 33), "/x\n"} {
		if validPointer(p) {
			t.Fatalf("accepted pointer %q", p)
		}
	}
	for _, equals := range []string{`{}`, `[]`, `1 2`, `NaN`} {
		if validateCustomCondition(&CustomCondition{Path: "/x", EqualsJSON: equals}) {
			t.Fatal(equals)
		}
	}
}
func TestCustomRejectsUnsafeRequests(t *testing.T) {
	for _, path := range []string{"https://evil.test/x", "//evil.test/x", "/\\evil", "/a/../b", "/%2e%2e/b", "/a/%2F/b", "/x#f", "/x?api_key=secret", "/x?Authorization=x", "/x?t%6fken=x", "/x?x=%7B%7Btoken%7D%7D", "/x/{{token}}", "/x?x=%0d%0a", "/x?bad=%xx"} {
		c := customFixture()
		c.Request.Path = path
		if _, err := (customAdapter{}).BuildRequest(customSource(c)); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	for _, headers := range []map[string]string{{"Host": "evil"}, {"X-Bad": "x\r\nY: bad"}, {"X Bad": "x"}, {"Authorization": "Bearer secret"}, {"Cookie": "x=y"}, {"X-Api-Key": "secret"}, {"X-Token": "{{other}}"}, {"Authorization": "{{token}}", "authorization": "{{token}}"}, {"Content-Type": "text/html"}, {"Content-Length": "1"}, {"Connection": "keep-alive"}} {
		c := customFixture()
		c.Request.Headers = headers
		if _, err := (customAdapter{}).BuildRequest(customSource(c)); err == nil {
			t.Fatalf("accepted headers %+v", headers)
		}
	}
	for _, body := range []string{`{"x":"{{token}}"}`, `{`, strings.Repeat(" ", maxCustomBodyBytes+1)} {
		c := customFixture()
		c.Request.Method = "POST"
		c.Request.Body = body
		if validateCustom(c) == nil {
			t.Fatal("accepted body")
		}
	}
	c := customFixture()
	c.Request.Body = `{}`
	if validateCustom(c) == nil {
		t.Fatal("GET accepted body")
	}
	c = customFixture()
	s := customSource(c)
	s.Token = ""
	if _, err := (customAdapter{}).BuildRequest(s); err == nil {
		t.Fatal("empty token")
	}
	c.Request.Headers = nil
	if _, err := (customAdapter{}).BuildRequest(s); err != nil {
		t.Fatal("public query", err)
	}
}
func TestCustomConfigurationBoundsAndIsolation(t *testing.T) {
	mutations := []func(*CustomConfig){func(c *CustomConfig) { c.Items = nil }, func(c *CustomConfig) { c.Items = append(c.Items, c.Items[0]) }, func(c *CustomConfig) { c.Items[0].ID = "a b" }, func(c *CustomConfig) { c.Items[0].Kind = "script" }, func(c *CustomConfig) { c.Items[0].Scope = "all" }, func(c *CustomConfig) { c.Items[0].Divisor = "0" }, func(c *CustomConfig) { c.Items[0].Divisor = "100" }, func(c *CustomConfig) { c.Items[0].RemainingFromTotalUsed = true }, func(c *CustomConfig) { c.Items[0].Unit.Code = "USD\n" }, func(c *CustomConfig) { c.Items[0].RemainingPath = "$.eval()" }}
	for i, mutate := range mutations {
		c := customFixture()
		mutate(c)
		if validateCustom(c) == nil {
			t.Fatalf("accepted mutation %d", i)
		}
	}
	for _, id := range []string{"deepseek.balance", "newapi.account"} {
		a, _ := LookupAdapter(id)
		if a.Validate(Source{CredentialMode: a.Describe().CredentialModes[0], QueryBaseURL: "https://example.test", Options: QueryOptions{Custom: customFixture(), UserID: "1"}}) == nil {
			t.Fatal("native accepts custom", id)
		}
	}
	a, _ := LookupAdapter("custom.balance")
	if a == nil {
		t.Fatal("not registered")
	}
	ds := Descriptors()
	for i := range ds {
		if ds[i].ID == "custom.balance" {
			ds[i].RequestMethods[0] = "DELETE"
		}
	}
	if a.Describe().RequestMethods[0] != http.MethodGet {
		t.Fatal("descriptor mutated")
	}
}

func TestCustomArrayPointersAreCanonicalAndHeadersBounded(t *testing.T) {
	root, _ := customDecode([]byte(`["first"]`))
	for _, p := range []string{"/+0", "/-0", "/00", "/-", "/1"} {
		if _, ok := pointerValue(root, p); ok {
			t.Fatal("noncanonical array index", p)
		}
	}
	if v, ok := pointerValue(root, "/0"); !ok || v != "first" {
		t.Fatal("valid array index")
	}
	c := customFixture()
	s := customSource(c)
	s.Token = strings.Repeat("x", 4097)
	if _, err := (customAdapter{}).BuildRequest(s); err == nil {
		t.Fatal("expanded header limit")
	}
	c.Request.Headers = map[string]string{"X-Api-Key": "{{token}}"}
	s.Token = "test-key"
	r, err := (customAdapter{}).BuildRequest(s)
	if err != nil || r.Headers.Get("X-Api-Key") != "test-key" {
		t.Fatal("token header substitution", err)
	}
}

func TestCustomConditionDecimalSurvivesConfigurationRoundTrip(t *testing.T) {
	const exact = "9007199254740993.123456789"
	c := customFixture()
	c.Success = &CustomCondition{Path: "/code", EqualsJSON: exact}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var restored CustomConfig
	if err = json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Success.EqualsJSON != exact {
		t.Fatal("condition rounded", restored.Success.EqualsJSON)
	}
	root, _ := customDecode([]byte(`{"code":` + exact + `}`))
	if !conditionMatches(root, restored.Success) {
		t.Fatal("exact condition failed after round trip")
	}
	rounded, _ := customDecode([]byte(`{"code":9007199254740994}`))
	if conditionMatches(rounded, restored.Success) {
		t.Fatal("rounded number unexpectedly matched")
	}
	s, f := (customAdapter{}).Parse(customSource(&restored), nil, []byte(`{"code":`+exact+`,"data":{"balance":0}}`))
	if f != nil || s == nil || *s.Items[0].Remaining != "0" {
		t.Fatal(s, f)
	}
}

func TestCustomNoCredentialModeRejectsTokenReferenceAtValidation(t *testing.T) {
	c := customFixture()
	s := Source{QueryBaseURL: "https://example.test", CredentialMode: CredentialNone, Options: QueryOptions{Custom: c}}
	if err := (customAdapter{}).Validate(s); err == nil {
		t.Fatal("none mode accepted token reference")
	}
	c.Request.Headers = map[string]string{"Accept": "application/json"}
	if err := (customAdapter{}).Validate(s); err != nil {
		t.Fatal("public query rejected", err)
	}
}
