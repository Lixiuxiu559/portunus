package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Upstream 是一个上游接入器：给定协议类型与上游连接配置（base_url / key），
// 产出该协议的上游端点、鉴权头与模型列表。
// 调用方（gateway / model）不再需要按协议 switch 拼端点、鉴权、解析模型集。
type Upstream interface {
	// ChatURL 拼出该协议发送对话请求的完整 URL。
	ChatURL(model string, stream bool) string
	// ChatHeaders 返回该协议发送对话请求所需的鉴权 / 内容头。
	ChatHeaders() http.Header
	// FetchModels 拉取并解析上游模型名列表，只读不落库。
	FetchModels() ([]string, error)
}

// baseConfig 是各接入器共享的上游连接配置。
type baseConfig struct {
	baseURL string
	key     string
}

// NewUpstream 按协议构建上游接入器，baseURL 只取基础地址（具体路径由各实现补全）。
func NewUpstream(p Provider, baseURL, key string) (Upstream, error) {
	impl, ok := implFor(p)
	if !ok {
		return nil, fmt.Errorf("未知协议: %s", p)
	}
	return impl.newUpstream(baseConfig{baseURL: strings.TrimRight(baseURL, "/"), key: key}), nil
}

// ===== 各协议上游工厂 =====

// newBearerUpstream 返回构造 OpenAI Chat Completions / Responses 上游的工厂，
// 二者仅对话路径不同，鉴权（Bearer）与模型集（/models + OpenAI 形状）完全一致。
func newBearerUpstream(chatPath string) func(baseConfig) Upstream {
	return func(cfg baseConfig) Upstream {
		return &bearerUpstream{baseConfig: cfg, chatPath: chatPath}
	}
}

func newAnthropicUpstream(cfg baseConfig) Upstream {
	return &anthropicUpstream{baseConfig: cfg}
}

func newGeminiUpstream(cfg baseConfig) Upstream {
	return &geminiUpstream{baseConfig: cfg}
}

// upstreamClient 拉取模型列表专用，设超时避免拖垮调用方。
var upstreamClient = &http.Client{Timeout: 15 * time.Second}

// fetchModels 拉取模型列表并把响应体交给 parse 解析。
func fetchModels(url string, header http.Header, parse func([]byte) ([]string, error)) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header = header
	resp, err := upstreamClient.Do(req)
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
	return parse(body)
}

// bearerUpstream 覆盖 OpenAI Chat Completions / Responses：二者仅对话路径不同，
// 鉴权（Bearer）与模型集（/models + OpenAI 形状）完全一致。
type bearerUpstream struct {
	baseConfig
	chatPath string
}

func (u *bearerUpstream) ChatURL(string, bool) string {
	return u.baseURL + u.chatPath
}

func (u *bearerUpstream) ChatHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+u.key)
	return h
}

func (u *bearerUpstream) FetchModels() ([]string, error) {
	return fetchModels(u.baseURL+"/models", u.ChatHeaders(), parseOpenAIModels)
}

// anthropicUpstream 覆盖 Anthropic Messages：x-api-key 鉴权 + anthropic-version。
type anthropicUpstream struct {
	baseConfig
}

func (u *anthropicUpstream) ChatURL(string, bool) string {
	return u.baseURL + "/messages"
}

func (u *anthropicUpstream) ChatHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("x-api-key", u.key)
	h.Set("anthropic-version", "2023-06-01")
	return h
}

func (u *anthropicUpstream) FetchModels() ([]string, error) {
	return fetchModels(u.baseURL+"/models", u.ChatHeaders(), parseOpenAIModels)
}

// geminiUpstream 覆盖 Gemini：模型名进 URL，鉴权用 x-goog-api-key，
// 模型集 key 走 query 参数而非请求头。
type geminiUpstream struct {
	baseConfig
}

func (u *geminiUpstream) ChatURL(model string, stream bool) string {
	suffix := ":generateContent"
	if stream {
		suffix = ":streamGenerateContent"
	}
	return u.baseURL + "/models/" + model + suffix
}

func (u *geminiUpstream) ChatHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("x-goog-api-key", u.key)
	return h
}

func (u *geminiUpstream) FetchModels() ([]string, error) {
	return fetchModels(u.baseURL+"/models?key="+u.key, http.Header{}, parseGeminiModels)
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
