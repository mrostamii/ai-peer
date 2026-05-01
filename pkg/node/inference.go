package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/mrostamii/tooti/pkg/apiv1"
	"github.com/mrostamii/tooti/pkg/backend/ollama"
)

const InferenceProtocolID = protocol.ID("/tooti/v0.1/inference/1.0.0")
const InferenceStreamProtocolID = protocol.ID("/tooti/v0.1/inference-stream/1.0.0")

type inferenceBackend interface {
	ChatCompletion(context.Context, *ollama.ChatCompletionRequest) (*ollama.ChatCompletionResponse, error)
}

type streamInferenceBackend interface {
	StreamChatCompletion(context.Context, *ollama.ChatCompletionRequest) (io.ReadCloser, error)
}

func (r *Runtime) registerInferenceHandler(backend inferenceBackend) {
	r.host.SetStreamHandler(InferenceProtocolID, func(s network.Stream) {
		defer s.Close()
		inferStarted := r.markInferenceStarted()
		defer r.markInferenceFinished()
		handlerStarted := time.Now()
		remotePeer := s.Conn().RemotePeer().String()
		reqID := ""
		model := ""
		tokensUsed := int64(0)
		success := false
		failure := ""
		defer func() {
			logInferenceEvent(map[string]any{
				"event":       "inference_server_complete",
				"stream":      false,
				"remote_peer": remotePeer,
				"request_id":  reqID,
				"model":       model,
				"tokens_used": tokensUsed,
				"ok":          success,
				"error":       failure,
				"latency_ms":  time.Since(handlerStarted).Milliseconds(),
			})
		}()
		var req apiv1.InferenceRequest
		if err := json.NewDecoder(io.LimitReader(s, 4<<20)).Decode(&req); err != nil {
			failure = "decode inference request: " + err.Error()
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceResponse{
				Ok:           false,
				ErrorMessage: "decode inference request: " + err.Error(),
			})
			return
		}
		reqID = req.GetRequestId()
		model = req.GetModel()
		remoteID := s.Conn().RemotePeer()
		if !r.isAllowedInferencePeer(remoteID) {
			failure = "unauthorized inference peer"
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceResponse{
				RequestId:    req.GetRequestId(),
				Ok:           false,
				ErrorMessage: "unauthorized inference peer",
			})
			return
		}
		started := time.Now()
		resp, err := inferWithBackend(context.Background(), backend, &req)
		if err != nil {
			failure = err.Error()
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceResponse{
				RequestId:    req.GetRequestId(),
				Ok:           false,
				ErrorMessage: err.Error(),
			})
			return
		}
		total := time.Since(inferStarted)
		// Unary backend responses do not expose TTFT, so use total duration as a proxy.
		r.recordInferenceSample(total, total, resp.GetTokensUsed())
		tokensUsed = resp.GetTokensUsed()
		resp.LatencyMs = time.Since(started).Milliseconds()
		success = true
		if err := json.NewEncoder(s).Encode(resp); err != nil {
			log.Printf("inference stream encode warning: %v", err)
		}
	})
}

func (r *Runtime) registerInferenceStreamHandler(backend streamInferenceBackend) {
	r.host.SetStreamHandler(InferenceStreamProtocolID, func(s network.Stream) {
		defer s.Close()
		_ = r.markInferenceStarted()
		streamStarted := time.Now()
		remotePeer := s.Conn().RemotePeer().String()
		reqID := ""
		model := ""
		tokensUsed := int64(0)
		success := false
		failure := ""
		var sampleTotal time.Duration
		var sampleTTFT time.Duration
		haveSample := false
		defer func() {
			if haveSample {
				r.recordInferenceSample(sampleTotal, sampleTTFT, 0)
			}
			r.markInferenceFinished()
			logInferenceEvent(map[string]any{
				"event":       "inference_server_complete",
				"stream":      true,
				"remote_peer": remotePeer,
				"request_id":  reqID,
				"model":       model,
				"tokens_used": tokensUsed,
				"ok":          success,
				"error":       failure,
				"latency_ms":  time.Since(streamStarted).Milliseconds(),
				"ttft_ms":     sampleTTFT.Milliseconds(),
			})
		}()
		var req apiv1.InferenceRequest
		if err := json.NewDecoder(io.LimitReader(s, 4<<20)).Decode(&req); err != nil {
			failure = "decode inference request: " + err.Error()
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
				RequestId:    req.GetRequestId(),
				Done:         true,
				Ok:           false,
				ErrorMessage: "decode inference request: " + err.Error(),
			})
			return
		}
		reqID = req.GetRequestId()
		model = req.GetModel()
		remoteID := s.Conn().RemotePeer()
		if !r.isAllowedInferencePeer(remoteID) {
			failure = "unauthorized inference peer"
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
				RequestId:    req.GetRequestId(),
				Done:         true,
				Ok:           false,
				ErrorMessage: "unauthorized inference peer",
			})
			return
		}
		rc, err := inferStreamWithBackend(context.Background(), backend, &req)
		if err != nil {
			failure = err.Error()
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
				RequestId:    req.GetRequestId(),
				Done:         true,
				Ok:           false,
				ErrorMessage: err.Error(),
			})
			return
		}
		defer rc.Close()

		dec := json.NewDecoder(bufio.NewReader(rc))
		gotFirst := false
		for {
			var chunk struct {
				Model   string `json:"model"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				PromptEvalCount int  `json:"prompt_eval_count"`
				EvalCount       int  `json:"eval_count"`
				Done            bool `json:"done"`
			}
			if err := dec.Decode(&chunk); err != nil {
				if err == io.EOF {
					if gotFirst {
						sampleTotal = time.Since(streamStarted)
						if sampleTTFT <= 0 {
							sampleTTFT = sampleTotal
						}
						haveSample = true
						success = true
					}
					_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
						RequestId: req.GetRequestId(),
						Done:      true,
						Ok:        true,
					})
					return
				}
				failure = "decode backend stream: " + err.Error()
				_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
					RequestId:    req.GetRequestId(),
					Done:         true,
					Ok:           false,
					ErrorMessage: "decode backend stream: " + err.Error(),
				})
				return
			}
			if !gotFirst {
				gotFirst = true
				sampleTTFT = time.Since(streamStarted)
			}
			if err := json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
				RequestId:  req.GetRequestId(),
				Model:      chunk.Model,
				Content:    chunk.Message.Content,
				TokensUsed: int64(chunk.PromptEvalCount + chunk.EvalCount),
				Done:       chunk.Done,
				Ok:         true,
			}); err != nil {
				failure = "encode response chunk: " + err.Error()
				return
			}
			if chunk.Done {
				tokensUsed = int64(chunk.PromptEvalCount + chunk.EvalCount)
				sampleTotal = time.Since(streamStarted)
				if sampleTTFT <= 0 {
					sampleTTFT = sampleTotal
				}
				haveSample = true
				success = true
				return
			}
		}
	})
}

func buildOllamaRequest(req *apiv1.InferenceRequest) (*ollama.ChatCompletionRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("inference request is nil")
	}
	if req.GetModel() == "" {
		return nil, fmt.Errorf("inference request missing model")
	}
	if len(req.GetMessages()) == 0 {
		return nil, fmt.Errorf("inference request missing messages")
	}
	outReq := &ollama.ChatCompletionRequest{
		Model:    req.GetModel(),
		Messages: make([]ollama.ChatMessage, 0, len(req.GetMessages())),
	}
	for _, m := range req.GetMessages() {
		outReq.Messages = append(outReq.Messages, ollama.ChatMessage{
			Role:    m.GetRole(),
			Content: m.GetContent(),
		})
	}
	if tRaw, ok := req.GetParams()["temperature"]; ok && tRaw != "" {
		t, err := strconv.ParseFloat(tRaw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid temperature %q: %w", tRaw, err)
		}
		outReq.Temperature = &t
	}
	return outReq, nil
}

func inferWithBackend(ctx context.Context, backend inferenceBackend, req *apiv1.InferenceRequest) (*apiv1.InferenceResponse, error) {
	outReq, err := buildOllamaRequest(req)
	if err != nil {
		return nil, err
	}
	outResp, err := backend.ChatCompletion(ctx, outReq)
	if err != nil {
		return nil, err
	}
	return &apiv1.InferenceResponse{
		RequestId:  req.GetRequestId(),
		Content:    outResp.Message.Content,
		TokensUsed: int64(outResp.PromptEvalCount + outResp.EvalCount),
		Ok:         true,
	}, nil
}

func inferStreamWithBackend(ctx context.Context, backend streamInferenceBackend, req *apiv1.InferenceRequest) (io.ReadCloser, error) {
	outReq, err := buildOllamaRequest(req)
	if err != nil {
		return nil, err
	}
	return backend.StreamChatCompletion(ctx, outReq)
}

func (r *Runtime) ConnectPeer(ctx context.Context, info peer.AddrInfo) error {
	return r.host.Connect(ctx, info)
}

func (r *Runtime) InferRemote(ctx context.Context, target peer.ID, req *apiv1.InferenceRequest) (*apiv1.InferenceResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("inference request is nil")
	}
	s, err := r.host.NewStream(ctx, target, InferenceProtocolID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := json.NewEncoder(s).Encode(req); err != nil {
		return nil, err
	}
	var resp apiv1.InferenceResponse
	if err := json.NewDecoder(io.LimitReader(s, 4<<20)).Decode(&resp); err != nil {
		return nil, err
	}
	if !resp.GetOk() {
		if msg := resp.GetErrorMessage(); msg != "" {
			if payErr, ok := decodePaymentRequiredEnvelope(msg); ok {
				return nil, payErr
			}
			return nil, fmt.Errorf("remote inference failed: %s", msg)
		}
		return nil, fmt.Errorf("remote inference failed")
	}
	return &resp, nil
}

func (r *Runtime) InferRemoteStream(ctx context.Context, target peer.ID, req *apiv1.InferenceRequest) (io.ReadCloser, error) {
	if req == nil {
		return nil, fmt.Errorf("inference request is nil")
	}
	s, err := r.host.NewStream(ctx, target, InferenceStreamProtocolID)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(s).Encode(req); err != nil {
		_ = s.Close()
		return nil, err
	}
	_ = s.CloseWrite()
	return s, nil
}

func logInferenceEvent(fields map[string]any) {
	line := formatOrderedInferenceLogLine(orderInferenceLogFields(sanitizeInferenceLogFields(fields)))
	if strings.TrimSpace(line) == "" {
		return
	}
	log.Print(line)
}

func orderInferenceLogFields(fields map[string]any) [][2]any {
	if len(fields) == 0 {
		return nil
	}
	priority := []string{
		"event",
		"request_id",
		"model",
		"remote_peer",
		"stream",
		"ok",
		"latency_ms",
		"ttft_ms",
		"tokens_used",
		"error",
	}
	out := make([][2]any, 0, len(fields))
	used := make(map[string]struct{}, len(fields))
	for _, key := range priority {
		v, ok := fields[key]
		if !ok || shouldOmitInferenceLogField(key, v) {
			continue
		}
		out = append(out, [2]any{key, v})
		used[key] = struct{}{}
	}
	extras := make([]string, 0, len(fields))
	for key := range fields {
		if _, ok := used[key]; ok {
			continue
		}
		if shouldOmitInferenceLogField(key, fields[key]) {
			continue
		}
		extras = append(extras, key)
	}
	sort.Strings(extras)
	for _, key := range extras {
		out = append(out, [2]any{key, fields[key]})
	}
	return out
}

func formatOrderedInferenceLogLine(fields [][2]any) string {
	if len(fields) == 0 {
		return ""
	}
	eventName := "inference_event"
	start := 0
	if k, ok := fields[0][0].(string); ok && k == "event" {
		if v, ok := fields[0][1].(string); ok && strings.TrimSpace(v) != "" {
			eventName = v
		}
		start = 1
	}

	var b strings.Builder
	b.WriteString(eventName)
	for i := start; i < len(fields); i++ {
		key, ok := fields[i][0].(string)
		if !ok || key == "event" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(formatInferenceLogValue(fields[i][1]))
	}
	return b.String()
}

func formatInferenceLogValue(v any) string {
	switch t := v.(type) {
	case string:
		if strings.ContainsAny(t, " \t\n\"") {
			return strconv.Quote(t)
		}
		return t
	case []string:
		return "[" + strings.Join(t, ",") + "]"
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(raw)
	}
}

func shouldOmitInferenceLogField(key string, v any) bool {
	if key == "error" {
		s, ok := v.(string)
		return ok && strings.TrimSpace(s) == ""
	}
	return false
}

func sanitizeInferenceLogFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		lk := strings.ToLower(strings.TrimSpace(k))
		switch lk {
		case "content", "message", "messages", "prompt", "prompt_text", "input":
			out[k] = "[redacted]"
			continue
		case "error":
			if s, ok := v.(string); ok {
				out[k] = sanitizeInferenceLogError(s)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func sanitizeInferenceLogError(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, `"messages"`) ||
		strings.Contains(lower, `"content"`) ||
		strings.Contains(lower, `"prompt"`) {
		return "redacted_potential_prompt_data"
	}
	if len(msg) > 256 {
		return msg[:256] + "...(truncated)"
	}
	return msg
}
