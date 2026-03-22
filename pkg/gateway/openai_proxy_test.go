package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mrostamii/ai-peer/pkg/apiv1"
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

func TestHandleModelsIncludesRegistryModels(t *testing.T) {
	t.Parallel()
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"}]}`))
	}))
	defer ollama.Close()

	reg := registry.New(time.Minute)
	now := time.Now().UnixMilli()
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-1","uptime_sec":1,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{
		NodeId:      "peer-1",
		Models:      []string{"qwen2.5:7b", "llama3.2:latest"},
		TimestampMs: now,
	})

	p := NewOpenAIProxy("127.0.0.1:0", ollama.URL, reg)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	p.handleModels(rr, req)
	res := rr.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}

	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := make([]string, 0, len(out.Data))
	for _, item := range out.Data {
		got = append(got, item.ID)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "llama3.2:latest,qwen2.5:7b" {
		t.Fatalf("models=%v want [llama3.2:latest qwen2.5:7b]", got)
	}
}

func TestHandleChatCompletionsUsesRemoteNode(t *testing.T) {
	t.Parallel()
	reg := registry.New(time.Minute)
	now := time.Now().UnixMilli()
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-remote","uptime_sec":1,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{
		NodeId:      "peer-remote",
		Models:      []string{"llama3.2:latest"},
		TimestampMs: now,
	})
	p := NewOpenAIProxy("127.0.0.1:0", "http://127.0.0.1:1", reg)
	p.SetRemoteChatFunc(func(_ context.Context, nodeID string, req *RemoteChatRequest) (*RemoteChatResponse, error) {
		if nodeID != "peer-remote" {
			t.Fatalf("nodeID=%q want peer-remote", nodeID)
		}
		if req.Model != "llama3.2:latest" || len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
			t.Fatalf("unexpected remote req: %+v", req)
		}
		return &RemoteChatResponse{
			Model:            "llama3.2:latest",
			Content:          "from-remote",
			CompletionTokens: 9,
		}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"llama3.2:latest",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleChatCompletions(rr, req)
	res := rr.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "from-remote") {
		t.Fatalf("expected remote response body, got: %s", string(body))
	}
}

func TestHandleChatCompletionsStreamUsesRemoteNode(t *testing.T) {
	t.Parallel()
	reg := registry.New(time.Minute)
	now := time.Now().UnixMilli()
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-remote","uptime_sec":1,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{
		NodeId:      "peer-remote",
		Models:      []string{"llama3.2:latest"},
		TimestampMs: now,
	})
	p := NewOpenAIProxy("127.0.0.1:0", "http://127.0.0.1:1", reg)
	p.SetRemoteStreamChatFunc(func(_ context.Context, nodeID string, req *RemoteChatRequest) (io.ReadCloser, error) {
		if nodeID != "peer-remote" {
			t.Fatalf("nodeID=%q want peer-remote", nodeID)
		}
		if req.Model != "llama3.2:latest" || len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
			t.Fatalf("unexpected remote req: %+v", req)
		}
		raw := `{"model":"llama3.2:latest","content":"from-remote","done":false,"ok":true}` + "\n" +
			`{"model":"llama3.2:latest","done":true,"ok":true}` + "\n"
		return io.NopCloser(strings.NewReader(raw)), nil
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"llama3.2:latest",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleChatCompletions(rr, req)
	res := rr.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, "from-remote") {
		t.Fatalf("expected remote stream content, got: %s", s)
	}
	if !strings.Contains(s, "data: [DONE]") {
		t.Fatalf("expected [DONE] marker, got: %s", s)
	}
}
