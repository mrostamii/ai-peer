package ollama

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthCheckOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	cli := New(srv.URL)
	if err := cli.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"},{"model":"qwen2.5:7b"}]}`))
	}))
	defer srv.Close()

	cli := New(srv.URL)
	got, err := cli.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(got) != 2 || got[0] != "llama3.2:latest" || got[1] != "qwen2.5:7b" {
		t.Fatalf("unexpected models: %#v", got)
	}
}

func TestChatCompletion(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"llama3.2:latest","message":{"role":"assistant","content":"hello"},"prompt_eval_count":10,"eval_count":5,"done":true}`))
	}))
	defer srv.Close()

	cli := New(srv.URL)
	out, err := cli.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "llama3.2:latest",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if out.Message.Content != "hello" || out.PromptEvalCount != 10 || out.EvalCount != 5 {
		t.Fatalf("unexpected chat response: %#v", out)
	}
}

func TestStreamChatCompletion(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("expected stream=true request body, got: %s", string(body))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"message\":{\"content\":\"chunk-1\"}}\n{\"done\":true}\n"))
	}))
	defer srv.Close()

	cli := New(srv.URL)
	rc, err := cli.StreamChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "llama3.2:latest",
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read stream response: %v", err)
	}
	if !strings.Contains(string(out), "chunk-1") {
		t.Fatalf("unexpected stream body: %s", string(out))
	}
}
