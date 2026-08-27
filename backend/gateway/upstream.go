package gateway

import (
	"bytes"
	"net/http"
)

// httpClient 不设总超时，避免截断长流式响应。
var httpClient = &http.Client{}

// doRequest 发送上游 POST 请求，返回响应（body 由调用方负责关闭与读取）。
// 端点与鉴权头由 protocol.Upstream 提供。
func doRequest(url string, headers http.Header, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = headers
	return httpClient.Do(req)
}
