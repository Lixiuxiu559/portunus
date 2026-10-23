package channel

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"regexp"
	"strings"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

const (
	customIconPrefix = "data:image/png;base64,"
	maxIconLength    = 64 * 1024
	maxIconDimension = 256
)

var builtinIconName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// validateIcon 在像素解码前检查尺寸，限制 data URI 大小和解码内存。
// 内置名称允许未知值，旧客户端或扩展目录仍可保存并由前端兜底。
func validateIcon(icon string) error {
	if icon == "" || builtinIconName.MatchString(icon) {
		return nil
	}
	invalid := &shared.StatusError{Status: 400, Message: "渠道图标无效，请选择内置图标或重新上传图片"}
	if len(icon) > maxIconLength || !strings.HasPrefix(icon, customIconPrefix) {
		return invalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(icon, customIconPrefix))
	if err != nil {
		return invalid
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxIconDimension || config.Height > maxIconDimension {
		return invalid
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return invalid
	}
	return nil
}
