package node

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestLogInferenceEventRedactsPromptFields(t *testing.T) {
	const promptSecret = "NODE_SECRET_PROMPT_456"

	var logs bytes.Buffer
	prevWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prevWriter)

	logInferenceEvent(map[string]any{
		"event":   "inference_server_complete",
		"content": promptSecret,
		"messages": []map[string]string{
			{"role": "user", "content": promptSecret},
		},
		"error": `backend failed with body {"messages":[{"content":"` + promptSecret + `"}]}`,
	})

	out := logs.String()
	if strings.Contains(out, promptSecret) {
		t.Fatalf("prompt text leaked in node logs: %s", out)
	}
}

func TestLogInferenceEventOrdersFieldsAndOmitsEmptyError(t *testing.T) {
	var logs bytes.Buffer
	prevWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prevWriter)

	logInferenceEvent(map[string]any{
		"remote_peer": "12D3KooX",
		"latency_ms":  1234,
		"tokens_used": 42,
		"event":       "inference_server_complete",
		"request_id":  "gw-1",
		"model":       "qwen2.5-coder:7b",
		"stream":      true,
		"ok":          true,
		"error":       "",
		"ttft_ms":     321,
	})

	out := logs.String()
	if strings.Contains(out, `"error":""`) {
		t.Fatalf("expected empty error field to be omitted, got: %s", out)
	}

	eventIdx := strings.Index(out, `"event":"inference_server_complete"`)
	reqIdx := strings.Index(out, `"request_id":"gw-1"`)
	modelIdx := strings.Index(out, `"model":"qwen2.5-coder:7b"`)
	peerIdx := strings.Index(out, `"remote_peer":"12D3KooX"`)
	if eventIdx < 0 || reqIdx < 0 || modelIdx < 0 || peerIdx < 0 {
		t.Fatalf("expected ordered keys in log output, got: %s", out)
	}
	if !(eventIdx < reqIdx && reqIdx < modelIdx && modelIdx < peerIdx) {
		t.Fatalf("unexpected key order, got: %s", out)
	}
}
