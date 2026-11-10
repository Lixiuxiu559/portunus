package configtransfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	channelentity "github.com/Lixiuxiu559/portunus/backend/channel"
	groupentity "github.com/Lixiuxiu559/portunus/backend/group"
	modelentity "github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
	"gorm.io/gorm"
)

func snapshot(ctx context.Context, db *gorm.DB, appVersion string) (*Payload, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p := &Payload{FormatVersion: 1, AppVersion: appVersion, Channels: []Channel{}, Models: []Model{}, Groups: []Group{}}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := checkSnapshotReadBudget(tx); err != nil {
			return err
		}
		// The entity serializer otherwise silently drops unknown option fields.
		// Old SQL NULL/empty options represent the documented all-zero legacy value.
		optionRows, err := tx.Table("channels").Select("connection_options").Rows()
		if err != nil {
			return err
		}
		for optionRows.Next() {
			var raw sql.NullString
			if err := optionRows.Scan(&raw); err != nil {
				optionRows.Close()
				return err
			}
			if raw.Valid && raw.String != "" && raw.String != "null" {
				var options upstream.Options
				if DecodeStrict([]byte(raw.String), &options) != nil {
					optionRows.Close()
					return NewError("SOURCE_INTEGRITY_ERROR", "snapshot")
				}
			}
		}
		if err := optionRows.Err(); err != nil {
			optionRows.Close()
			return err
		}
		optionRows.Close()
		var channels []channelentity.Channel
		var models []modelentity.Model
		var groups []groupentity.Group
		var items []groupentity.GroupItem
		if err := tx.Order("id").Limit(MaxChannels + 1).Find(&channels).Error; err != nil {
			return err
		}
		if len(channels) > MaxChannels {
			return NewError("LIMIT_EXCEEDED", "snapshot")
		}
		if err := tx.Select("id", "channel_id", "name").Order("id").Limit(MaxModels + 1).Find(&models).Error; err != nil {
			return err
		}
		if len(models) > MaxModels {
			return NewError("LIMIT_EXCEEDED", "snapshot")
		}
		if err := tx.Select("id", "name", "strategy", "active_item_id").Order("id").Limit(MaxGroups + 1).Find(&groups).Error; err != nil {
			return err
		}
		if len(groups) > MaxGroups {
			return NewError("LIMIT_EXCEEDED", "snapshot")
		}
		if err := tx.Select("id", "group_id", "model_id", "priority").Order("group_id, priority, id").Limit(MaxItems + 1).Find(&items).Error; err != nil {
			return err
		}
		if len(items) > MaxItems {
			return NewError("LIMIT_EXCEEDED", "snapshot")
		}
		channelRefs, modelRefs, groupPositions := map[int64]string{}, map[int64]string{}, map[int64]int{}
		for i, c := range channels {
			ref := "c" + strconv.Itoa(i+1)
			channelRefs[c.ID] = ref
			p.Channels = append(p.Channels, Channel{Ref: ref, Name: c.Name, Icon: c.Icon, ProviderID: c.ProviderID, ProfileID: c.ProfileID, AdapterID: c.AdapterID, Type: string(c.Type), BaseURL: c.BaseURL, Key: c.Key, ConnectionOptions: c.ConnectionOptions, AutoSync: c.AutoSync, ThinkingCompat: c.ThinkingCompat, MaxConcurrentRequests: int64(c.MaxConcurrentRequests), RateLimitScope: c.RateLimitScope})
		}
		for i, m := range models {
			ref := "m" + strconv.Itoa(i+1)
			modelRefs[m.ID] = ref
			if channelRefs[m.ChannelID] == "" {
				return NewError("SOURCE_INTEGRITY_ERROR", "snapshot")
			}
			p.Models = append(p.Models, Model{Ref: ref, Name: m.Name, ChannelRef: channelRefs[m.ChannelID]})
		}
		for i, g := range groups {
			groupPositions[g.ID] = i
			p.Groups = append(p.Groups, Group{Ref: "g" + strconv.Itoa(i+1), Name: g.Name, Strategy: string(g.Strategy), Items: []Item{}})
		}
		activeFound := map[int64]bool{}
		for i, item := range items {
			position, ok := groupPositions[item.GroupID]
			if !ok || modelRefs[item.ModelID] == "" {
				return NewError("SOURCE_INTEGRITY_ERROR", "snapshot")
			}
			ref := "i" + strconv.Itoa(i+1)
			p.Groups[position].Items = append(p.Groups[position].Items, Item{Ref: ref, ModelRef: modelRefs[item.ModelID], Priority: strconv.FormatInt(int64(item.Priority), 10)})
			if groups[position].ActiveItemID == item.ID {
				p.Groups[position].ActiveItemRef = &ref
				activeFound[item.GroupID] = true
			}
		}
		for _, g := range groups {
			if g.ActiveItemID != 0 && !activeFound[g.ID] {
				return NewError("SOURCE_INTEGRITY_ERROR", "snapshot")
			}
		}
		return nil
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, safeDBError(err, "snapshot")
	}
	if err := Validate(p); err != nil {
		if e, ok := err.(*Error); ok && (e.Code == "LIMIT_EXCEEDED" || e.Code == "EMPTY_CONFIGURATION") {
			return nil, e
		}
		return nil, NewError("SOURCE_INTEGRITY_ERROR", "snapshot")
	}
	if err := checkBudgets(p); err != nil {
		return nil, err
	}
	return p, nil
}

// Inspect lengths inside the same snapshot before materializing strings. This
// prevents an oversized source database from allocating unbounded name/secret
// arrays merely to discover the 32 MiB payload limit afterwards.
func checkSnapshotReadBudget(tx *gorm.DB) error {
	total := int64(0)
	for _, entry := range []struct {
		table  string
		limit  int
		fields []string
	}{
		{"channels", MaxChannels, []string{"name", "icon", "provider_id", "profile_id", "adapter_id", "type", "base_url", "key", "connection_options", "rate_limit_scope"}},
		{"models", MaxModels, []string{"name"}},
		{"groups", MaxGroups, []string{"name", "strategy"}},
		{"group_items", MaxItems, nil},
	} {
		terms := []string{}
		invalid := []string{}
		for _, field := range entry.fields {
			expr := "COALESCE(length(CAST(" + quoteIdentifier(field) + " AS BLOB)),0)"
			terms = append(terms, expr)
			max := MaxNameBytes
			if field == "icon" {
				max = 64 * 1024
			}
			if field == "connection_options" {
				max = 2*MaxNameBytes + 256
			}
			invalid = append(invalid, expr+">"+strconv.Itoa(max))
		}
		query := "SELECT count(*) AS count, 0 AS bytes, 0 AS invalid FROM " + quoteIdentifier(entry.table)
		if len(terms) > 0 {
			query = "SELECT count(*) AS count, COALESCE(sum(" + strings.Join(terms, "+") + "),0) AS bytes, COALESCE(max(CASE WHEN " + strings.Join(invalid, " OR ") + " THEN 1 ELSE 0 END),0) AS invalid FROM " + quoteIdentifier(entry.table)
		}
		var row struct {
			Count   int64
			Bytes   int64
			Invalid int
		}
		if err := tx.Raw(query).Scan(&row).Error; err != nil {
			return err
		}
		if row.Count > int64(entry.limit) {
			return NewError("LIMIT_EXCEEDED", "snapshot")
		}
		if row.Invalid != 0 {
			return NewError("SOURCE_INTEGRITY_ERROR", "snapshot")
		}
		total += row.Bytes
		if total > MaxPlaintextBytes {
			return NewError("LIMIT_EXCEEDED", "snapshot")
		}
	}
	return nil
}

func payloadBytes(p *Payload) ([]byte, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, NewError("CONFIG_INVALID", "validate")
	}
	if len(b) > MaxPlaintextBytes {
		wipe(b)
		return nil, NewError("LIMIT_EXCEEDED", "validate")
	}
	return b, nil
}
