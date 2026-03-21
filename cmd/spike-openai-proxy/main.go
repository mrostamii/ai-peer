// Spike-openai-proxy is a Phase 0.2 exercise: a tiny OpenAI-compatible HTTP surface
// that proxies chat completions and model listing to Ollama (/v1/chat/completions,
// /v1/models). Use curl or any OpenAI client pointed at this listener.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	ollama := flag.String("ollama", "http://127.0.0.1:11434", "Ollama base URL")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		handleModels(w, r, *ollama)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		handleChatCompletions(w, r, *ollama)
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("OpenAI spike proxy listening on http://%s → Ollama %s", *listen, *ollama)
	log.Fatal(srv.ListenAndServe())
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(t0))
	})
}

func handleModels(w http.ResponseWriter, r *http.Request, ollamaBase string) {
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(ollamaBase, "/")+"/api/tags", nil)
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
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, "ollama tags: "+err.Error()))
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

func handleChatCompletions(w http.ResponseWriter, r *http.Request, ollamaBase string) {
	ctx := r.Context()
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
		_ = writeJSON(w, http.StatusNotImplemented, openAIError(http.StatusNotImplemented, "spike proxy: set stream=false; streaming left for a follow-up"))
		return
	}

	body := toOllamaChatBody(&oreq)
	raw, err := json.Marshal(body)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ollamaBase, "/")+"/api/chat", bytes.NewReader(raw))
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
	obody, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	if resp.StatusCode != http.StatusOK {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, string(obody)))
		return
	}
	var ochat ollamaChatResponse
	if err := json.Unmarshal(obody, &ochat); err != nil {
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, "ollama chat decode: "+err.Error()))
		return
	}
	out := openAIChatCompletionFromOllama(&ochat, oreq.Model)
	_ = writeJSON(w, http.StatusOK, out)
}
