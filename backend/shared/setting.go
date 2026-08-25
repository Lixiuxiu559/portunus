package shared

import "fmt"

// Currency 是费用计价的货币单位，仅支持人民币与美元。
type Currency string

const (
	CurrencyUSD Currency = "USD" // 美元
	CurrencyCNY Currency = "CNY" // 人民币
)

// Valid 校验货币是否受支持。
func (c Currency) Valid() bool {
	return c == CurrencyUSD || c == CurrencyCNY
}

// SettingKey 是全局设置项。
type SettingKey string

const (
	SettingKeyCurrency SettingKey = "currency" // 费用计价货币
)

// Setting 是全局设置项，key-value 存储。
type Setting struct {
	Key   SettingKey `gorm:"primaryKey" json:"key"`
	Value string     `gorm:"not null" json:"value"`
}

// DefaultSettings 返回全局默认设置。
func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyCurrency, Value: string(CurrencyUSD)},
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

// GetCurrency 返回当前货币设置；未初始化或缺失时回退 USD。
func GetCurrency() Currency {
	if DB == nil {
		return CurrencyUSD
	}
	var s Setting
	if err := DB.First(&s, SettingKeyCurrency).Error; err != nil {
		return CurrencyUSD
	}
	return Currency(s.Value)
}

// SetCurrency 更新货币设置。
func SetCurrency(c Currency) error {
	if !c.Valid() {
		return &StatusError{Status: 400, Message: fmt.Sprintf("不支持的货币: %s", c)}
	}
	if DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	return DB.Save(&Setting{Key: SettingKeyCurrency, Value: string(c)}).Error
}
