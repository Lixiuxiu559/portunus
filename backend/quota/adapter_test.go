package quota

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Sanitized fixtures follow the official response schemas linked by Describe.
const deepSeekFixture = `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"0","granted_balance":"0","topped_up_balance":"0"},{"currency":"USD","total_balance":"-1.20"}]}`
const siliconFlowFixture = `{"code":20000,"status":true,"data":{"balance":"0.88","chargeBalance":"88.00","totalBalance":"88.88","status":"normal"}}`
const openRouterFixture = `{"data":{"total_credits":100.5,"total_usage":25.75}}`
const newAPIFixture = `{"success":true,"data":{"quota":1000000,"used_quota":"125000"}}`

func TestAdapterFixtures(t *testing.T) {
	for _, tc := range []struct {
		id, body, remaining, used, unit string
		options                         QueryOptions
		count                           int
	}{
		{id: "deepseek.balance", body: deepSeekFixture, remaining: "0", unit: "CNY", count: 2},
		{id: "siliconflow.balance", body: siliconFlowFixture, remaining: "88.88", unit: "CNY", count: 1},
		{id: "openrouter.account_balance", body: openRouterFixture, remaining: "74.75", used: "25.75", unit: "USD", count: 1},
		{id: "newapi.account", body: newAPIFixture, remaining: "1000000", used: "125000", unit: "QUOTA", options: QueryOptions{UserID: "7"}, count: 1},
		{id: "newapi.account", body: newAPIFixture, remaining: "2", used: "0.25", unit: "CNY", options: QueryOptions{UserID: "7", QuotaPerUnit: "500000", DisplayUnit: "CNY"}, count: 1},
	} {
		t.Run(tc.id+tc.unit, func(t *testing.T) {
			a, _ := LookupAdapter(tc.id)
			s, f := a.Parse(ResolvedSource{Options: tc.options}, nil, []byte(tc.body))
			if f != nil || !validateSnapshot(s) {
				t.Fatalf("parse=%+v failure=%+v", s, f)
			}
			i := s.Items[0]
			if len(s.Items) != tc.count || *i.Remaining != tc.remaining || i.Unit.Code != tc.unit {
				t.Fatalf("unexpected %+v", s)
			}
			if tc.used != "" && (i.Used == nil || *i.Used != tc.used) {
				t.Fatalf("used=%v", i.Used)
			}
			if i.AccountStatus != "" {
				t.Fatal("balance must not infer account authentication status")
			}
		})
	}
}
func TestAdaptersRejectMissingAmounts(t *testing.T) {
	for _, tc := range []struct{ id, body string }{
		{"deepseek.balance", `{"is_available":true,"balance_infos":[]}`}, {"deepseek.balance", `{"is_available":true,"balance_infos":[{"currency":"USD"}]}`},
		{"siliconflow.balance", `{"code":20000,"status":true,"data":{"balance":"1"}}`}, {"openrouter.account_balance", `{"data":{"total_credits":0}}`},
		{"newapi.account", `{"success":true,"data":{"quota":0}}`},
	} {
		a, _ := LookupAdapter(tc.id)
		if s, f := a.Parse(ResolvedSource{Options: QueryOptions{UserID: "1"}}, nil, []byte(tc.body)); f == nil || s != nil {
			t.Fatalf("%s accepted incomplete response", tc.id)
		}
	}
}
func TestAdaptersBusinessErrorsAndInvalidJSON(t *testing.T) {
	for _, id := range []string{"deepseek.balance", "siliconflow.balance", "openrouter.account_balance", "newapi.account"} {
		a, _ := LookupAdapter(id)
		for _, body := range []string{`{}`, `null`, `<html>secret</html>`, `{} {}`} {
			if _, f := a.Parse(ResolvedSource{Options: QueryOptions{UserID: "1"}}, nil, []byte(body)); f == nil {
				t.Fatalf("%s accepted %s", id, body)
			}
		}
	}
	for _, id := range []string{"siliconflow.balance", "newapi.account"} {
		a, _ := LookupAdapter(id)
		_, f := a.Parse(ResolvedSource{Options: QueryOptions{UserID: "1"}}, nil, []byte(`{"code":50000,"status":false,"success":false,"message":"secret"}`))
		if f == nil || f.Category != "business" || strings.Contains(f.Message, "secret") {
			t.Fatalf("unexpected failure: %+v", f)
		}
	}
}
func TestAdapterRequestsAndOfficialTargetIsolation(t *testing.T) {
	paths := map[string]string{"deepseek.balance": "https://api.deepseek.com/user/balance", "siliconflow.balance": "https://api.siliconflow.cn/v1/user/info", "openrouter.account_balance": "https://openrouter.ai/api/v1/credits", "newapi.account": "http://localhost:4000/prefix/api/user/self"}
	for _, d := range Descriptors() {
		if d.ID == "custom.balance" {
			continue
		}
		a, _ := LookupAdapter(d.ID)
		s := ResolvedSource{Token: "fixture-token", QueryBaseURL: d.DefaultBaseURL}
		if d.ID == "newapi.account" {
			s.QueryBaseURL = "http://localhost:4000/prefix/"
			s.Options.UserID = "42"
		}
		r, err := a.BuildRequest(s)
		if err != nil || r.URL != paths[d.ID] || r.Method != http.MethodGet || r.Headers.Get("Authorization") != "Bearer fixture-token" {
			t.Fatalf("%s request=%+v err=%v", d.ID, r, err)
		}
		if d.ID == "newapi.account" {
			if r.Headers.Get("New-Api-User") != "42" {
				t.Fatal("missing user header")
			}
			continue
		}
		for _, base := range []string{"http://api.deepseek.com", d.DefaultBaseURL + "/extra", d.DefaultBaseURL + "?token=secret", d.DefaultBaseURL + "#fragment", "https://evil.example", strings.Replace(d.DefaultBaseURL, "https://", "https://secret@", 1)} {
			s.QueryBaseURL = base
			if _, err := a.BuildRequest(s); err == nil {
				t.Fatalf("%s accepted arbitrary target %s", d.ID, base)
			}
		}
	}
}
func TestNewAPIOptions(t *testing.T) {
	a, _ := LookupAdapter("newapi.account")
	for _, o := range []QueryOptions{{}, {UserID: "0"}, {UserID: "1\r\nX: y"}, {UserID: "1", QuotaPerUnit: "0", DisplayUnit: "USD"}, {UserID: "1", QuotaPerUnit: "-1", DisplayUnit: "USD"}, {UserID: "1", QuotaPerUnit: "500000"}, {UserID: "1", DisplayUnit: "USD"}} {
		if err := a.Validate(Source{CredentialMode: "dedicated", QueryBaseURL: "https://example.com", Options: o}); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	if err := a.Validate(Source{CredentialMode: "inherit_channel_key", QueryBaseURL: "https://example.com", Options: QueryOptions{UserID: "1"}}); err == nil {
		t.Fatal("account token must be dedicated")
	}
}
func TestDecimalExactnessAndBounds(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{`"-0.00"`, "0"}, {`0`, "0"}, {`1e-8`, "0.00000001"}, {`"9007199254740993.123456789"`, "9007199254740993.123456789"}} {
		v, e := amount(json.RawMessage(tc.input))
		if e != nil || *v != tc.want {
			t.Fatalf("%s => %v %v", tc.input, v, e)
		}
	}
	for _, raw := range []string{`null`, `true`, `"NaN"`, `"Infinity"`, `"1/2"`, `"+1"`, `1e999999`, `"` + strings.Repeat("9", 129) + `"`} {
		if _, err := amount(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	a, _ := LookupAdapter("openrouter.account_balance")
	s, f := a.Parse(ResolvedSource{}, nil, []byte(`{"data":{"total_credits":"0.3","total_usage":"0.1"}}`))
	if f != nil || *s.Items[0].Remaining != "0.2" {
		t.Fatal("decimal subtraction lost precision")
	}
	s, f = a.Parse(ResolvedSource{}, nil, []byte(`{"data":{"total_credits":"0","total_usage":"1"}}`))
	if f != nil || *s.Items[0].Remaining != "-1" {
		t.Fatal("negative balance lost")
	}
}
func TestDescriptorsIndependent(t *testing.T) {
	first := Descriptors()
	if len(first) != 5 {
		t.Fatal(len(first))
	}
	first[0].ProviderIDs[0] = "changed"
	if Descriptors()[0].ProviderIDs[0] == "changed" {
		t.Fatal("descriptor aliases registry")
	}
	if _, ok := LookupAdapter("unknown"); ok {
		t.Fatal("unknown adapter accepted")
	}
}

func TestNewAPIRepeatingDecimalRounding(t *testing.T) {
	a, _ := LookupAdapter("newapi.account")
	s, f := a.Parse(ResolvedSource{Options: QueryOptions{UserID: "1", QuotaPerUnit: "3", DisplayUnit: "credits"}}, nil, []byte(`{"success":true,"data":{"quota":1,"used_quota":2}}`))
	if f != nil || *s.Items[0].Remaining != "0.333333333333333333" || *s.Items[0].Used != "0.666666666666666667" {
		t.Fatalf("snapshot=%+v failure=%+v", s, f)
	}
}
func TestSnapshotValidation(t *testing.T) {
	zero := "0"
	invalid := "NaN"
	if validateSnapshot(&Snapshot{}) || validateSnapshot(nil) {
		t.Fatal("empty snapshot accepted")
	}
	item := balanceItem("balance", "余额", "USD", &zero)
	if !validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("zero is a valid observed balance")
	}
	item.Remaining = nil
	if validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("missing amounts accepted")
	}
	item.Remaining = &invalid
	if validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("nonfinite amount accepted")
	}
	item.Remaining = &zero
	item.Unit.Kind = "invalid"
	if validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("invalid unit accepted")
	}
}
func TestOpenRouterUsesDedicatedManagementCredential(t *testing.T) {
	a, _ := LookupAdapter("openrouter.account_balance")
	if err := a.Validate(Source{CredentialMode: "inherit_channel_key"}); err == nil {
		t.Fatal("must explicitly configure management credential")
	}
}

func TestNewAPIOriginalAmountsPreserveDecimalPrecision(t *testing.T) {
	a, _ := LookupAdapter("newapi.account")
	body := []byte(`{"success":true,"data":{"quota":"9007199254740993.123456789","used_quota":0.000000001}}`)
	options := QueryOptions{UserID: "1", QuotaPerUnit: "10", DisplayUnit: "credits"}
	snapshot, failure := a.Parse(ResolvedSource{Options: options}, nil, body)
	if failure != nil || !validateSnapshot(snapshot) || len(snapshot.Items) != 1 {
		t.Fatalf("snapshot=%+v failure=%+v", snapshot, failure)
	}
	item := snapshot.Items[0]
	if item.Original == nil || item.Original.Unit != (Unit{Kind: "count", Code: "QUOTA"}) || *item.Original.Remaining != "9007199254740993.123456789" || *item.Original.Used != "0.000000001" || item.Original.Total != nil {
		t.Fatalf("original=%+v", item.Original)
	}
	if *item.Remaining != "900719925474099.3123456789" || *item.Used != "0.0000000001" {
		t.Fatalf("converted=%+v", item)
	}
	options.QuotaPerUnit = ""
	options.DisplayUnit = ""
	raw, failure := a.Parse(ResolvedSource{Options: options}, nil, body)
	if failure != nil || raw.Items[0].Original != nil || *raw.Items[0].Remaining != *item.Original.Remaining {
		t.Fatalf("raw=%+v failure=%+v", raw, failure)
	}
}
func TestSnapshotRejectsInvalidOriginalAmounts(t *testing.T) {
	zero, invalid := "0", "NaN"
	original := OriginalAmounts{Unit: Unit{Kind: "count", Code: "QUOTA"}, Remaining: &zero}
	item := balanceItem("account_quota", "账户额度", "CNY", &zero)
	item.Original = &original
	if !validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("valid original rejected")
	}
	original.Remaining = &invalid
	if validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("nonfinite original accepted")
	}
	tooLong := strings.Repeat("1", 129)
	original.Remaining = &tooLong
	if validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("oversized original accepted")
	}
	original.Remaining = nil
	if validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("empty original accepted")
	}
	original.Remaining = &zero
	original.Unit.Kind = "invalid"
	if validateSnapshot(&Snapshot{Items: []Item{item}}) {
		t.Fatal("invalid original unit accepted")
	}
}
