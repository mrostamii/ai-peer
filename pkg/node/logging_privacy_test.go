package node

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestLogInferenceEventRedactsPromptFields(t *testing.T) {
	t.Parallel()
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
