package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mrostamii/tooti/pkg/apiv1"
	"github.com/mrostamii/tooti/pkg/registry"
	"github.com/mrostamii/tooti/pkg/x402spike"
)

type OpenAIProxy struct {
	listenAddr          string
	ollamaBase          string
	localBackendEnabled bool
	reg                 *registry.Registry
	remoteChat          RemoteChatFunc
	remoteStreamChat    RemoteStreamChatFunc
	peerLatency         PeerLatencyFunc
	firstTokenTimeout   time.Duration
	totalRequestTimeout time.Duration
	chatPaywall         *X402PaywallConfig
}

const maxRemoteRetries = 2

type RemoteChatMessage struct {
	Role    string
	Content string
}

type RemoteChatRequest struct {
	RequestID        string
	Model            string
	Messages         []RemoteChatMessage
	MaxTokens        *int
	Temperature      *float64
	PaymentSignature string
	ResourceURL      string
}

type RemoteChatResponse struct {
	Model            string
	Content          string
	CompletionTokens int64
}

type RemoteChatFunc func(context.Context, string, *RemoteChatRequest) (*RemoteChatResponse, error)
type RemoteStreamChatFunc func(context.Context, string, *RemoteChatRequest) (io.ReadCloser, error)
type PeerLatencyFunc func(context.Context, string) (time.Duration, error)

type RemotePaymentRequiredError struct {
	Message               string
	PaymentRequiredHeader string
	PaymentResponseHeader string
}

func (e *RemotePaymentRequiredError) Error() string {
	if e == nil {
		return "remote payment required"
	}
	if strings.TrimSpace(e.Message) == "" {
		return "remote payment required"
	}
	return e.Message
}

// NewOpenAIProxy serves OpenAI-shaped HTTP. If reg is non-nil, GET /v1/network/nodes
// returns peers learned from gossip health messages.
func NewOpenAIProxy(listenAddr, ollamaBase string, reg *registry.Registry) *OpenAIProxy {
	return &OpenAIProxy{
		listenAddr:          listenAddr,
		ollamaBase:          strings.TrimRight(ollamaBase, "/"),
		localBackendEnabled: true,
		reg:                 reg,
		firstTokenTimeout:   30 * time.Second,
		totalRequestTimeout: 120 * time.Second,
	}
}

func (p *OpenAIProxy) SetLocalBackendEnabled(enabled bool) {
	p.localBackendEnabled = enabled
}

func (p *OpenAIProxy) SetRemoteChatFunc(fn RemoteChatFunc) {
	p.remoteChat = fn
}

func (p *OpenAIProxy) SetRemoteStreamChatFunc(fn RemoteStreamChatFunc) {
	p.remoteStreamChat = fn
}

func (p *OpenAIProxy) SetPeerLatencyFunc(fn PeerLatencyFunc) {
	p.peerLatency = fn
}

func (p *OpenAIProxy) SetTimeouts(firstToken, total time.Duration) {
	if firstToken > 0 {
		p.firstTokenTimeout = firstToken
	}
	if total > 0 {
		p.totalRequestTimeout = total
	}
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
	seen := map[string]struct{}{}
	if p.localBackendEnabled {
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
	requestID := fmt.Sprintf("gw-%d", time.Now().UnixNano())
	w.Header().Set("X-Tooti-Request-ID", requestID)
	r.Header.Set("X-Tooti-Request-ID", requestID)
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
	if !p.enforceChatPayment(w, r, &oreq) {
		return
	}
	if oreq.Stream {
		p.handleChatCompletionsStream(w, r, &oreq, requestID)
		return
	}
	started := time.Now()
	selectedNode := ""
	tokensUsed := int64(0)
	success := false
	failure := ""
	defer func() {
		p.logRequest(map[string]any{
			"event":       "inference_request",
			"request_id":  requestID,
			"stream":      false,
			"model":       oreq.Model,
			"node_id":     selectedNode,
			"latency_ms":  time.Since(started).Milliseconds(),
			"tokens_used": tokensUsed,
			"ok":          success,
			"error":       failure,
		})
	}()

	reqCtx, cancel := context.WithTimeout(r.Context(), p.totalRequestTimeout)
	defer cancel()
	if p.reg != nil && p.remoteChat != nil {
		nodes := rankedNodesForModel(p.reg, oreq.Model)
		nodes = p.reorderNodesByPing(r.Context(), nodes)
		if len(nodes) > 0 {
			remoteReq := &RemoteChatRequest{
				RequestID:        requestID,
				Model:            oreq.Model,
				MaxTokens:        oreq.MaxTokens,
				Messages:         make([]RemoteChatMessage, 0, len(oreq.Messages)),
				Temperature:      oreq.Temperature,
				PaymentSignature: strings.TrimSpace(r.Header.Get("PAYMENT-SIGNATURE")),
				ResourceURL:      requestURL(r),
			}
			for _, msg := range oreq.Messages {
				remoteReq.Messages = append(remoteReq.Messages, RemoteChatMessage{
					Role:    msg.Role,
					Content: msg.Content,
				})
			}
			for i, node := range nodes {
				if i > maxRemoteRetries {
					break
				}
				selectedNode = node.NodeID
				resp, err := p.remoteChat(reqCtx, node.NodeID, remoteReq)
				if err != nil {
					var payErr *RemotePaymentRequiredError
					if errors.As(err, &payErr) {
						failure = payErr.Error()
						if p.writeRemotePaymentRequired(w, payErr) {
							return
						}
					}
					failure = err.Error()
					continue
				}
				tokensUsed = resp.CompletionTokens
				success = true
				_ = writeJSON(w, http.StatusOK, openAIChatCompletionFromRemote(resp, oreq.Model, requestID))
				return
			}
		}
	}
	selectedNode = "local"
	if !p.localBackendEnabled {
		failure = "no remote provider available for model"
		_ = writeJSON(w, http.StatusServiceUnavailable, openAIError(http.StatusServiceUnavailable, failure))
		return
	}

	body := toOllamaChatBody(&oreq)
	raw, err := json.Marshal(body)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.ollamaBase+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		failure = err.Error()
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		failure = err.Error()
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		failure = err.Error()
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	if resp.StatusCode != http.StatusOK {
		failure = string(respBody)
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, string(respBody)))
		return
	}

	var ochat ollamaChatResponse
	if err := json.Unmarshal(respBody, &ochat); err != nil {
		failure = err.Error()
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, "ollama chat decode: "+err.Error()))
		return
	}

	tokensUsed = int64(ochat.PromptEvalCount + ochat.EvalCount)
	success = true
	_ = writeJSON(w, http.StatusOK, openAIChatCompletionFromOllama(&ochat, oreq.Model, requestID))
}

func (p *OpenAIProxy) handleChatCompletionsStream(w http.ResponseWriter, r *http.Request, oreq *openAIChatRequest, requestID string) {
	started := time.Now()
	selectedNode := ""
	tokensUsed := int64(0)
	success := false
	failure := ""
	defer func() {
		p.logRequest(map[string]any{
			"event":       "inference_request",
			"request_id":  requestID,
			"stream":      true,
			"model":       oreq.Model,
			"node_id":     selectedNode,
			"latency_ms":  time.Since(started).Milliseconds(),
			"tokens_used": tokensUsed,
			"ok":          success,
			"error":       failure,
		})
	}()
	reqCtx, cancel := context.WithTimeout(r.Context(), p.totalRequestTimeout)
	defer cancel()
	if p.reg != nil && p.remoteStreamChat != nil {
		nodes := rankedNodesForModel(p.reg, oreq.Model)
		nodes = p.reorderNodesByPing(r.Context(), nodes)
		for i, node := range nodes {
			if i > maxRemoteRetries {
				break
			}
			selectedNode = node.NodeID
			attemptStarted := time.Now()
			rc, err := p.remoteStreamChat(reqCtx, node.NodeID, &RemoteChatRequest{
				RequestID:        requestID,
				Model:            oreq.Model,
				MaxTokens:        oreq.MaxTokens,
				Temperature:      oreq.Temperature,
				PaymentSignature: strings.TrimSpace(r.Header.Get("PAYMENT-SIGNATURE")),
				ResourceURL:      requestURL(r),
				Messages: func() []RemoteChatMessage {
					out := make([]RemoteChatMessage, 0, len(oreq.Messages))
					for _, m := range oreq.Messages {
						out = append(out, RemoteChatMessage{Role: m.Role, Content: m.Content})
					}
					return out
				}(),
			})
			if err != nil {
				var payErr *RemotePaymentRequiredError
				if errors.As(err, &payErr) {
					failure = payErr.Error()
					if p.writeRemotePaymentRequired(w, payErr) {
						return
					}
				}
				failure = err.Error()
				continue
			}
			defer rc.Close()

			chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
			created := time.Now().Unix()
			model := oreq.Model
			dec := json.NewDecoder(bufio.NewReader(rc))
			firstChunk := true
			var first apiv1.InferenceStreamChunk
			remainingFirst := p.firstTokenTimeout - time.Since(attemptStarted)
			if err := p.decodeWithTimeout(remainingFirst, func() error { return dec.Decode(&first) }); err != nil {
				failure = err.Error()
				continue
			}
			if !first.GetOk() {
				if payErr, ok := decodeRemotePaymentRequiredError(first.GetErrorMessage()); ok {
					failure = payErr.Error()
					if p.writeRemotePaymentRequired(w, payErr) {
						return
					}
				}
				failure = first.GetErrorMessage()
				continue
			}

			flusher, ok := w.(http.Flusher)
			if !ok {
				failure = "streaming not supported by server writer"
				_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "streaming not supported by server writer"))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			if model == "" && first.GetModel() != "" {
				model = first.GetModel()
			}
			delta := map[string]any{"role": "assistant"}
			if c := first.GetContent(); c != "" {
				delta["content"] = c
			}
			finishReason := any(nil)
			if first.GetDone() {
				finishReason = "stop"
			}
			firstChunk = false
			if err := p.writeSSEChunk(w, flusher, chatID, created, model, requestID, delta, finishReason); err != nil {
				failure = err.Error()
				return
			}
			if first.GetDone() {
				tokensUsed = first.GetTokensUsed()
				success = true
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			for {
				var chunk apiv1.InferenceStreamChunk
				if err := dec.Decode(&chunk); err != nil {
					if err == io.EOF {
						break
					}
					failure = err.Error()
					return
				}
				if !chunk.GetOk() {
					failure = chunk.GetErrorMessage()
					return
				}
				if model == "" && chunk.GetModel() != "" {
					model = chunk.GetModel()
				}
				delta := map[string]any{}
				if firstChunk {
					delta["role"] = "assistant"
				}
				if c := chunk.GetContent(); c != "" {
					delta["content"] = c
				}
				finishReason := any(nil)
				if chunk.GetDone() {
					tokensUsed = chunk.GetTokensUsed()
					finishReason = "stop"
				}
				firstChunk = false
				if err := p.writeSSEChunk(w, flusher, chatID, created, model, requestID, delta, finishReason); err != nil {
					failure = err.Error()
					return
				}
				if chunk.GetDone() {
					break
				}
			}
			success = true
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
	}
	selectedNode = "local"
	if !p.localBackendEnabled {
		failure = "no remote provider available for model"
		_ = writeJSON(w, http.StatusServiceUnavailable, openAIError(http.StatusServiceUnavailable, failure))
		return
	}

	body := toOllamaChatBody(oreq)
	raw, err := json.Marshal(body)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}

	streamStarted := time.Now()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.ollamaBase+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		failure = err.Error()
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		failure = err.Error()
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, err.Error()))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		failure = string(respBody)
		_ = writeJSON(w, http.StatusBadGateway, openAIError(http.StatusBadGateway, string(respBody)))
		return
	}

	dec := json.NewDecoder(bufio.NewReader(resp.Body))
	var first ollamaStreamResponse
	remainingFirst := p.firstTokenTimeout - time.Since(streamStarted)
	if err := p.decodeWithTimeout(remainingFirst, func() error { return dec.Decode(&first) }); err != nil {
		failure = err.Error()
		_ = writeJSON(w, http.StatusGatewayTimeout, openAIError(http.StatusGatewayTimeout, "first token timeout"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		failure = "streaming not supported by server writer"
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
	firstChunk := true
	if model == "" && first.Model != "" {
		model = first.Model
	}
	firstDelta := map[string]any{"role": "assistant"}
	if first.Message.Content != "" {
		firstDelta["content"] = first.Message.Content
	}
	firstFinish := any(nil)
	if first.Done {
		tokensUsed = int64(first.PromptEvalCount + first.EvalCount)
		firstFinish = "stop"
	}
	firstChunk = false
	if err := p.writeSSEChunk(w, flusher, chatID, created, model, requestID, firstDelta, firstFinish); err != nil {
		failure = err.Error()
		return
	}
	if first.Done {
		success = true
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	for {
		var chunk ollamaStreamResponse
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			failure = err.Error()
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
			tokensUsed = int64(chunk.PromptEvalCount + chunk.EvalCount)
			finishReason = "stop"
		}
		firstChunk = false
		if err := p.writeSSEChunk(w, flusher, chatID, created, model, requestID, delta, finishReason); err != nil {
			failure = err.Error()
			return
		}
	}
	success = true
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func decodeRemotePaymentRequiredError(msg string) (*RemotePaymentRequiredError, bool) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil, false
	}
	var env struct {
		Code            string `json:"code"`
		Message         string `json:"message"`
		PaymentRequired string `json:"payment_required"`
		PaymentResponse string `json:"payment_response"`
	}
	if err := json.Unmarshal([]byte(msg), &env); err != nil {
		return nil, false
	}
	if env.Code != "payment_required" || strings.TrimSpace(env.PaymentRequired) == "" {
		return nil, false
	}
	return &RemotePaymentRequiredError{
		Message:               env.Message,
		PaymentRequiredHeader: env.PaymentRequired,
		PaymentResponseHeader: env.PaymentResponse,
	}, true
}

func (p *OpenAIProxy) writeRemotePaymentRequired(w http.ResponseWriter, payErr *RemotePaymentRequiredError) bool {
	if payErr == nil || strings.TrimSpace(payErr.PaymentRequiredHeader) == "" {
		return false
	}
	var pr x402spike.PaymentRequired
	if err := x402spike.DecodeBase64JSON(payErr.PaymentRequiredHeader, &pr); err != nil {
		return false
	}
	var settle x402spike.SettlementResponse
	if strings.TrimSpace(payErr.PaymentResponseHeader) != "" {
		if err := x402spike.DecodeBase64JSON(payErr.PaymentResponseHeader, &settle); err != nil {
			settle = x402spike.SettlementResponse{}
		}
	}
	writePaymentRequired(w, pr, settle)
	return true
}

func (p *OpenAIProxy) reorderNodesByPing(ctx context.Context, nodes []registry.NodeRecord) []registry.NodeRecord {
	if p.peerLatency == nil || len(nodes) < 2 {
		return nodes
	}
	type candidate struct {
		rec      registry.NodeRecord
		pingMS   int64
		hasPing  bool
		fallback int64
	}
	out := make([]candidate, 0, len(nodes))
	for _, n := range nodes {
		c := candidate{
			rec:      n,
			pingMS:   n.LatencyMs,
			fallback: n.LatencyMs,
		}
		pingCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
		d, err := p.peerLatency(pingCtx, n.NodeID)
		cancel()
		if err == nil && d >= 0 {
			c.pingMS = d.Milliseconds()
			c.hasPing = true
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.pingMS != b.pingMS {
			return a.pingMS < b.pingMS
		}
		if a.hasPing != b.hasPing {
			return a.hasPing
		}
		if a.fallback != b.fallback {
			return a.fallback < b.fallback
		}
		return a.rec.NodeID < b.rec.NodeID
	})
	res := make([]registry.NodeRecord, 0, len(out))
	for _, c := range out {
		c.rec.LatencyMs = c.pingMS
		res = append(res, c.rec)
	}
	return res
}

func rankedNodesForModel(reg *registry.Registry, model string) []registry.NodeRecord {
	if reg == nil || model == "" {
		return nil
	}
	nodes := reg.NodesForModel(model)
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Load != b.Load {
			return a.Load < b.Load
		}
		aHasTTFT := a.TTFTMs > 0
		bHasTTFT := b.TTFTMs > 0
		if aHasTTFT != bHasTTFT {
			return aHasTTFT
		}
		if aHasTTFT && a.TTFTMs != b.TTFTMs {
			return a.TTFTMs < b.TTFTMs
		}
		aHasDecode := a.DecodeTPS > 0
		bHasDecode := b.DecodeTPS > 0
		if aHasDecode != bHasDecode {
			return aHasDecode
		}
		if aHasDecode && a.DecodeTPS != b.DecodeTPS {
			return a.DecodeTPS > b.DecodeTPS
		}
		if a.LatencyMs != b.LatencyMs {
			return a.LatencyMs < b.LatencyMs
		}
		if a.UptimeSec != b.UptimeSec {
			return a.UptimeSec > b.UptimeSec
		}
		if !a.LastSeen.Equal(b.LastSeen) {
			return a.LastSeen.After(b.LastSeen)
		}
		return a.NodeID < b.NodeID
	})
	return nodes
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Stream      bool                `json:"stream"`
	MaxTokens   *int                `json:"max_tokens,omitempty"`
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
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
	Done            bool `json:"done"`
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

func openAIChatCompletionFromOllama(ollama *ollamaChatResponse, requestedModel, requestID string) map[string]any {
	model := requestedModel
	if model == "" {
		model = ollama.Model
	}
	return map[string]any{
		"id":           fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":       "chat.completion",
		"created":      time.Now().Unix(),
		"model":        model,
		"x_request_id": requestID,
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

func openAIChatCompletionFromRemote(remote *RemoteChatResponse, requestedModel, requestID string) map[string]any {
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
		"id":           fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":       "chat.completion",
		"created":      time.Now().Unix(),
		"model":        model,
		"x_request_id": requestID,
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

func (p *OpenAIProxy) decodeWithTimeout(timeout time.Duration, decodeFn func() error) error {
	if timeout <= 0 {
		return fmt.Errorf("first token timeout")
	}
	if p.firstTokenTimeout <= 0 {
		return decodeFn()
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- decodeFn()
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(p.firstTokenTimeout):
		return fmt.Errorf("first token timeout")
	}
}

func (p *OpenAIProxy) writeSSEChunk(w io.Writer, flusher http.Flusher, id string, created int64, model string, requestID string, delta map[string]any, finishReason any) error {
	event := map[string]any{
		"id":           id,
		"object":       "chat.completion.chunk",
		"created":      created,
		"model":        model,
		"x_request_id": requestID,
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
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", chunkRaw); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (p *OpenAIProxy) logRequest(fields map[string]any) {
	raw, err := json.Marshal(sanitizeLogFields(fields))
	if err != nil {
		log.Printf("gateway log marshal error: %v", err)
		return
	}
	log.Print(string(raw))
}

func sanitizeLogFields(fields map[string]any) map[string]any {
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
				out[k] = sanitizeLogError(s)
				continue
			}
		}
		out[k] = sanitizeLogValue(v)
	}
	return out
}

func sanitizeLogValue(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		return sanitizeLogFields(vv)
	case []any:
		out := make([]any, 0, len(vv))
		for _, item := range vv {
			out = append(out, sanitizeLogValue(item))
		}
		return out
	case []map[string]any:
		out := make([]any, 0, len(vv))
		for _, item := range vv {
			out = append(out, sanitizeLogFields(item))
		}
		return out
	default:
		return v
	}
}

func sanitizeLogError(msg string) string {
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
