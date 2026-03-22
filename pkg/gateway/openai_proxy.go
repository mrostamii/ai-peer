package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIProxy struct {
	listenAddr string
	ollamaBase string
}

func NewOpenAIProxy(listenAddr, ollamaBase string) *OpenAIProxy {
	return &OpenAIProxy{listenAddr: listenAddr, ollamaBase: strings.TrimRight(ollamaBase, "/")}
}

func (p *OpenAIProxy) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /v1/models", p.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", p.handleChatCompletions)

	srv := &http.Server{
		Addr:              p.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (p *OpenAIProxy) handleModels(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, p.ollamaBase+"/api/tags", nil)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	if resp.StatusCode != http.StatusOK {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, string(body)))
		return
	}

	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &tags); err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, "ollama tags decode: "+err.Error()))
		return
	}

	data := make([]map[string]any, 0, len(tags.Models))
	for _, m := range tags.Models {
		id := m.Name
		if id == "" {
			id = m.Model
		}
		data = append(data, map[string]any{
			"id":       id,
			"object":   "model",
			"owned_by": "ollama",
		})
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (p *OpenAIProxy) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		_ = writeJSON(w, http.StatusUnsupportedMediaType, openAIError(http.StatusUnsupportedMediaType, "expected application/json body"))
		return
	}

	var oreq openAIChatRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&oreq); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	if oreq.Model == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "missing model"))
		return
	}
	if len(oreq.Messages) == 0 {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "missing messages"))
		return
	}
	if oreq.Stream {
		_ = writeJSON(w, http.StatusNotImplemented, openAIError(http.StatusNotImplemented, "gateway v0.1: set stream=false; streaming not implemented yet"))
		return
	}

	body := toOllamaChatBody(&oreq)
	raw, err := json.Marshal(body)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.ollamaBase+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	if resp.StatusCode != http.StatusOK {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, string(respBody)))
		return
	}

	var ochat ollamaChatResponse
	if err := json.Unmarshal(respBody, &ochat); err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, "ollama chat decode: "+err.Error()))
		return
	}

	_ = writeJSON(w, http.StatusOK, openAIChatCompletionFromOllama(&ochat, oreq.Model))
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Stream      bool                `json:"stream"`
	Temperature *float64            `json:"temperature,omitempty"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

func toOllamaChatBody(req *openAIChatRequest) map[string]any {
	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"stream":   req.Stream,
	}
	if req.Temperature != nil {
		body["options"] = map[string]any{"temperature": *req.Temperature}
	}
	return body
}

func openAIChatCompletionFromOllama(ollama *ollamaChatResponse, requestedModel string) map[string]any {
	model := requestedModel
	if model == "" {
		model = ollama.Model
	}
	return map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": ollama.Message.Content},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     ollama.PromptEvalCount,
			"completion_tokens": ollama.EvalCount,
			"total_tokens":      ollama.PromptEvalCount + ollama.EvalCount,
		},
	}
}

func openAIError(status int, message string) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    fmt.Sprintf("http_%d", status),
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}
