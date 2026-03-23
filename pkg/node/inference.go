package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/mrostamii/ai-peer/pkg/apiv1"
	"github.com/mrostamii/ai-peer/pkg/backend/ollama"
)

const InferenceProtocolID = protocol.ID("/ai-peer/v0.1/inference/1.0.0")
const InferenceStreamProtocolID = protocol.ID("/ai-peer/v0.1/inference-stream/1.0.0")

type inferenceBackend interface {
	ChatCompletion(context.Context, *ollama.ChatCompletionRequest) (*ollama.ChatCompletionResponse, error)
}

type streamInferenceBackend interface {
	StreamChatCompletion(context.Context, *ollama.ChatCompletionRequest) (io.ReadCloser, error)
}

func (r *Runtime) registerInferenceHandler(backend inferenceBackend) {
	r.host.SetStreamHandler(InferenceProtocolID, func(s network.Stream) {
		defer s.Close()
		var req apiv1.InferenceRequest
		if err := json.NewDecoder(io.LimitReader(s, 4<<20)).Decode(&req); err != nil {
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceResponse{
				Ok:           false,
				ErrorMessage: "decode inference request: " + err.Error(),
			})
			return
		}
		started := time.Now()
		resp, err := inferWithBackend(context.Background(), backend, &req)
		if err != nil {
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceResponse{
				RequestId:    req.GetRequestId(),
				Ok:           false,
				ErrorMessage: err.Error(),
			})
			return
		}
		resp.LatencyMs = time.Since(started).Milliseconds()
		if err := json.NewEncoder(s).Encode(resp); err != nil {
			log.Printf("inference stream encode warning: %v", err)
		}
	})
}

func (r *Runtime) registerInferenceStreamHandler(backend streamInferenceBackend) {
	r.host.SetStreamHandler(InferenceStreamProtocolID, func(s network.Stream) {
		defer s.Close()
		var req apiv1.InferenceRequest
		if err := json.NewDecoder(io.LimitReader(s, 4<<20)).Decode(&req); err != nil {
			_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
				RequestId:    req.GetRequestId(),
				Done:         true,
				Ok:           false,
				ErrorMessage: "decode inference request: " + err.Error(),
			})
			return
		}
		rc, err := inferStreamWithBackend(context.Background(), backend, &req)
		if err != nil {
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
		for {
			var chunk struct {
				Model   string `json:"model"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Done bool `json:"done"`
			}
			if err := dec.Decode(&chunk); err != nil {
				if err == io.EOF {
					_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
						RequestId: req.GetRequestId(),
						Done:      true,
						Ok:        true,
					})
					return
				}
				_ = json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
					RequestId:    req.GetRequestId(),
					Done:         true,
					Ok:           false,
					ErrorMessage: "decode backend stream: " + err.Error(),
				})
				return
			}
			if err := json.NewEncoder(s).Encode(&apiv1.InferenceStreamChunk{
				RequestId: req.GetRequestId(),
				Model:     chunk.Model,
				Content:   chunk.Message.Content,
				Done:      chunk.Done,
				Ok:        true,
			}); err != nil {
				return
			}
			if chunk.Done {
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
