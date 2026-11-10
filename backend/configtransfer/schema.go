package configtransfer

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const receiptDDL = `CREATE TABLE IF NOT EXISTS config_import_receipts (
  operation_id TEXT PRIMARY KEY NOT NULL,
  owner_instance_id TEXT NOT NULL,
  file_sha256 TEXT NOT NULL CHECK(length(file_sha256)=64),
  plan_sha256 TEXT NOT NULL CHECK(length(plan_sha256)=64),
  status TEXT NOT NULL CHECK(status IN ('prepared','committed','aborted')),
  created_at_ms INTEGER NOT NULL,
  expires_at_ms INTEGER NOT NULL,
  confirmed_at_ms INTEGER,
  claim_started_at_ms INTEGER,
  committed_at_ms INTEGER,
  acknowledged_at_ms INTEGER,
  result_version INTEGER NOT NULL DEFAULT 1 CHECK(result_version=1),
  channels_count INTEGER NOT NULL DEFAULT 0 CHECK(channels_count>=0),
  models_count INTEGER NOT NULL DEFAULT 0 CHECK(models_count>=0),
  groups_count INTEGER NOT NULL DEFAULT 0 CHECK(groups_count>=0),
  rename_count INTEGER NOT NULL DEFAULT 0 CHECK(rename_count>=0),
  result_json TEXT,
  failure_code TEXT,
  failure_stage TEXT,
  CHECK(expires_at_ms > created_at_ms),
  CHECK(status='committed' OR (channels_count=0 AND models_count=0
         AND groups_count=0 AND rename_count=0)),
  CHECK((status='committed' AND committed_at_ms IS NOT NULL
         AND result_json IS NOT NULL AND failure_code IS NULL)
     OR (status='prepared' AND committed_at_ms IS NULL AND result_json IS NULL)
     OR (status='aborted' AND committed_at_ms IS NULL
         AND result_json IS NULL AND failure_code IS NOT NULL))
)`
const ownerIndexDDL = `CREATE INDEX IF NOT EXISTS idx_ct_prepared_owner ON config_import_receipts(status, owner_instance_id)`
const recoveryIndexDDL = `CREATE INDEX IF NOT EXISTS idx_ct_unack_confirmed ON config_import_receipts(created_at_ms DESC, operation_id DESC) WHERE acknowledged_at_ms IS NULL AND confirmed_at_ms IS NOT NULL`

func silentDB(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}
func normalizedDDL(sql string) string {
	sql = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(sql), ";"))
	sql = strings.ReplaceAll(sql, "if not exists", "")
	return strings.Join(strings.FieldsFunc(sql, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == '`' || r == '"' }), "")
}

// Migrate creates only the receipt table and its two indexes. Unknown existing
// definitions fail closed; the application never silently repairs user schemas.
func Migrate(db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return NewError("UNSUPPORTED_DATABASE_SCHEMA", "recovery")
	}
	err := silentDB(db).Transaction(func(tx *gorm.DB) error {
		existingTable := false
		for _, entry := range []struct{ name, kind, ddl string }{{"config_import_receipts", "table", receiptDDL}, {"idx_ct_prepared_owner", "index", ownerIndexDDL}, {"idx_ct_unack_confirmed", "index", recoveryIndexDDL}} {
			var saved struct{ SQL string }
			if err := tx.Raw("SELECT sql FROM sqlite_master WHERE name = ? AND type = ?", entry.name, entry.kind).Scan(&saved).Error; err != nil {
				return err
			}
			if saved.SQL != "" && normalizedDDL(saved.SQL) != normalizedDDL(entry.ddl) {
				return NewError("UNSUPPORTED_DATABASE_SCHEMA", "recovery")
			}
			if entry.kind == "table" {
				existingTable = saved.SQL != ""
			}
			if existingTable && entry.kind == "index" && saved.SQL == "" {
				return NewError("UNSUPPORTED_DATABASE_SCHEMA", "recovery")
			}
			if saved.SQL == "" {
				if err := tx.Exec(entry.ddl).Error; err != nil {
					return err
				}
			}
		}
		return checkBusinessSchema(tx)
	})
	if err != nil {
		if isBusy(err) {
			return NewError("DB_BUSY", "recovery")
		}
		return NewError("UNSUPPORTED_DATABASE_SCHEMA", "recovery")
	}
	return nil
}

type indexRow struct {
	Name    string
	Unique  int
	Partial int
}
type indexColumn struct {
	Seqno int
	Cid   int
	Name  string
	Coll  string
	Key   int
}

func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func checkBusinessSchema(db *gorm.DB) error {
	for _, entry := range []struct {
		table   string
		columns []string
	}{{"channels", []string{"name"}}, {"models", []string{"channel_id", "name"}}, {"groups", []string{"name"}}, {"group_items", []string{"group_id", "model_id"}}} {
		var indexes []indexRow
		if err := db.Raw("PRAGMA index_list(" + quoteIdentifier(entry.table) + ")").Scan(&indexes).Error; err != nil {
			return err
		}
		matched := false
		for _, idx := range indexes {
			if idx.Unique == 0 {
				continue
			}
			var cols []indexColumn
			if err := db.Raw("PRAGMA index_xinfo(" + quoteIdentifier(idx.Name) + ")").Scan(&cols).Error; err != nil {
				return err
			}
			actual := []string{}
			for _, col := range cols {
				if col.Key == 1 {
					if col.Cid < 0 || col.Coll != "BINARY" {
						return NewError("UNSUPPORTED_DATABASE_SCHEMA", "recovery")
					}
					actual = append(actual, col.Name)
				}
			}
			if idx.Partial != 0 || strings.Join(actual, "\x00") != strings.Join(entry.columns, "\x00") {
				return NewError("UNSUPPORTED_DATABASE_SCHEMA", "recovery")
			}
			matched = true
		}
		if !matched {
			return NewError("UNSUPPORTED_DATABASE_SCHEMA", "recovery")
		}
	}
	return nil
}
