package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mrostamii/ai-peer/pkg/registry"
)

type OpenAIProxy struct {
	listenAddr string
	ollamaBase string
	reg        *registry.Registry
	remoteChat RemoteChatFunc
}

type RemoteChatMessage struct {
	Role    string
	Content string
}

type RemoteChatRequest struct {
	Model       string
	Messages    []RemoteChatMessage
	Temperature *float64
}

type RemoteChatResponse struct {
	Model            string
	Content          string
	CompletionTokens int64
}

type RemoteChatFunc func(context.Context, string, *RemoteChatRequest) (*RemoteChatResponse, error)

// NewOpenAIProxy serves OpenAI-shaped HTTP. If reg is non-nil, GET /v1/network/nodes
// returns peers learned from gossip health messages.
func NewOpenAIProxy(listenAddr, ollamaBase string, reg *registry.Registry) *OpenAIProxy {
	return &OpenAIProxy{
		listenAddr: listenAddr,
		ollamaBase: strings.TrimRight(ollamaBase, "/"),
		reg:        reg,
	}
}

func (p *OpenAIProxy) SetRemoteChatFunc(fn RemoteChatFunc) {
	p.remoteChat = fn
}

func (p *OpenAIProxy) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /v1/models", p.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", p.handleChatCompletions)
	if p.reg != nil {
		mux.HandleFunc("GET /v1/network/nodes", p.handleNetworkNodes)
	}

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

func (p *OpenAIProxy) handleNetworkNodes(w http.ResponseWriter, _ *http.Request) {
	if p.reg == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	nodes := p.reg.List()
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   nodes,
	})
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

	seen := map[string]struct{}{}
	for _, m := range tags.Models {
		id := m.Name
		if id == "" {
			id = m.Model
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
	}
	if p.reg != nil {
		for _, rec := range p.reg.List() {
			for _, model := range rec.Models {
				model = strings.TrimSpace(model)
				if model == "" {
					continue
				}
				seen[model] = struct{}{}
			}
		}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	data := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
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
		p.handleChatCompletionsStream(w, r, &oreq)
		return
	}
	if p.reg != nil && p.remoteChat != nil {
		nodes := p.reg.NodesForModel(oreq.Model)
		if len(nodes) > 0 {
			remoteReq := &RemoteChatRequest{
				Model:       oreq.Model,
				Messages:    make([]RemoteChatMessage, 0, len(oreq.Messages)),
				Temperature: oreq.Temperature,
			}
			for _, msg := range oreq.Messages {
				remoteReq.Messages = append(remoteReq.Messages, RemoteChatMessage{
					Role:    msg.Role,
					Content: msg.Content,
				})
			}
			for _, node := range nodes {
				resp, err := p.remoteChat(r.Context(), node.NodeID, remoteReq)
				if err != nil {
					continue
				}
				_ = writeJSON(w, http.StatusOK, openAIChatCompletionFromRemote(resp, oreq.Model))
				return
			}
		}
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

func (p *OpenAIProxy) handleChatCompletionsStream(w http.ResponseWriter, r *http.Request, oreq *openAIChatRequest) {
	body := toOllamaChatBody(oreq)
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
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, string(respBody)))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "streaming not supported by server writer"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	model := oreq.Model

	dec := json.NewDecoder(bufio.NewReader(resp.Body))
	firstChunk := true
	for {
		var chunk ollamaStreamResponse
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return
		}
		if model == "" && chunk.Model != "" {
			model = chunk.Model
		}
		delta := map[string]any{}
		if firstChunk {
			delta["role"] = "assistant"
		}
		if chunk.Message.Content != "" {
			delta["content"] = chunk.Message.Content
		}
		finishReason := any(nil)
		if chunk.Done {
			finishReason = "stop"
		}
		firstChunk = false

		event := map[string]any{
			"id":      chatID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         delta,
					"finish_reason": finishReason,
				},
			},
		}
		chunkRaw, err := json.Marshal(event)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", chunkRaw); err != nil {
			return
		}
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
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

type ollamaStreamResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
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

func openAIChatCompletionFromRemote(remote *RemoteChatResponse, requestedModel string) map[string]any {
	model := requestedModel
	if model == "" && remote != nil {
		model = remote.Model
	}
	content := ""
	tokens := int64(0)
	if remote != nil {
		content = remote.Content
		tokens = remote.CompletionTokens
	}
	return map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": content},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int64{
			"prompt_tokens":     0,
			"completion_tokens": tokens,
			"total_tokens":      tokens,
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
