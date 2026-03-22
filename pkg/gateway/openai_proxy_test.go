package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	p := NewOpenAIProxy("127.0.0.1:0", ollama.URL)
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
