package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Config 是服务的全局配置，来源为 JSON 文件 + 环境变量覆盖。
type Config struct {
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"server"`
	Database struct {
		Type    string `json:"type"` // sqlite / mysql / postgres
		Path    string `json:"path"`
		LogPath string `json:"log_path"` // 可选：日志库独立路径，空则复用主库
	} `json:"database"`
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *Config {
	c := &Config{}
	c.Server.Host = "0.0.0.0"
	c.Server.Port = 8080
	c.Database.Type = "sqlite"
	c.Database.Path = "data/portunus.db"
	return c
}

// Load 从指定路径加载配置；文件不存在时用默认值。环境变量 PORTUNUS_* 覆盖对应字段。
func Load(path string) (*Config, error) {
	c := DefaultConfig()

	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	applyEnv(c)
	return c, nil
}

// applyEnv 用 PORTUNUS_SERVER_PORT 形式的环境变量覆盖配置。
func applyEnv(c *Config) {
	if v := os.Getenv("PORTUNUS_SERVER_HOST"); v != "" {
		c.Server.Host = v
	}
	if v := os.Getenv("PORTUNUS_SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Server.Port = n
		}
	}
	if v := os.Getenv("PORTUNUS_DATABASE_TYPE"); v != "" {
		c.Database.Type = v
	}
	if v := os.Getenv("PORTUNUS_DATABASE_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("PORTUNUS_DATABASE_LOG_PATH"); v != "" {
		c.Database.LogPath = v
	}
}
