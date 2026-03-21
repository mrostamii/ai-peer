package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// openAIChatRequest is a minimal subset of POST /v1/chat/completions.
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

type ollamaChatResponse struct {
	Model           string `json:"model"`
	Message         struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool `json:"done"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
}

func openAIChatCompletionFromOllama(ollama *ollamaChatResponse, requestedModel string) map[string]any {
	model := requestedModel
	if model == "" {
		model = ollama.Model
	}
	now := time.Now().Unix()
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	return map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": now,
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
