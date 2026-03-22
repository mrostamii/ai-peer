package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mrostamii/ai-peer/pkg/registry"
)

func TestHandleChatCompletionsStream(t *testing.T) {
	t.Parallel()
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		reqBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !strings.Contains(string(reqBody), `"stream":true`) {
			t.Fatalf("expected stream=true in request body, got: %s", string(reqBody))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"llama3.2:latest","message":{"role":"assistant","content":"hello"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"model":"llama3.2:latest","message":{"role":"assistant","content":""},"done":true}` + "\n"))
	}))
	defer ollama.Close()

	p := NewOpenAIProxy("127.0.0.1:0", ollama.URL, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"llama3.2:latest",
		"messages":[{"role":"user","content":"say hello"}],
		"stream":true
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	p.handleChatCompletions(rr, req)
	res := rr.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(got), "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "chat.completion.chunk") {
		t.Fatalf("expected chunk response, got: %s", s)
	}
	if !strings.Contains(s, "data: [DONE]") {
		t.Fatalf("expected [DONE] marker, got: %s", s)
	}
}

func TestHandleNetworkNodes(t *testing.T) {
	t.Parallel()
	reg := registry.New(time.Minute)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	payload := fmt.Sprintf(`{"node_id":"peer-xyz","uptime_sec":10,"timestamp_ms":%d}`, ts.UnixMilli())
	if err := reg.ApplyHealthJSON([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", reg)
	req := httptest.NewRequest(http.MethodGet, "/v1/network/nodes", nil)
	rr := httptest.NewRecorder()
	p.handleNetworkNodes(rr, req)
	res := rr.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "peer-xyz") {
		t.Fatalf("expected peer in body: %s", body)
	}
}
