package channel

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func testPNGIcon(t *testing.T, width, height int) string {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return customIconPrefix + base64.StdEncoding.EncodeToString(data.Bytes())
}

func TestValidateIcon(t *testing.T) {
	good := testPNGIcon(t, 96, 96)
	for _, icon := range []string{"", "deepseek", "unknown-brand_2", good, testPNGIcon(t, 256, 256)} {
		if err := validateIcon(icon); err != nil {
			t.Fatalf("valid icon rejected: %q: %v", icon[:min(len(icon), 40)], err)
		}
	}
	for name, icon := range map[string]string{
		"external URL":   "https://example.test/icon.png",
		"SVG":            "data:image/svg+xml;base64,PHN2Zz4=",
		"fake PNG":       customIconPrefix + base64.StdEncoding.EncodeToString([]byte("not a PNG")),
		"truncated PNG":  good[:len(good)-16],
		"header only":    customIconPrefix + strings.TrimPrefix(good, customIconPrefix)[:44],
		"wide PNG":       testPNGIcon(t, 257, 96),
		"tall PNG":       testPNGIcon(t, 96, 257),
		"long data":      customIconPrefix + strings.Repeat("A", maxIconLength),
		"long name":      strings.Repeat("a", 65),
		"invalid name":   "../deepseek",
		"invalid base64": customIconPrefix + "?",
	} {
		t.Run(name, func(t *testing.T) {
			err := validateIcon(icon)
			status, ok := err.(*shared.StatusError)
			if !ok || status.Status != 400 {
				t.Fatalf("invalid icon error = %v, want 400", err)
			}
		})
	}
}

func TestIconPersistenceAndLegacyMetadataUpdate(t *testing.T) {
	setupExistsTest(t)
	icon := testPNGIcon(t, 96, 96)
	c, err := Create(CreateRequest{Name: "icon", ProviderID: "deepseek", Key: "original-secret", Icon: icon})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := Get(c.ID)
	if err != nil || saved.Icon != icon || saved.ToResponse().Icon != icon {
		t.Fatalf("icon lost after create: %+v, %v", saved, err)
	}
	name := "renamed"
	if c, err = Update(c.ID, UpdateRequest{Name: &name}); err != nil || c.Icon != icon {
		t.Fatalf("omitted icon changed: %+v, %v", c, err)
	}
	empty := ""
	if c, err = Update(c.ID, UpdateRequest{Icon: &empty}); err != nil || c.Icon != "" {
		t.Fatalf("clear failed: %+v, %v", c, err)
	}
	legacy := Channel{Name: "legacy-icon", Type: protocol.ProviderOpenAI, BaseURL: "https://legacy.invalid/models?old=1", Key: "legacy-secret"}
	if err := shared.DB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	brand := "deepseek"
	c, err = Update(legacy.ID, UpdateRequest{Icon: &brand})
	if err != nil || c.Icon != brand || c.Key != legacy.Key || c.ConnectionConfig() != legacy.ConnectionConfig() {
		t.Fatalf("metadata edit changed connection: %+v, %v", c, err)
	}
	bad := "https://example.test/icon.png"
	if _, err := Update(legacy.ID, UpdateRequest{Icon: &bad}); err == nil {
		t.Fatal("invalid update accepted")
	}
	saved, err = Get(legacy.ID)
	if err != nil || saved.Icon != brand {
		t.Fatalf("rejected update mutated database: %+v, %v", saved, err)
	}
	if _, err := Create(CreateRequest{Name: "bad-icon", ProviderID: "openai", Key: "secret", Icon: bad}); err == nil {
		t.Fatal("invalid create accepted")
	}
}
