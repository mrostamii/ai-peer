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

func TestHandleChatCompletionsRetriesNextBestNode(t *testing.T) {
	t.Parallel()
	reg := registry.New(time.Minute)
	now := time.Now().UnixMilli()
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-a","uptime_sec":200,"load":0.1,"latency_ms":5,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-b","uptime_sec":100,"load":0.4,"latency_ms":20,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: "peer-a", Models: []string{"llama3.2:latest"}, TimestampMs: now})
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: "peer-b", Models: []string{"llama3.2:latest"}, TimestampMs: now})

	p := NewOpenAIProxy("127.0.0.1:0", "http://127.0.0.1:1", reg)
	var tried []string
	p.SetRemoteChatFunc(func(_ context.Context, nodeID string, _ *RemoteChatRequest) (*RemoteChatResponse, error) {
		tried = append(tried, nodeID)
		if nodeID == "peer-a" {
			return nil, fmt.Errorf("transient")
		}
		return &RemoteChatResponse{
			Model:            "llama3.2:latest",
			Content:          "from-second-node",
			CompletionTokens: 12,
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
	if len(tried) != 2 || tried[0] != "peer-a" || tried[1] != "peer-b" {
		t.Fatalf("unexpected retry order: %v", tried)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "from-second-node") {
		t.Fatalf("expected fallback node response, got: %s", string(body))
	}
}

func TestHandleChatCompletionsLimitsRetriesToTwo(t *testing.T) {
	t.Parallel()
	reg := registry.New(time.Minute)
	now := time.Now().UnixMilli()
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("peer-%d", i)
		_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"%s","uptime_sec":100,"load":0.1,"latency_ms":5,"timestamp_ms":%d}`, id, now)))
		_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: id, Models: []string{"llama3.2:latest"}, TimestampMs: now})
	}
	p := NewOpenAIProxy("127.0.0.1:0", "http://127.0.0.1:1", reg)
	attempts := 0
	p.SetRemoteChatFunc(func(_ context.Context, _ string, _ *RemoteChatRequest) (*RemoteChatResponse, error) {
		attempts++
		return nil, fmt.Errorf("always fail")
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"llama3.2:latest",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleChatCompletions(rr, req)
	if attempts != 3 {
		t.Fatalf("attempts=%d want 3 (initial + 2 retries)", attempts)
	}
}

func TestHandleChatCompletionsPrefersLowerPing(t *testing.T) {
	t.Parallel()
	reg := registry.New(time.Minute)
	now := time.Now().UnixMilli()
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-a","uptime_sec":100,"load":0.1,"latency_ms":5,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-b","uptime_sec":100,"load":0.1,"latency_ms":25,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: "peer-a", Models: []string{"llama3.2:latest"}, TimestampMs: now})
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: "peer-b", Models: []string{"llama3.2:latest"}, TimestampMs: now})

	p := NewOpenAIProxy("127.0.0.1:0", "http://127.0.0.1:1", reg)
	p.SetPeerLatencyFunc(func(_ context.Context, nodeID string) (time.Duration, error) {
		if nodeID == "peer-a" {
			return 80 * time.Millisecond, nil
		}
		if nodeID == "peer-b" {
			return 12 * time.Millisecond, nil
		}
		return 0, fmt.Errorf("unknown node %s", nodeID)
	})
	var tried []string
	p.SetRemoteChatFunc(func(_ context.Context, nodeID string, _ *RemoteChatRequest) (*RemoteChatResponse, error) {
		tried = append(tried, nodeID)
		return &RemoteChatResponse{
			Model:            "llama3.2:latest",
			Content:          "ok",
			CompletionTokens: 5,
		}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"llama3.2:latest",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleChatCompletions(rr, req)
	if len(tried) == 0 {
		t.Fatal("expected at least one remote attempt")
	}
	if tried[0] != "peer-b" {
		t.Fatalf("expected lower-ping peer first, got %v", tried)
	}
}

func TestRankedNodesForModelUsesLoadLatencyUptimeOrdering(t *testing.T) {
	t.Parallel()
	reg := registry.New(time.Minute)
	now := time.Now().UnixMilli()

	// same model on all nodes; ordering should be:
	// 1) lower load, 2) lower latency, 3) higher uptime
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-a","uptime_sec":100,"load":0.3,"latency_ms":5,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-b","uptime_sec":120,"load":0.1,"latency_ms":40,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyHealthJSON([]byte(fmt.Sprintf(`{"node_id":"peer-c","uptime_sec":80,"load":0.1,"latency_ms":10,"timestamp_ms":%d}`, now)))
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: "peer-a", Models: []string{"llama3.2:latest"}, TimestampMs: now})
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: "peer-b", Models: []string{"llama3.2:latest"}, TimestampMs: now})
	_ = reg.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{NodeId: "peer-c", Models: []string{"llama3.2:latest"}, TimestampMs: now})

	nodes := rankedNodesForModel(reg, "llama3.2:latest")
	if len(nodes) != 3 {
		t.Fatalf("len(nodes)=%d want 3", len(nodes))
	}
	// peer-c wins over peer-b due to lower latency at same load.
	if nodes[0].NodeID != "peer-c" || nodes[1].NodeID != "peer-b" || nodes[2].NodeID != "peer-a" {
		t.Fatalf("unexpected ranking: [%s %s %s]", nodes[0].NodeID, nodes[1].NodeID, nodes[2].NodeID)
	}
}

func TestReorderNodesByPingFallsBackWhenPingFails(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	p.SetPeerLatencyFunc(func(_ context.Context, nodeID string) (time.Duration, error) {
		if nodeID == "peer-a" {
			return 0, fmt.Errorf("ping failed")
		}
		if nodeID == "peer-b" {
			return 9 * time.Millisecond, nil
		}
		return 0, fmt.Errorf("unknown node")
	})

	nodes := []registry.NodeRecord{
		{NodeID: "peer-a", LatencyMs: 3},
		{NodeID: "peer-b", LatencyMs: 30},
	}
	got := p.reorderNodesByPing(context.Background(), nodes)
	if len(got) != 2 {
		t.Fatalf("len(got)=%d want 2", len(got))
	}
	// peer-a keeps its fallback latency (3ms) because ping failed; peer-b uses ping (9ms).
	if got[0].NodeID != "peer-a" || got[1].NodeID != "peer-b" {
		t.Fatalf("unexpected order after ping fallback: [%s %s]", got[0].NodeID, got[1].NodeID)
	}
	if got[0].LatencyMs != 3 || got[1].LatencyMs != 9 {
		t.Fatalf("unexpected latencies after reorder: [%d %d]", got[0].LatencyMs, got[1].LatencyMs)
	}
}

func TestHandleChatCompletionsStreamFirstTokenTimeout(t *testing.T) {
	t.Parallel()
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"llama3.2:latest","message":{"role":"assistant","content":"late"},"done":true}` + "\n"))
	}))
	defer ollama.Close()

	p := NewOpenAIProxy("127.0.0.1:0", ollama.URL, nil)
	p.SetTimeouts(20*time.Millisecond, 2*time.Second)
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
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected status 504, got %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !strings.Contains(string(body), "first token timeout") {
		t.Fatalf("expected first token timeout, got: %s", string(body))
	}
}
