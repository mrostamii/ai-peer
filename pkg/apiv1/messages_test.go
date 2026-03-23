package apiv1

import "testing"

func TestHealthUpdateGetters(t *testing.T) {
	msg := &HealthUpdate{
		NodeId:      "peer-1",
		UptimeSec:   42,
		Load:        0.5,
		LatencyMs:   12,
		TimestampMs: 1234,
	}

	if got := msg.GetNodeId(); got != "peer-1" {
		t.Fatalf("GetNodeId()=%q want peer-1", got)
	}
	if got := msg.GetUptimeSec(); got != 42 {
		t.Fatalf("GetUptimeSec()=%d want 42", got)
	}
	if got := msg.GetLoad(); got != 0.5 {
		t.Fatalf("GetLoad()=%f want 0.5", got)
	}
	if got := msg.GetLatencyMs(); got != 12 {
		t.Fatalf("GetLatencyMs()=%d want 12", got)
	}
	if got := msg.GetTimestampMs(); got != 1234 {
		t.Fatalf("GetTimestampMs()=%d want 1234", got)
	}

	var nilMsg *HealthUpdate
	if got := nilMsg.GetNodeId(); got != "" {
		t.Fatalf("nil GetNodeId()=%q want empty", got)
	}
	if got := nilMsg.GetUptimeSec(); got != 0 {
		t.Fatalf("nil GetUptimeSec()=%d want 0", got)
	}
	if got := nilMsg.GetLoad(); got != 0 {
		t.Fatalf("nil GetLoad()=%f want 0", got)
	}
	if got := nilMsg.GetLatencyMs(); got != 0 {
		t.Fatalf("nil GetLatencyMs()=%d want 0", got)
	}
	if got := nilMsg.GetTimestampMs(); got != 0 {
		t.Fatalf("nil GetTimestampMs()=%d want 0", got)
	}
}

func TestNodeAnnounceGetters(t *testing.T) {
	msg := &NodeAnnounce{
		NodeId:          "peer-2",
		Models:          []string{"mistral", "llama"},
		HardwareSummary: "gpu",
		LocationHint:    "eu",
		PricingHint:     "$",
		TimestampMs:     5678,
	}

	if got := msg.GetNodeId(); got != "peer-2" {
		t.Fatalf("GetNodeId()=%q want peer-2", got)
	}
	models := msg.GetModels()
	if len(models) != 2 || models[0] != "mistral" || models[1] != "llama" {
		t.Fatalf("GetModels()=%v want [mistral llama]", models)
	}
	if got := msg.GetHardwareSummary(); got != "gpu" {
		t.Fatalf("GetHardwareSummary()=%q want gpu", got)
	}
	if got := msg.GetLocationHint(); got != "eu" {
		t.Fatalf("GetLocationHint()=%q want eu", got)
	}
	if got := msg.GetPricingHint(); got != "$" {
		t.Fatalf("GetPricingHint()=%q want $", got)
	}
	if got := msg.GetTimestampMs(); got != 5678 {
		t.Fatalf("GetTimestampMs()=%d want 5678", got)
	}

	var nilMsg *NodeAnnounce
	if got := nilMsg.GetNodeId(); got != "" {
		t.Fatalf("nil GetNodeId()=%q want empty", got)
	}
	if got := nilMsg.GetModels(); len(got) != 0 {
		t.Fatalf("nil GetModels()=%v want empty", got)
	}
	if got := nilMsg.GetHardwareSummary(); got != "" {
		t.Fatalf("nil GetHardwareSummary()=%q want empty", got)
	}
	if got := nilMsg.GetLocationHint(); got != "" {
		t.Fatalf("nil GetLocationHint()=%q want empty", got)
	}
	if got := nilMsg.GetPricingHint(); got != "" {
		t.Fatalf("nil GetPricingHint()=%q want empty", got)
	}
	if got := nilMsg.GetTimestampMs(); got != 0 {
		t.Fatalf("nil GetTimestampMs()=%d want 0", got)
	}
}
