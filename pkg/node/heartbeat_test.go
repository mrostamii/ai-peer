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
	msg, err := buildHealthUpdate("peer-1", models, started, now)
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
	if out.Load != 0 || out.LatencyMs != 0 {
		t.Fatalf("expected default load/latency to be 0, got load=%f latency=%d", out.Load, out.LatencyMs)
	}
	if len(out.Models) != 2 || out.Models[0] != "qwen2.5:3b" || out.Models[1] != "llama3.2:latest" {
		t.Fatalf("unexpected models: %v", out.Models)
	}
}

func TestBuildHealthUpdateNilModels(t *testing.T) {
	t.Parallel()
	now := time.Unix(1710000000, 0)
	started := now.Add(-10 * time.Second)
	msg, err := buildHealthUpdate("peer-2", nil, started, now)
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
	if err := publishHealthUpdate(context.Background(), pub, "peer-1", []string{"mistral:7b"}, started, now); err != nil {
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
}
