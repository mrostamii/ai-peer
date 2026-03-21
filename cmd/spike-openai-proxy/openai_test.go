package main

import (
	"encoding/json"
	"testing"
)

func TestToOllamaChatBody(t *testing.T) {
	temp := 0.2
	req := &openAIChatRequest{
		Model: "m",
		Messages: []openAIChatMessage{
			{Role: "system", Content: "s"},
			{Role: "user", Content: "u"},
		},
		Stream:      false,
		Temperature: &temp,
	}
	body := toOllamaChatBody(req)
	if body["model"] != "m" || body["stream"] != false {
		t.Fatalf("unexpected body: %#v", body)
	}
	opts, ok := body["options"].(map[string]any)
	if !ok || opts["temperature"] != 0.2 {
		t.Fatalf("expected options.temperature, got %#v", body)
	}
}

func TestOpenAIChatCompletionFromOllama(t *testing.T) {
	ollama := &ollamaChatResponse{}
	ollama.Model = "llama"
	ollama.Message.Role = "assistant"
	ollama.Message.Content = "hi"
	ollama.PromptEvalCount = 3
	ollama.EvalCount = 2
	out := openAIChatCompletionFromOllama(ollama, "requested")
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var dec struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Object != "chat.completion" || dec.Model != "requested" {
		t.Fatalf("%+v", dec)
	}
	if len(dec.Choices) != 1 || dec.Choices[0].Message.Content != "hi" {
		t.Fatalf("%+v", dec)
	}
}
