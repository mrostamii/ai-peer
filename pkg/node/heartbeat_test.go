package node

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type mockPublisher struct {
	payloads [][]byte
}

func (m *mockPublisher) Publish(_ context.Context, data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	m.payloads = append(m.payloads, cp)
	return nil
}

func TestBuildHealthUpdate(t *testing.T) {
	t.Parallel()
	now := time.Unix(1710000000, 0)
	started := now.Add(-75 * time.Second)
	models := []string{"qwen2.5:3b", "llama3.2:latest"}
	msg, err := buildHealthUpdate("peer-1", models, map[string]ModelPricingHint{
		"qwen2.5:3b": {PricePer1KAtomic: 10000, MinAmountAtomic: 500},
	}, started, now, 0.75, 42, 1800, 21.5)
	if err != nil {
		t.Fatalf("buildHealthUpdate() error = %v", err)
	}

	var out HealthUpdate
	if err := json.Unmarshal(msg, &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if out.NodeID != "peer-1" {
		t.Fatalf("unexpected node id: %q", out.NodeID)
	}
	if out.UptimeSec != 75 {
		t.Fatalf("unexpected uptime sec: %d", out.UptimeSec)
	}
	if out.Load != 0.75 || out.LatencyMs != 42 {
		t.Fatalf("expected load=0.75 latency=42, got load=%f latency=%d", out.Load, out.LatencyMs)
	}
	if out.TTFTMs != 1800 || out.DecodeTPS != 21.5 {
		t.Fatalf("expected ttft=1800 decode_tps=21.5, got ttft=%d decode_tps=%f", out.TTFTMs, out.DecodeTPS)
	}
	if len(out.Models) != 2 || out.Models[0] != "qwen2.5:3b" || out.Models[1] != "llama3.2:latest" {
		t.Fatalf("unexpected models: %v", out.Models)
	}
	if out.ModelPricing["qwen2.5:3b"].PricePer1KAtomic != 10000 {
		t.Fatalf("expected model pricing in heartbeat, got %+v", out.ModelPricing)
	}
}

func TestBuildHealthUpdateNilModels(t *testing.T) {
	t.Parallel()
	now := time.Unix(1710000000, 0)
	started := now.Add(-10 * time.Second)
	msg, err := buildHealthUpdate("peer-2", nil, nil, started, now, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("buildHealthUpdate() error = %v", err)
	}

	var out HealthUpdate
	if err := json.Unmarshal(msg, &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(out.Models) != 0 {
		t.Fatalf("expected no models, got %v", out.Models)
	}
}

func TestPublishHealthUpdate(t *testing.T) {
	t.Parallel()
	pub := &mockPublisher{}
	now := time.Unix(1710000000, 0)
	started := now.Add(-10 * time.Second)
	if err := publishHealthUpdate(context.Background(), pub, "peer-1", []string{"mistral:7b"}, nil, started, now, 0.5, 18, 700, 33.3); err != nil {
		t.Fatalf("publishHealthUpdate() error = %v", err)
	}
	if len(pub.payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(pub.payloads))
	}
	var out HealthUpdate
	if err := json.Unmarshal(pub.payloads[0], &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(out.Models) != 1 || out.Models[0] != "mistral:7b" {
		t.Fatalf("expected [mistral:7b], got %v", out.Models)
	}
	if out.Load != 0.5 || out.LatencyMs != 18 {
		t.Fatalf("expected load=0.5 latency=18, got load=%f latency=%d", out.Load, out.LatencyMs)
	}
	if out.TTFTMs != 700 || out.DecodeTPS != 33.3 {
		t.Fatalf("expected ttft=700 decode_tps=33.3, got ttft=%d decode_tps=%f", out.TTFTMs, out.DecodeTPS)
	}
}

func TestRuntimeHealthSnapshotReflectsInferenceStats(t *testing.T) {
	t.Parallel()
	var r Runtime
	started := r.markInferenceStarted()
	load, latency, ttft, decodeTPS := r.healthSnapshot()
	if load != 1 {
		t.Fatalf("expected in-flight load 1, got %f", load)
	}
	if latency != 0 {
		t.Fatalf("expected zero latency before completion, got %d", latency)
	}
	if ttft != 0 || decodeTPS != 0 {
		t.Fatalf("expected zero ttft/decode before completion, got ttft=%d decode_tps=%f", ttft, decodeTPS)
	}

	time.Sleep(2 * time.Millisecond)
	r.recordInferenceSample(time.Since(started), time.Since(started), 16)
	r.markInferenceFinished()
	load, latency, ttft, decodeTPS = r.healthSnapshot()
	if load != 0 {
		t.Fatalf("expected load 0 after completion, got %f", load)
	}
	if latency <= 0 {
		t.Fatalf("expected positive latency after completion, got %d", latency)
	}
	if ttft <= 0 || decodeTPS <= 0 {
		t.Fatalf("expected positive ttft/decode after completion, got ttft=%d decode_tps=%f", ttft, decodeTPS)
	}
}
