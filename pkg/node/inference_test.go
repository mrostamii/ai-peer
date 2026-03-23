package node

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mrostamii/ai-peer/pkg/apiv1"
	"github.com/mrostamii/ai-peer/pkg/backend/ollama"
)

type fakeInferenceBackend struct {
	lastReq *ollama.ChatCompletionRequest
	resp    *ollama.ChatCompletionResponse
	stream  io.ReadCloser
	err     error
}

func (f *fakeInferenceBackend) ChatCompletion(_ context.Context, req *ollama.ChatCompletionRequest) (*ollama.ChatCompletionResponse, error) {
	f.lastReq = req
	return f.resp, f.err
}

func (f *fakeInferenceBackend) StreamChatCompletion(_ context.Context, req *ollama.ChatCompletionRequest) (io.ReadCloser, error) {
	f.lastReq = req
	return f.stream, f.err
}

func TestInferWithBackend(t *testing.T) {
	t.Parallel()
	temp := "0.25"
	backend := &fakeInferenceBackend{
		resp: &ollama.ChatCompletionResponse{
			Model: "llama3.2:latest",
			Message: struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}{
				Role:    "assistant",
				Content: "hello",
			},
			PromptEvalCount: 7,
			EvalCount:       5,
			Done:            true,
		},
	}

	out, err := inferWithBackend(context.Background(), backend, &apiv1.InferenceRequest{
		RequestId: "req-1",
		Model:     "llama3.2:latest",
		Messages: []*apiv1.ChatMessage{
			{Role: "user", Content: "hi"},
		},
		Params: map[string]string{
			"temperature": temp,
		},
	})
	if err != nil {
		t.Fatalf("inferWithBackend() error = %v", err)
	}
	if backend.lastReq == nil || backend.lastReq.Temperature == nil {
		t.Fatalf("expected backend temperature to be set, got %#v", backend.lastReq)
	}
	if got := *backend.lastReq.Temperature; got != 0.25 {
		t.Fatalf("temperature=%v want 0.25", got)
	}
	if out.GetRequestId() != "req-1" || !out.GetOk() || out.GetContent() != "hello" || out.GetTokensUsed() != 12 {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestInferStreamWithBackend(t *testing.T) {
	t.Parallel()
	temp := "0.4"
	backend := &fakeInferenceBackend{
		stream: io.NopCloser(strings.NewReader("{\"message\":{\"content\":\"x\"},\"done\":false}\n")),
	}
	rc, err := inferStreamWithBackend(context.Background(), backend, &apiv1.InferenceRequest{
		RequestId: "req-stream",
		Model:     "llama3.2:latest",
		Messages: []*apiv1.ChatMessage{
			{Role: "user", Content: "hi"},
		},
		Params: map[string]string{"temperature": temp},
	})
	if err != nil {
		t.Fatalf("inferStreamWithBackend() error = %v", err)
	}
	defer rc.Close()
	if backend.lastReq == nil || backend.lastReq.Temperature == nil {
		t.Fatalf("expected backend temperature to be set, got %#v", backend.lastReq)
	}
	if got := *backend.lastReq.Temperature; got != 0.4 {
		t.Fatalf("temperature=%v want 0.4", got)
	}
}
