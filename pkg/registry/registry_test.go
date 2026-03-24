package registry

import (
	"fmt"
	"testing"
	"time"

	"github.com/mrostamii/ai-peer/pkg/apiv1"
)

func TestRegistryHealthAndPrune(t *testing.T) {
	start := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
	clock := start
	r := New(30*time.Second, WithClock(func() time.Time { return clock }))

	payload := fmt.Sprintf(`{"node_id":"peer-a","uptime_sec":1,"timestamp_ms":%d}`, start.UnixMilli())
	if err := r.ApplyHealthJSON([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if r.Len() != 1 {
		t.Fatalf("len=%d want 1", r.Len())
	}

	clock = start.Add(29 * time.Second)
	if r.PruneStale() != 0 {
		t.Fatal("unexpected prune")
	}

	clock = start.Add(91 * time.Second)
	if n := r.PruneStale(); n != 1 {
		t.Fatalf("prune=%d want 1", n)
	}
	if r.Len() != 0 {
		t.Fatalf("len=%d want 0", r.Len())
	}
}

func TestNodesForModel(t *testing.T) {
	now := time.Now()
	r := New(time.Minute, WithClock(func() time.Time { return now }))

	_ = r.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{
		NodeId:      "n1",
		Models:      []string{"llama", "mistral"},
		TimestampMs: now.UnixMilli(),
	})
	_ = r.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{
		NodeId:      "n2",
		Models:      []string{"phi"},
		TimestampMs: now.UnixMilli(),
	})

	nodes := r.NodesForModel("llama")
	if len(nodes) != 1 || nodes[0].NodeID != "n1" {
		t.Fatalf("llama providers: %+v", nodes)
	}
	nodes = r.NodesForModel("phi")
	if len(nodes) != 1 || nodes[0].NodeID != "n2" {
		t.Fatalf("phi providers: %+v", nodes)
	}
}

func TestApplyHealthJSONWithModels(t *testing.T) {
	now := time.Now()
	r := New(30*time.Second, WithClock(func() time.Time { return now }))

	payload := fmt.Sprintf(`{"node_id":"peer-q","uptime_sec":60,"timestamp_ms":%d,"models":["qwen2.5:3b","mistral:7b"]}`, now.UnixMilli())
	if err := r.ApplyHealthJSON([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	if len(list[0].Models) != 2 || list[0].Models[0] != "mistral:7b" || list[0].Models[1] != "qwen2.5:3b" {
		t.Fatalf("models=%v want [mistral:7b qwen2.5:3b]", list[0].Models)
	}

	nodes := r.NodesForModel("qwen2.5:3b")
	if len(nodes) != 1 || nodes[0].NodeID != "peer-q" {
		t.Fatalf("qwen providers: %+v", nodes)
	}
}

func TestApplyHealthJSONWithTTFTAndDecodeTPS(t *testing.T) {
	now := time.Now()
	r := New(30*time.Second, WithClock(func() time.Time { return now }))

	payload := fmt.Sprintf(`{"node_id":"peer-m","uptime_sec":12,"load":1.5,"latency_ms":2100,"ttft_ms":550,"decode_tps":42.25,"timestamp_ms":%d}`, now.UnixMilli())
	if err := r.ApplyHealthJSON([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	if list[0].TTFTMs != 550 {
		t.Fatalf("ttft_ms=%d want 550", list[0].TTFTMs)
	}
	if list[0].DecodeTPS != 42.25 {
		t.Fatalf("decode_tps=%f want 42.25", list[0].DecodeTPS)
	}
}

func TestApplyHealthJSONWithModelPricing(t *testing.T) {
	now := time.Now()
	r := New(30*time.Second, WithClock(func() time.Time { return now }))

	payload := fmt.Sprintf(`{"node_id":"peer-price","uptime_sec":12,"timestamp_ms":%d,"model_pricing":{"qwen2.5:3b":{"price_per_1k_atomic":10000,"min_amount_atomic":500}}}`, now.UnixMilli())
	if err := r.ApplyHealthJSON([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	mp := list[0].ModelPricing["qwen2.5:3b"]
	if mp.PricePer1KAtomic != 10000 || mp.MinAmountAtomic != 500 {
		t.Fatalf("unexpected model pricing: %+v", list[0].ModelPricing)
	}
}

func TestApplyHealthJSONWithoutModelsPreservesExisting(t *testing.T) {
	now := time.Now()
	r := New(30*time.Second, WithClock(func() time.Time { return now }))

	_ = r.ApplyNodeAnnounceProto(&apiv1.NodeAnnounce{
		NodeId:      "peer-x",
		Models:      []string{"llama"},
		TimestampMs: now.UnixMilli(),
	})

	payload := fmt.Sprintf(`{"node_id":"peer-x","uptime_sec":10,"timestamp_ms":%d}`, now.UnixMilli())
	if err := r.ApplyHealthJSON([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list[0].Models) != 1 || list[0].Models[0] != "llama" {
		t.Fatalf("models=%v want [llama] (should not be cleared)", list[0].Models)
	}
}

func TestApplyHealthProto(t *testing.T) {
	now := time.Now()
	r := New(10*time.Second, WithClock(func() time.Time { return now }))
	err := r.ApplyHealthProto(&apiv1.HealthUpdate{
		NodeId:      "x",
		UptimeSec:   5,
		TimestampMs: now.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list) != 1 || list[0].UptimeSec != 5 {
		t.Fatalf("list=%+v", list)
	}
}
