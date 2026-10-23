package gateway

import (
	"github.com/Lixiuxiu559/portunus/backend/resource"
	"net/http"
)

func readUpstreamBody(resp *http.Response, max int64) ([]byte, error) {
	if err := resource.CheckSize(resp.ContentLength, max, resource.Response, "body_bytes"); err != nil {
		return nil, err
	}
	return resource.ReadAll(resp.Body, max, resource.Response)
}
