package model

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// syncClient 拉取上游模型列表专用，设超时避免拖垮渠道保存请求。
var syncClient = &http.Client{Timeout: 15 * time.Second}

// SyncFromChannel 从渠道上游拉取模型列表，把本地缺失的模型同步到模型表。
// 已存在（channel_id + name）的模型不覆盖，保留用户手动设置的价格与启停状态。
// 返回新增的模型数量。
func SyncFromChannel(ch *channel.Channel) (int, error) {
	names, err := fetchModelNames(ch)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, name := range names {
		var count int64
		if err := shared.DB.Model(&Model{}).Where("channel_id = ? AND name = ?", ch.ID, name).Count(&count).Error; err != nil {
			return added, err
		}
		if count > 0 {
			continue
		}
		m := Model{ChannelID: ch.ID, Name: name, Enabled: true}
		m.ApplyDefaultPrice()
		if err := shared.DB.Create(&m).Error; err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// fetchModelNames 按渠道协议拉取上游模型名列表。
func fetchModelNames(ch *channel.Channel) ([]string, error) {
	base := strings.TrimRight(ch.BaseURL, "/")
	url := base + "/models"
	if ch.Type == protocol.ProviderGemini {
		url += "?key=" + ch.Key
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	switch ch.Type {
	case protocol.ProviderAnthropic:
		req.Header.Set("x-api-key", ch.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case protocol.ProviderGemini:
		// key 已作为 query 参数，无需额外请求头
	default: // openai / openai_responses
		req.Header.Set("Authorization", "Bearer "+ch.Key)
	}

	resp, err := syncClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("拉取模型列表失败: %s", resp.Status)
	}

	if ch.Type == protocol.ProviderGemini {
		return parseGeminiModels(body)
	}
	return parseOpenAIModels(body)
}

// parseOpenAIModels 解析 OpenAI / Anthropic 的模型列表（data 数组，元素含 id）。
func parseOpenAIModels(body []byte) ([]string, error) {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Data))
	for _, d := range resp.Data {
		if d.ID != "" {
			names = append(names, d.ID)
		}
	}
	return names, nil
}

// parseGeminiModels 解析 Gemini 的模型列表（models 数组，name 带 models/ 前缀）。
func parseGeminiModels(body []byte) ([]string, error) {
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Models))
	for _, m := range resp.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}
