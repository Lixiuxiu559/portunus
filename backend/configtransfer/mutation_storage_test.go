package configtransfer

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestMutationStorageMissingIndexesFailClosed(t *testing.T) {
	for _, index := range []string{"idx_ct_prepared_owner", "idx_ct_unack_confirmed"} {
		t.Run(index, func(t *testing.T) {
			db := testDB(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("DROP INDEX " + index).Error; err != nil {
				t.Fatal(err)
			}
			assertCode(t, Migrate(db), "UNSUPPORTED_DATABASE_SCHEMA")
			var n int64
			db.Raw("SELECT count(*) FROM sqlite_master WHERE name=?", index).Scan(&n)
			if n != 0 {
				t.Fatal("unknown schema silently repaired")
			}
		})
	}
}

func TestMutationStorageInclusiveEntityLimits(t *testing.T) {
	for _, kind := range []string{"channels", "models", "groups", "items"} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			seedIconChannel(t, db, "")
			want := Counts{Channels: 1}
			sql := ""
			switch kind {
			case "channels":
				want.Channels = MaxChannels
				sql = `WITH RECURSIVE n(x) AS (SELECT 2 UNION ALL SELECT x+1 FROM n WHERE x<1000)
INSERT INTO channels(name,type,base_url,connection_options) SELECT printf('boundary-%d',x),'openai','https://synthetic.invalid','null' FROM n`
			case "models":
				want.Models = MaxModels
				sql = `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<50000)
INSERT INTO models(channel_id,name) SELECT 1,printf('boundary-%d',x) FROM n`
			case "items":
				want.Models = 100
				sql = `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<100)
INSERT INTO models(channel_id,name) SELECT 1,printf('boundary-%d',x) FROM n`
			case "groups":
				want.Groups = MaxGroups
				sql = `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<5000)
INSERT INTO groups(name,strategy,active_item_id) SELECT printf('boundary-%d',x),'manual',0 FROM n`
			}
			if err := db.Exec(sql).Error; err != nil {
				t.Fatal(err)
			}
			if kind == "items" {
				want.Groups = 1000
				if err := db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000)
INSERT INTO groups(name,strategy,active_item_id) SELECT printf('items-%d',x),'manual',0 FROM n`).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec("INSERT INTO group_items(group_id,model_id,priority) SELECT groups.id,models.id,0 FROM groups CROSS JOIN models").Error; err != nil {
					t.Fatal(err)
				}
			}
			p, err := snapshot(context.Background(), db, "synthetic-size-boundary")
			if err != nil || p.Counts() != want {
				t.Fatal("maximum valid snapshot truncated or rejected", p, err)
			}
			if kind == "items" && (len(p.Groups[0].Items) != 100 || len(p.Groups[999].Items) != 100) {
				t.Fatal("snapshot lost group members")
			}
		})
	}
}

func TestMutationStorageSnapshotBudgetGuards(t *testing.T) {
	for _, kind := range []string{"oversized string", "oversized count", "malformed options"} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			seedIconChannel(t, db, "")
			switch kind {
			case "oversized string":
				if err := db.Table("channels").Where("id=1").Update("key", strings.Repeat("x", MaxNameBytes+1)).Error; err != nil {
					t.Fatal(err)
				}
			case "oversized count":
				if err := db.Exec(`WITH RECURSIVE n(x) AS (SELECT 2 UNION ALL SELECT x+1 FROM n WHERE x<1001)
INSERT INTO channels(name,type,base_url) SELECT printf('overflow-%d',x),'openai','https://synthetic.invalid' FROM n`).Error; err != nil {
					t.Fatal(err)
				}
			case "malformed options":
				db.Table("channels").Where("id=1").Update("connection_options", "{")
			}
			err := db.Transaction(func(tx *gorm.DB) error { return checkSnapshotReadBudget(tx) })
			if kind == "oversized string" {
				assertCode(t, err, "SOURCE_INTEGRITY_ERROR")
			} else if kind == "oversized count" {
				assertCode(t, err, "LIMIT_EXCEEDED")
			} else {
				_, err = snapshot(context.Background(), db, "synthetic")
				assertCode(t, err, "SOURCE_INTEGRITY_ERROR")
			}
		})
	}
}
