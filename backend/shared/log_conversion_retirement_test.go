package shared

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRetiredConversionPolicyPreservesHistory(t *testing.T) {
	setupLogTest(t)
	old := Log{GroupName: "historical", ConversionPolicy: "strict", ConversionChecked: true, ConversionScope: "request_native_v1", ConversionIssueCount: 2}
	if err := LogDB.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := LogDB.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	var stored Log
	if err := LogDB.First(&stored, old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ConversionPolicy != "strict" || !stored.ConversionChecked || stored.ConversionIssueCount != 2 {
		t.Fatalf("history changed: %+v", stored)
	}
	current := Log{GroupName: "current", ConversionChecked: true, ConversionScope: "request_native_v1", ConversionIssueCount: 1}
	if err := LogDB.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []Log{stored, current} {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "conversion_policy") || !strings.Contains(string(data), `"conversion_checked":true`) || !strings.Contains(string(data), `"conversion_scope":"request_native_v1"`) {
			t.Fatalf("retired policy exposed or summary lost: %s", data)
		}
	}
}
