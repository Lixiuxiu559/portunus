package shared

import (
	"fmt"
	"strconv"
	"time"
)

// SettingKey 是全局设置项。
type SettingKey string

const (
	SettingKeySyncInterval     SettingKey = "sync_interval"      // 自动同步间隔（分钟，0 = 关闭）
	SettingKeyLastSyncAt       SettingKey = "last_sync_at"       // 上次同步时间（Unix 秒，0 = 从未）
	SettingKeyLogRetentionDays SettingKey = "log_retention_days" // 调用日志保留天数（0 = 不自动清理）
)

// Setting 是全局设置项，key-value 存储。
type Setting struct {
	Key   SettingKey `gorm:"primaryKey" json:"key"`
	Value string     `gorm:"not null" json:"value"`
}

// DefaultSettings 返回全局默认设置。
func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeySyncInterval, Value: "360"},
		{Key: SettingKeyLastSyncAt, Value: "0"},
		{Key: SettingKeyLogRetentionDays, Value: "30"},
	}
}

// EnsureDefaultSettings 为缺失的设置项写入默认值，应在迁移后调用。
func EnsureDefaultSettings() error {
	for _, s := range DefaultSettings() {
		var count int64
		if err := DB.Model(&Setting{}).Where("key = ?", s.Key).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := DB.Create(&s).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// SyncIntervalDefault 是自动同步间隔的默认值（分钟）。
const SyncIntervalDefault = 360

// GetSyncInterval 返回自动同步间隔（分钟）；未初始化或解析失败回退默认值。
func GetSyncInterval() int {
	if DB == nil {
		return SyncIntervalDefault
	}
	var s Setting
	if err := DB.First(&s, SettingKeySyncInterval).Error; err != nil {
		return SyncIntervalDefault
	}
	n, err := strconv.Atoi(s.Value)
	if err != nil {
		return SyncIntervalDefault
	}
	return n
}

// SetSyncInterval 更新自动同步间隔；minutes 为 0 表示关闭自动同步，负数不合法。
func SetSyncInterval(minutes int) error {
	if minutes < 0 {
		return &StatusError{Status: 400, Message: "同步间隔不能为负"}
	}
	return saveSetting(SettingKeySyncInterval, strconv.Itoa(minutes))
}

// GetLastSyncAt 返回上次同步时间（Unix 秒）；缺失或失败返回 0（从未）。
func GetLastSyncAt() int64 {
	if DB == nil {
		return 0
	}
	var s Setting
	if err := DB.First(&s, SettingKeyLastSyncAt).Error; err != nil {
		return 0
	}
	n, err := strconv.ParseInt(s.Value, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// SetLastSyncAt 记录上次同步时间。
func SetLastSyncAt(t time.Time) error {
	return saveSetting(SettingKeyLastSyncAt, strconv.FormatInt(t.Unix(), 10))
}

// LogRetentionDaysDefault 是调用日志保留天数的默认值。
const LogRetentionDaysDefault = 30

// GetLogRetentionDays 返回日志保留天数；未初始化或解析失败回退默认值。
func GetLogRetentionDays() int {
	if DB == nil {
		return LogRetentionDaysDefault
	}
	var s Setting
	if err := DB.First(&s, SettingKeyLogRetentionDays).Error; err != nil {
		return LogRetentionDaysDefault
	}
	n, err := strconv.Atoi(s.Value)
	if err != nil || n < 0 {
		return LogRetentionDaysDefault
	}
	return n
}

// SetLogRetentionDays 更新日志保留天数；0 表示禁用自动清理，负数不合法。
func SetLogRetentionDays(days int) error {
	if days < 0 {
		return &StatusError{Status: 400, Message: "日志保留天数不能为负"}
	}
	return saveSetting(SettingKeyLogRetentionDays, strconv.Itoa(days))
}

// saveSetting 以 key 为主键 upsert 一条设置。
func saveSetting(key SettingKey, value string) error {
	if DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	return DB.Save(&Setting{Key: key, Value: value}).Error
}
