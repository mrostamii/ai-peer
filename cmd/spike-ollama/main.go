// Spike-ollama is a Phase 0.2 exercise: call Ollama's HTTP API from Go (/api/tags,
// /api/chat non-streaming and streaming) against a local or remote Ollama base URL.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:11434", "Ollama server base URL")
	model := flag.String("model", "llama3.2", "model name for /api/chat")
	prompt := flag.String("prompt", "Say hello in one short sentence.", "user message for chat")
	stream := flag.Bool("stream", false, "also run a streaming /api/chat after non-stream call")
	timeout := flag.Duration("timeout", 120*time.Second, "HTTP client timeout per request")
	flag.Parse()

	ctx := context.Background()
	client := &http.Client{Timeout: *timeout}

	if err := runTags(ctx, client, *base); err != nil {
		log.Fatalf("tags: %v", err)
	}
	if err := runChat(ctx, client, *base, *model, *prompt, false); err != nil {
		log.Fatalf("chat: %v", err)
	}
	if *stream {
		if err := runChatStream(ctx, client, *base, *model, *prompt); err != nil {
			log.Fatalf("chat stream: %v", err)
		}
	}
}

func runTags(ctx context.Context, client *http.Client, base string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/api/tags: %s: %s", resp.Status, truncate(body, 200))
	}

	var out tagsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("decode tags: %w (body %s)", err, truncate(body, 300))
	}
	log.Printf("/api/tags: %d model(s)", len(out.Models))
	for _, m := range out.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		log.Printf("  - %s", name)
	}
	return nil
}

type tagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

func runChat(ctx context.Context, client *http.Client, base, model, prompt string, stream bool) error {
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   stream,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/api/chat: %s: %s", resp.Status, truncate(body, 400))
	}

	var chat chatResponse
	if err := json.Unmarshal(body, &chat); err != nil {
		return fmt.Errorf("decode chat: %w", err)
	}
	log.Printf("/api/chat (stream=%v) done in %s", stream, time.Since(t0))
	log.Printf("  assistant: %s", strings.TrimSpace(chat.Message.Content))
	if chat.EvalCount != 0 || chat.PromptEvalCount != 0 {
		log.Printf("  prompt_eval_count=%d eval_count=%d", chat.PromptEvalCount, chat.EvalCount)
	}
	return nil
}

type chatResponse struct {
	Model           string `json:"model"`
	Message         msg    `json:"message"`
	Done            bool   `json:"done"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

type msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func runChatStream(ctx context.Context, client *http.Client, base, model, prompt string) error {
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   true,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// Streaming responses can run longer than a single-response timeout.
	streamClient := *client
	streamClient.Timeout = 0

	t0 := time.Now()
	resp, err := streamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("/api/chat stream: %s: %s", resp.Status, truncate(b, 400))
	}

	var full strings.Builder
	sc := bufio.NewScanner(resp.Body)
	// Default token size may be too small for large JSON lines.
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	lineN := 0
	for sc.Scan() {
		line := sc.Bytes()
		lineN++
		var ev streamChatEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("stream line %d: %w: %s", lineN, err, truncate(line, 200))
		}
		full.WriteString(ev.Message.Content)
		if ev.Done {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	log.Printf("/api/chat stream: %d line(s) in %s", lineN, time.Since(t0))
	log.Printf("  assembled assistant: %s", strings.TrimSpace(full.String()))
	return nil
}

type streamChatEvent struct {
	Message msg `json:"message"`
	Done    bool `json:"done"`
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func init() {
	log.SetOutput(os.Stdout)
	log.SetFlags(0)
}
