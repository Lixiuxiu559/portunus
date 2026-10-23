package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/model"
)

func TestChannelConcurrencyAPI(t *testing.T) {
	setupCascadeTest(t)
	r := channelTestRouter()
	w := channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"limited","key":"secret","max_concurrent_requests":2,"models":["m1"]}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var c channel.Response
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.MaxConcurrentRequests != 2 {
		t.Fatalf("created limit=%d", c.MaxConcurrentRequests)
	}
	path := fmt.Sprintf("/api/channels/%d", c.ID)
	for _, method := range []string{"GET", "PUT"} {
		w = channelRequest(r, method, path, `{"name":"renamed"}`)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if c.MaxConcurrentRequests != 2 {
			t.Fatal("omitted limit lost")
		}
	}
	for _, value := range []string{`-1`, `1.5`, `"2"`, `true`, `9007199254740992`, `9223372036854775808`} {
		w = channelRequest(r, "PUT", path, `{"name":"must-not-save","max_concurrent_requests":`+value+`}`)
		if w.Code != 400 {
			t.Fatalf("invalid update %s: %d %s", value, w.Code, w.Body.String())
		}
		w = channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"invalid","key":"secret","max_concurrent_requests":`+value+`}`)
		if w.Code != 400 {
			t.Fatalf("invalid create %s: %d %s", value, w.Code, w.Body.String())
		}
	}
	saved, err := channel.Get(c.ID)
	if err != nil || saved.Name != "renamed" || saved.MaxConcurrentRequests != 2 {
		t.Fatalf("invalid update mutated: %+v %v", saved, err)
	}
	// 模型导入失败必须回滚同一事务中的并发设置。
	w = channelRequest(r, "PUT", path, `{"max_concurrent_requests":7,"models":[""]}`)
	if w.Code != 400 {
		t.Fatalf("invalid model: %d %s", w.Code, w.Body.String())
	}
	saved, err = channel.Get(c.ID)
	if err != nil || saved.MaxConcurrentRequests != 2 {
		t.Fatalf("model failure changed limit: %+v %v", saved, err)
	}
	_, total, err := model.List(c.ID, "", 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("models changed: %d %v", total, err)
	}
	w = channelRequest(r, "PUT", path, `{"max_concurrent_requests":0}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || c.MaxConcurrentRequests != 0 {
		t.Fatalf("disable: %+v %v", c, err)
	}
	w = channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"default","key":"secret"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || c.MaxConcurrentRequests != 0 {
		t.Fatalf("default: %+v %v", c, err)
	}
}

func TestChannelConcurrencyAPISafeIntegerMaximum(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("安全整数边界超出本机 int 范围，JSON binding 会先拒绝")
	}
	setupCascadeTest(t)
	r := channelTestRouter()
	w := channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"maximum","key":"secret","max_concurrent_requests":9007199254740991}`)
	if w.Code != 201 {
		t.Fatalf("create maximum: %d %s", w.Code, w.Body.String())
	}
	var c channel.Response
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || int64(c.MaxConcurrentRequests) != channel.MaxConcurrentRequestsLimit {
		t.Fatalf("maximum response: %+v %v", c, err)
	}
	path := fmt.Sprintf("/api/channels/%d", c.ID)
	for _, body := range []string{`{"name":"renamed"}`, `{"max_concurrent_requests":9007199254740991}`} {
		w = channelRequest(r, "PUT", path, body)
		if w.Code != 200 {
			t.Fatalf("update maximum: %d %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || int64(c.MaxConcurrentRequests) != channel.MaxConcurrentRequestsLimit {
			t.Fatalf("maximum update response: %+v %v", c, err)
		}
	}
}
