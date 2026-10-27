package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"time"
)

func (a newAPIAdapter) requestPart(s ResolvedSource, path string) (RequestSpec, error) {
	if err := validateNewAPIOptions(s.Options); err != nil {
		return RequestSpec{}, err
	}
	r, err := requestFor(s, a.Describe(), path)
	if err == nil {
		r.Headers.Set("New-Api-User", s.Options.UserID)
	}
	return r, err
}
func partStatus(f *Failure) QueryPartStatus {
	if f == nil {
		return QueryPartStatus{Status: "ok"}
	}
	status := "error"
	if f.UpstreamStatus == 404 {
		status = "unsupported"
	}
	return QueryPartStatus{Status: status, Failure: f}
}
func executeNewAPI(ctx context.Context, doer HTTPDoer, a newAPIAdapter, source ResolvedSource) (*Snapshot, *Failure) {
	budget := maxResponseBytes
	fetch := func(path string) ([]byte, int, *Failure) {
		spec, err := a.requestPart(source, path)
		if err != nil {
			return nil, 0, configurationFailure()
		}
		body, _, status, failure := executeRequest(ctx, doer, a.Describe(), spec, &budget)
		return body, status, failure
	}
	body, status, wf := fetch("/api/user/self")
	var wallet *Snapshot
	if wf == nil {
		wallet, wf = a.Parse(source, nil, body)
		if wf != nil {
			wf.UpstreamStatus = status
		}
	}
	// Configuration/redirect failures and cancellation must never publish partial data.
	if wf != nil && (wf.Category == "configuration" || wf.Category == "cancelled") {
		return nil, wf
	}
	body, status, sf := fetch("/api/subscription/self")
	var items []Item
	var preference string
	if sf == nil {
		items, preference, sf = parseNewAPISubscriptions(source, body, time.Now())
		if sf != nil {
			sf.UpstreamStatus = status
		}
	}
	if ctx.Err() != nil {
		return nil, requestFailure(ctx, ctx.Err(), "network_error", "额度查询连接失败")
	}
	if sf != nil && (sf.Category == "configuration" || sf.Category == "cancelled") {
		return nil, sf
	}
	if wf != nil && sf != nil {
		// Preserve the existing invalid-identity behavior when no primary read succeeds.
		for _, f := range []*Failure{wf, sf} {
			if f.Category == "authentication" || f.Category == "permission" {
				return nil, f
			}
		}
		var limited *Failure
		for _, f := range []*Failure{wf, sf} {
			if f.Category == "rate_limited" && (limited == nil || rateLimitDelay(f) > rateLimitDelay(limited)) {
				limited = f
			}
		}
		if limited != nil {
			return nil, limited
		}
		return nil, wf
	}
	snapshot := &Snapshot{Items: []Item{}, NewAPI: &NewAPIStatus{BillingPreference: preference, Wallet: partStatus(wf), Subscriptions: partStatus(sf)}}
	if wallet != nil {
		snapshot.Items = append(snapshot.Items, wallet.Items...)
	}
	if sf == nil {
		snapshot.Items = append(snapshot.Items, items...)
		snapshot.NewAPI.SubscriptionSummary = summarizeNewAPISubscriptions(source, items, time.Now())
	}
	combined := summarizeNewAPISubscriptions(source, snapshot.Items, time.Now())
	limit := "limited"
	if combined.UnlimitedCount > 0 {
		limit = "unlimited"
	}
	snapshot.NewAPI.AccountSummary = &Item{ID: "account_total", Label: "可用额度", Kind: "balance", Scope: "account", Unit: combined.Unit, Remaining: combined.Remaining, LimitState: limit, ExpiresAt: combined.ExpiresAt}
	if len(items) > 0 {
		body, status, pf := fetch("/api/subscription/plans")
		var titles map[string]string
		if pf == nil {
			titles, pf = parseNewAPIPlans(body)
			if pf != nil {
				pf.UpstreamStatus = status
			}
		}
		if ctx.Err() != nil {
			return nil, requestFailure(ctx, ctx.Err(), "network_error", "额度查询连接失败")
		}
		if pf != nil && pf.Category == "cancelled" {
			return nil, pf
		}
		state := partStatus(pf)
		snapshot.NewAPI.Plans = &state
		if pf == nil {
			for i := range snapshot.Items {
				if title := titles[snapshot.Items[i].PlanID]; title != "" {
					snapshot.Items[i].Label = title
				}
			}
		}
	}
	if ctx.Err() != nil {
		return nil, requestFailure(ctx, ctx.Err(), "network_error", "额度查询连接失败")
	}
	if !validateSnapshot(snapshot) {
		return nil, invalidResponse()
	}
	return snapshot, nil
}

func summarizeNewAPISubscriptions(source ResolvedSource, items []Item, now time.Time) *NewAPISubscriptionSummary {
	summary := &NewAPISubscriptionSummary{Count: len(items)}
	remaining := new(big.Rat)
	for _, item := range items {
		if item.LimitState == "unlimited" {
			summary.UnlimitedCount++
		} else {
			// Sum original quota before conversion, avoiding per-subscription rounding.
			value := item.Remaining
			if item.Original != nil {
				value = item.Original.Remaining
			}
			amount, _ := parseDecimal(*value)
			remaining.Add(remaining, amount)
		}
		if item.ExpiresAt != nil && (summary.ExpiresAt == nil || item.ExpiresAt.Before(*summary.ExpiresAt)) {
			summary.ExpiresAt = item.ExpiresAt
		}
		if item.ResetsAt != nil && item.ResetsAt.After(now) && (summary.ResetsAt == nil || item.ResetsAt.Before(*summary.ResetsAt)) {
			summary.ResetsAt = item.ResetsAt
		}
	}
	value := decimalString(remaining)
	aggregate := Item{Remaining: &value}
	convertNewAPIItem(source, &aggregate)
	summary.Unit = aggregate.Unit
	if summary.UnlimitedCount == 0 {
		summary.Remaining = aggregate.Remaining
	}
	return summary
}

// Partial reads still obey every endpoint's rate limit, including optional titles.
func newAPIRetryAfter(snapshot *Snapshot) int64 {
	if snapshot == nil || snapshot.NewAPI == nil {
		return 0
	}
	info := snapshot.NewAPI
	parts := []QueryPartStatus{info.Wallet, info.Subscriptions}
	if info.Plans != nil {
		parts = append(parts, *info.Plans)
	}
	var seconds int64
	for _, part := range parts {
		if part.Failure != nil && part.Failure.Category == "rate_limited" {
			seconds = max(seconds, rateLimitDelay(part.Failure))
		}
	}
	return seconds
}

func newAPIData(body []byte) (json.RawMessage, *Failure) {
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if !decodeBody(body, &envelope) || envelope.Success == nil {
		return nil, invalidResponse()
	}
	if !*envelope.Success {
		return nil, businessFailure()
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, invalidResponse()
	}
	return envelope.Data, nil
}
func positiveID(raw json.RawMessage) (string, bool) {
	// IDs and timestamps are JSON integers in the upstream schema; no float conversion.
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || n <= 0 {
		return "", false
	}
	return strconv.FormatInt(n, 10), true
}
func unixField(raw json.RawMessage, allowZero bool) (*time.Time, bool) {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || n < 0 || n > 253402300799 || (n == 0 && !allowZero) {
		return nil, false
	}
	if n == 0 {
		return nil, true
	}
	value := time.Unix(n, 0).UTC()
	return &value, true
}
func convertNewAPIItem(s ResolvedSource, item *Item) {
	item.Unit = Unit{Kind: "count", Code: "QUOTA"}
	if s.Options.QuotaPerUnit == "" {
		return
	}
	item.Original = &OriginalAmounts{Unit: item.Unit, Remaining: item.Remaining, Used: item.Used, Total: item.Total}
	ratio, _ := parseDecimal(s.Options.QuotaPerUnit)
	for _, field := range []**string{&item.Remaining, &item.Used, &item.Total} {
		if *field != nil {
			n, _ := parseDecimal(**field)
			value := decimalString(new(big.Rat).Quo(n, ratio))
			*field = &value
		}
	}
	item.Unit.Code = s.Options.DisplayUnit
	if item.Unit.Code == "USD" || item.Unit.Code == "CNY" {
		item.Unit.Kind = "money"
	}
}
func parseNewAPISubscriptions(s ResolvedSource, body []byte, now time.Time) ([]Item, string, *Failure) {
	data, f := newAPIData(body)
	if f != nil {
		return nil, "", f
	}
	var v struct {
		BillingPreference string          `json:"billing_preference"`
		Subscriptions     json.RawMessage `json:"subscriptions"`
	}
	if !decodeBody(data, &v) || len(v.BillingPreference) > 128 || len(v.Subscriptions) == 0 || v.Subscriptions[0] != '[' {
		return nil, "", invalidResponse()
	}
	var entries []struct {
		Subscription json.RawMessage `json:"subscription"`
	}
	if !decodeBody(v.Subscriptions, &entries) || len(entries) > 100 {
		return nil, "", invalidResponse()
	}
	items := []Item{}
	seen := map[string]bool{}
	for _, entry := range entries {
		var sub struct {
			ID     json.RawMessage `json:"id"`
			PlanID json.RawMessage `json:"plan_id"`
			Total  json.RawMessage `json:"amount_total"`
			Used   json.RawMessage `json:"amount_used"`
			Status string          `json:"status"`
			Start  json.RawMessage `json:"start_time"`
			End    json.RawMessage `json:"end_time"`
			Reset  json.RawMessage `json:"next_reset_time"`
		}
		if !decodeBody(entry.Subscription, &sub) || sub.Status == "" || len(sub.Status) > 32 {
			return nil, "", invalidResponse()
		}
		id, ok := positiveID(sub.ID)
		if !ok || seen[id] {
			return nil, "", invalidResponse()
		}
		seen[id] = true
		plan, ok := positiveID(sub.PlanID)
		if !ok {
			return nil, "", invalidResponse()
		}
		end, ok := unixField(sub.End, false)
		if !ok {
			return nil, "", invalidResponse()
		}
		start, ok := unixField(sub.Start, true)
		if !ok {
			return nil, "", invalidResponse()
		}
		if sub.Status != "active" || !end.After(now) || (start != nil && start.After(now)) {
			continue
		}
		reset, ok := unixField(sub.Reset, true)
		if !ok {
			return nil, "", invalidResponse()
		}
		total, e1 := amount(sub.Total)
		used, e2 := amount(sub.Used)
		if e1 != nil || e2 != nil {
			return nil, "", invalidResponse()
		}
		tr, _ := parseDecimal(*total)
		ur, _ := parseDecimal(*used)
		if tr.Sign() < 0 || ur.Sign() < 0 {
			return nil, "", invalidResponse()
		}
		item := Item{ID: "subscription:" + id, SubscriptionID: id, PlanID: plan, Label: fmt.Sprintf("套餐 #%s", plan), Kind: "usage_window", Scope: "plan", Total: total, Used: used, LimitState: "limited", ResetsAt: reset, ExpiresAt: end, AccountStatus: "active"}
		if tr.Sign() == 0 {
			item.LimitState = "unlimited"
		} else {
			percent, _ := new(big.Rat).Mul(new(big.Rat).Quo(ur, tr), big.NewRat(100, 1)).Float64()
			item.UsedPercent = &percent
			remaining := decimalString(new(big.Rat).Sub(tr, ur))
			item.Remaining = &remaining
		}
		convertNewAPIItem(s, &item)
		items = append(items, item)
	}
	return items, v.BillingPreference, nil
}
func parseNewAPIPlans(body []byte) (map[string]string, *Failure) {
	data, f := newAPIData(body)
	if f != nil {
		return nil, f
	}
	if len(data) == 0 || data[0] != '[' {
		return nil, invalidResponse()
	}
	var plans []struct {
		Plan struct {
			ID    json.RawMessage `json:"id"`
			Title string          `json:"title"`
		} `json:"plan"`
	}
	if !decodeBody(data, &plans) || len(plans) > 100 {
		return nil, invalidResponse()
	}
	titles := map[string]string{}
	for _, p := range plans {
		id, ok := positiveID(p.Plan.ID)
		if !ok || p.Plan.Title == "" || len(p.Plan.Title) > 256 || titles[id] != "" {
			return nil, invalidResponse()
		}
		titles[id] = p.Plan.Title
	}
	return titles, nil
}
