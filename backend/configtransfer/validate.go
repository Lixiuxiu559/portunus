package configtransfer

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
)

var refPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var priorityPattern = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)
var builtinIconPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var operationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func ValidOperationID(s string) bool { return operationPattern.MatchString(s) }
func ValidPreviewID(s string) bool   { return refPattern.MatchString(s) }
func ValidHash(s string) bool        { return hashPattern.MatchString(s) }
func validString(s string, max int, required bool) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validIcon(s string) bool {
	if s == "" || builtinIconPattern.MatchString(s) {
		return true
	}
	const prefix = "data:image/png;base64,"
	if len(s) > 64*1024 || !strings.HasPrefix(s, prefix) {
		return false
	}
	b, err := base64.StdEncoding.Strict().DecodeString(s[len(prefix):])
	if err != nil {
		return false
	}
	c, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || c.Width < 1 || c.Height < 1 || c.Width > 256 || c.Height > 256 {
		return false
	}
	_, err = png.Decode(bytes.NewReader(b))
	return err == nil
}
func invalid(path string) *Error {
	e := NewError("CONFIG_INVALID", "validate")
	e.Field = path
	return e
}
func Validate(p *Payload) error {
	if p.FormatVersion != 1 {
		return NewError("FORMAT_UNSUPPORTED", "validate")
	}
	if !validString(p.AppVersion, 128, true) {
		return invalid("app_version")
	}
	if p.Channels == nil || p.Models == nil || p.Groups == nil {
		return invalid("configuration")
	}
	if len(p.Channels) > MaxChannels || len(p.Models) > MaxModels || len(p.Groups) > MaxGroups {
		return NewError("LIMIT_EXCEEDED", "validate")
	}
	if p.Counts().Empty() {
		return NewError("EMPTY_CONFIGURATION", "validate")
	}
	channels, models, groups, items := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	channelNames, groupNames := map[string]bool{}, map[string]bool{}
	for i, c := range p.Channels {
		path := "channels[" + strconv.Itoa(i) + "]"
		if !refPattern.MatchString(c.Ref) || channels[c.Ref] {
			return invalid(path + ".ref")
		}
		channels[c.Ref] = true
		if !validString(c.Name, MaxNameBytes, true) || channelNames[c.Name] {
			return invalid(path + ".name")
		}
		channelNames[c.Name] = true
		if !validIcon(c.Icon) {
			return invalid(path + ".icon")
		}
		for _, s := range []string{c.ProviderID, c.ProfileID, c.AdapterID, c.Type, c.BaseURL, c.Key, c.RateLimitScope, c.ConnectionOptions.ModelDiscovery} {
			if !validString(s, MaxNameBytes, false) {
				return invalid(path + ".connection")
			}
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
			return invalid(path + ".base_url")
		}
		if c.MaxConcurrentRequests < 0 || c.MaxConcurrentRequests > MaxSafeID || (c.RateLimitScope != "" && c.RateLimitScope != "model" && c.RateLimitScope != "channel") {
			return invalid(path + ".limits")
		}
		if _, err := upstream.Open(upstream.Config{ProviderID: c.ProviderID, ProfileID: c.ProfileID, AdapterID: c.AdapterID, Type: protocol.Provider(c.Type), BaseURL: c.BaseURL, Key: c.Key, Options: c.ConnectionOptions}); err != nil {
			return invalid(path + ".connection_options")
		}
	}
	modelNames := map[string]map[string]bool{}
	for i, m := range p.Models {
		path := "models[" + strconv.Itoa(i) + "]"
		if !refPattern.MatchString(m.Ref) || models[m.Ref] {
			return invalid(path + ".ref")
		}
		models[m.Ref] = true
		if !channels[m.ChannelRef] || !validString(m.Name, MaxNameBytes, true) {
			return invalid(path + ".channel_ref/name")
		}
		if modelNames[m.ChannelRef] == nil {
			modelNames[m.ChannelRef] = map[string]bool{}
		}
		if modelNames[m.ChannelRef][m.Name] {
			return invalid(path + ".name")
		}
		modelNames[m.ChannelRef][m.Name] = true
	}
	total := 0
	for i, g := range p.Groups {
		path := "groups[" + strconv.Itoa(i) + "]"
		if !refPattern.MatchString(g.Ref) || groups[g.Ref] {
			return invalid(path + ".ref")
		}
		groups[g.Ref] = true
		if !validString(g.Name, MaxNameBytes, true) || groupNames[g.Name] {
			return invalid(path + ".name")
		}
		groupNames[g.Name] = true
		if g.Strategy != "manual" && g.Strategy != "failover" {
			return invalid(path + ".strategy")
		}
		if g.Items == nil {
			return invalid(path + ".items")
		}
		total += len(g.Items)
		if total > MaxItems {
			return NewError("LIMIT_EXCEEDED", "validate")
		}
		local, usedModels := map[string]bool{}, map[string]bool{}
		var previous int64
		for j, item := range g.Items {
			if !refPattern.MatchString(item.Ref) || items[item.Ref] || !models[item.ModelRef] || usedModels[item.ModelRef] {
				return invalid(path + ".items[" + strconv.Itoa(j) + "].ref/model_ref")
			}
			items[item.Ref], local[item.Ref], usedModels[item.ModelRef] = true, true, true
			priority, err := strconv.ParseInt(item.Priority, 10, 64)
			if !priorityPattern.MatchString(item.Priority) || err != nil || (j > 0 && priority < previous) {
				return invalid(path + ".items[" + strconv.Itoa(j) + "].priority")
			}
			previous = priority
		}
		if g.ActiveItemRef != nil && !local[*g.ActiveItemRef] {
			return invalid(path + ".active_item_ref")
		}
	}
	return nil
}
