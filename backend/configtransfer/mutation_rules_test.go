package configtransfer

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMutationJSONUnicodeDepthAndSafePaths(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"a b"`, "a b"}, {`"\\\"\n\t"`, "\\\"\n\t"},
		{`"\ud800\udc00"`, "\U00010000"}, {`"\udbff\udfff"`, "\U0010ffff"},
		{`"\ud7ff"`, "\ud7ff"}, {`"\ue000"`, "\ue000"}, {`"\uffff"`, "\uffff"},
	} {
		var got string
		if err := DecodeStrict([]byte(tc.input), &got); err != nil || got != tc.want {
			t.Fatal(tc.input, got, err)
		}
	}
	for _, input := range []string{`"\ud800\udbff"`, `"\udbff\ue000"`, `"\ud800x"`, `"\udbff\uZZZZ"`, `"\udc00"`, `"\udfff"`, `"a` + string([]byte{0x1f}) + `b"`} {
		var got string
		assertCode(t, DecodeStrict([]byte(input), &got), "INVALID_INPUT")
	}
	type nested struct {
		Child *nested `json:"child"`
	}
	for _, depth := range []int{16, 17} {
		input := strings.Repeat(`{"child":`, depth) + "null" + strings.Repeat("}", depth)
		var got nested
		err := DecodeStrict([]byte(input), &got)
		if (err == nil) != (depth == 16) {
			t.Fatal("JSON depth boundary", depth, err)
		}
	}
	var target struct {
		Rows []struct {
			Value int32 `json:"value"`
		} `json:"rows"`
	}
	err := DecodeStrict([]byte(`{"rows":[{"value":2147483648}]}`), &target)
	assertCode(t, err, "INVALID_INPUT")
	if err.(*Error).Field != "rows[0].value" {
		t.Fatal("unsafe or incomplete field path", err)
	}
	if err := DecodeStrict([]byte(`{"rows":[{"value":2147483647}]}`), &target); err != nil || target.Rows[0].Value != 2147483647 {
		t.Fatal(target, err)
	}
	var tagged struct {
		hidden  int
		Ignored int `json:"-"`
		Default string
	}
	if err := DecodeStrict([]byte(`{"Default":"kept"}`), &tagged); err != nil || tagged.Default != "kept" {
		t.Fatal(tagged, err)
	}
	for _, value := range []any{nil, 1, (*Payload)(nil)} {
		assertCode(t, DecodeStrict([]byte(`{}`), value), "INVALID_INPUT")
	}
}

func TestMutationRuleFieldAndRelationValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Payload){
		"version":         func(p *Payload) { p.FormatVersion = 2 },
		"app version":     func(p *Payload) { p.AppVersion = "" },
		"nil channels":    func(p *Payload) { p.Channels = nil },
		"nil models":      func(p *Payload) { p.Models = nil },
		"nil groups":      func(p *Payload) { p.Groups = nil },
		"channel ref":     func(p *Payload) { p.Channels[0].Ref = "!" },
		"channel name":    func(p *Payload) { p.Channels[0].Name = "" },
		"channel dupname": func(p *Payload) { p.Channels[1].Name = p.Channels[0].Name },
		"channel dupref":  func(p *Payload) { p.Channels[1].Ref = p.Channels[0].Ref },
		"channel NUL":     func(p *Payload) { p.Channels[0].Key = "a\x00b" },
		"channel URL":     func(p *Payload) { p.Channels[0].BaseURL = "ftp://synthetic.invalid" },
		"channel limit":   func(p *Payload) { p.Channels[0].MaxConcurrentRequests = -1 },
		"channel max":     func(p *Payload) { p.Channels[0].MaxConcurrentRequests = MaxSafeID + 1 },
		"channel scope":   func(p *Payload) { p.Channels[0].RateLimitScope = "unknown" },
		"model ref":       func(p *Payload) { p.Models[0].Ref = "!" },
		"model name":      func(p *Payload) { p.Models[0].Name = "" },
		"model dupname":   func(p *Payload) { p.Models[2].Name = p.Models[1].Name },
		"group ref":       func(p *Payload) { p.Groups[0].Ref = "!" },
		"group name":      func(p *Payload) { p.Groups[0].Name = "" },
		"group dupname":   func(p *Payload) { p.Groups[1].Name = p.Groups[0].Name },
		"group dupref":    func(p *Payload) { p.Groups[1].Ref = p.Groups[0].Ref },
		"group strategy":  func(p *Payload) { p.Groups[0].Strategy = "unknown" },
		"group nil items": func(p *Payload) { p.Groups[0].Items = nil },
		"item ref":        func(p *Payload) { p.Groups[0].Items[0].Ref = "!" },
		"item dupref":     func(p *Payload) { p.Groups[0].Items[1].Ref = p.Groups[0].Items[0].Ref },
		"item missing":    func(p *Payload) { p.Groups[0].Items[0].ModelRef = "absent" },
		"priority order": func(p *Payload) {
			p.Groups[0].Items[0].Priority = "1"
			p.Groups[0].Items[1].Priority = "0"
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := samplePayload()
			mutate(&p)
			if Validate(&p) == nil {
				t.Fatal("invalid field or relation accepted")
			}
		})
	}
	for _, counts := range []Counts{{}, {Channels: 1}, {Models: 1}, {Groups: 1}} {
		if counts.Empty() != (counts == (Counts{})) {
			t.Fatal("nonempty configuration hidden", counts)
		}
	}
	for _, tc := range []struct {
		value    string
		required bool
		want     bool
	}{{"", false, true}, {"", true, false}, {"abc", true, true}, {"abcd", true, false}, {"a\x00", false, false}, {string([]byte{0xff}), false, false}} {
		if validString(tc.value, 3, tc.required) != tc.want {
			t.Fatal("string byte/Unicode boundary", tc)
		}
	}
	for _, size := range []int{1, 256} {
		icon := "data:image/png;base64," + base64.StdEncoding.EncodeToString(syntheticPNG(t, size, size))
		if !validIcon(icon) {
			t.Fatal("valid PNG size rejected", size)
		}
	}
	for _, mutate := range []func(*Payload){
		func(p *Payload) { p.Channels = make([]Channel, MaxChannels+1) },
		func(p *Payload) { p.Models = make([]Model, MaxModels+1) },
		func(p *Payload) { p.Groups = make([]Group, MaxGroups+1) },
	} {
		p := samplePayload()
		mutate(&p)
		assertCode(t, Validate(&p), "LIMIT_EXCEEDED")
	}
}

func TestMutationPlanReservationsPreviewAndHash(t *testing.T) {
	db := testDB(t)
	db.Table("channels").Create(map[string]any{"name": "A"})
	db.Table("groups").Create(map[string]any{"name": "coding"})
	p := samplePayload()
	plan, err := makePlan(db, &p, strings.Repeat("a", 64))
	if err != nil || plan.Channels[0].To != "A（导入 2）" || plan.Channels[1].To != "A（导入）" || plan.Groups[0].To != "coding（导入）" {
		t.Fatal(plan, err)
	}
	d := previewDTO(&p, plan)
	if d.RenameCount != 2 || d.Counts != p.Counts() || d.Channels[0].ModelCount != 1 || d.Channels[1].ModelCount != 2 || d.Channels[0].HasKey || !d.Channels[1].HasKey || d.Groups[0].Items[0].Position != 1 || d.Groups[0].Items[1].Position != 2 || !reflect.DeepEqual(d.Groups[0].ActiveItemRef, p.Groups[0].ActiveItemRef) {
		t.Fatal("preview changed source references, secret flags or order", d)
	}
	h := plan.hash()
	plan.FileSHA256 = strings.Repeat("b", 64)
	if plan.hash() == h {
		t.Fatal("name confirmation hash omitted file identity")
	}
	long := strings.Repeat("a", MaxNameBytes-len("（导入）"))
	got, err := planNamespace([]rename{{Ref: "a", From: long}}, map[string]bool{long: true})
	if err != nil || got[0].To != long+"（导入）" || len(got[0].To) != MaxNameBytes {
		t.Fatal("name limit is inclusive", got, err)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "synthetic-only-key") {
		t.Fatal("preview exposed credentials")
	}
}
