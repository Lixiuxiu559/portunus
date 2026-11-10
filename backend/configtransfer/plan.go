package configtransfer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"gorm.io/gorm"
)

type rename struct {
	Ref  string `json:"ref"`
	From string `json:"from"`
	To   string `json:"to"`
}
type namePlan struct {
	FileSHA256 string   `json:"file_sha256"`
	Channels   []rename `json:"channels"`
	Groups     []rename `json:"groups"`
}

func (p namePlan) hash() string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func targetNames(db *gorm.DB, table string) (map[string]bool, error) {
	rows, err := db.Table(table).Select("CASE WHEN length(CAST(name AS BLOB)) <= ? THEN name ELSE NULL END AS name", 64*1024*1024).Limit(100001).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	count, bytes := 0, 0
	for rows.Next() {
		var stored sql.NullString
		if err := rows.Scan(&stored); err != nil {
			return nil, err
		}
		if !stored.Valid {
			return nil, NewError("LIMIT_EXCEEDED", "plan")
		}
		name := stored.String
		count++
		bytes += len(name)
		if count > 100000 || bytes > 64*1024*1024 {
			return nil, NewError("LIMIT_EXCEEDED", "plan")
		}
		names[name] = true
	}
	return names, rows.Err()
}
func planNamespace(source []rename, target map[string]bool) ([]rename, error) {
	reserved := map[string]bool{}
	for name := range target {
		reserved[name] = true
	}
	for _, item := range source {
		reserved[item.From] = true
	}
	probes := 0
	for i, item := range source {
		source[i].To = item.From
		if !target[item.From] {
			continue
		}
		for n := 1; ; n++ {
			probes++
			if probes > 100000 {
				return nil, NewError("NAME_SPACE_EXHAUSTED", "plan")
			}
			suffix := "（导入）"
			if n > 1 {
				suffix = "（导入 " + strconv.Itoa(n) + "）"
			}
			prefix := item.From
			if len(prefix)+len(suffix) > MaxNameBytes {
				prefix = prefix[:MaxNameBytes-len(suffix)]
				for !utf8.ValidString(prefix) {
					prefix = prefix[:len(prefix)-1]
				}
			}
			candidate := prefix + suffix
			if !reserved[candidate] {
				source[i].To = candidate
				reserved[candidate] = true
				break
			}
		}
	}
	return source, nil
}
func makePlan(db *gorm.DB, p *Payload, fileHash string) (namePlan, error) {
	plan := namePlan{FileSHA256: fileHash, Channels: []rename{}, Groups: []rename{}}
	for _, c := range p.Channels {
		plan.Channels = append(plan.Channels, rename{Ref: c.Ref, From: c.Name})
	}
	for _, g := range p.Groups {
		plan.Groups = append(plan.Groups, rename{Ref: g.Ref, From: g.Name})
	}
	cn, err := targetNames(db, "channels")
	if err != nil {
		return plan, err
	}
	gn, err := targetNames(db, "groups")
	if err != nil {
		return plan, err
	}
	// Both namespaces share the documented aggregate target-name budget.
	if len(cn)+len(gn) > 100000 {
		return plan, NewError("LIMIT_EXCEEDED", "plan")
	}
	total := 0
	for name := range cn {
		total += len(name)
	}
	for name := range gn {
		total += len(name)
	}
	if total > 64*1024*1024 {
		return plan, NewError("LIMIT_EXCEEDED", "plan")
	}
	plan.Channels, err = planNamespace(plan.Channels, cn)
	if err != nil {
		return plan, err
	}
	plan.Groups, err = planNamespace(plan.Groups, gn)
	return plan, err
}
func previewDTO(p *Payload, plan namePlan) PreviewDTO {
	d := PreviewDTO{FileInfo: FileInfo{1, p.AppVersion}, Counts: p.Counts(), Channels: []PreviewChannel{}, Models: append([]Model{}, p.Models...), Groups: []PreviewGroup{}}
	counts := map[string]int{}
	for _, m := range p.Models {
		counts[m.ChannelRef]++
	}
	for i, c := range p.Channels {
		name := plan.Channels[i].To
		if name != c.Name {
			d.RenameCount++
		}
		d.Channels = append(d.Channels, PreviewChannel{Ref: c.Ref, OriginalName: c.Name, Name: name, ModelCount: counts[c.Ref], HasKey: c.Key != "", Type: c.Type})
	}
	for i, g := range p.Groups {
		name := plan.Groups[i].To
		if name != g.Name {
			d.RenameCount++
		}
		pg := PreviewGroup{Ref: g.Ref, OriginalName: g.Name, Name: name, Strategy: g.Strategy, ActiveItemRef: g.ActiveItemRef, Items: []PreviewItem{}}
		for j, item := range g.Items {
			pg.Items = append(pg.Items, PreviewItem{Ref: item.Ref, ModelRef: item.ModelRef, Priority: item.Priority, Position: j + 1})
		}
		d.Groups = append(d.Groups, pg)
	}
	return d
}
func checkBudgets(p *Payload) error {
	plan := namePlan{Channels: []rename{}, Groups: []rename{}}
	for _, c := range p.Channels {
		plan.Channels = append(plan.Channels, rename{Ref: c.Ref, From: c.Name, To: c.Name})
	}
	for _, g := range p.Groups {
		plan.Groups = append(plan.Groups, rename{Ref: g.Ref, From: g.Name, To: g.Name})
	}
	d := previewDTO(p, plan)
	for i := range d.Channels {
		d.Channels[i].HasKey = false
	}
	margin := 64*(len(p.Channels)+len(p.Groups)) + 4096
	n, err := encodedSize(d)
	if err != nil || n+margin > MaxPreviewBytes {
		return NewError("LIMIT_EXCEEDED", "validate")
	}
	r := ResultDTO{Channels: []ResultName{}, Groups: []ResultName{}}
	for _, c := range p.Channels {
		r.Channels = append(r.Channels, ResultName{c.Ref, "9007199254740991", c.Name, c.Name})
	}
	for _, g := range p.Groups {
		r.Groups = append(r.Groups, ResultName{g.Ref, "9007199254740991", g.Name, g.Name})
	}
	n, err = encodedSize(r)
	if err != nil || n+margin > MaxResultBytes {
		return NewError("LIMIT_EXCEEDED", "validate")
	}
	return nil
}
