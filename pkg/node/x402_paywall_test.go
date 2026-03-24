package node

import (
	"testing"

	"github.com/mrostamii/ai-peer/pkg/apiv1"
	"github.com/mrostamii/ai-peer/pkg/config"
	"github.com/mrostamii/ai-peer/pkg/x402spike"
)

func TestComputeInferenceRequirementUsesModelPricing(t *testing.T) {
	t.Parallel()
	paywall := &x402InferencePaywallConfig{
		Requirement: x402spike.PaymentRequirements{
			Scheme:  "exact",
			Network: "eip155:84532",
			Amount:  "1",
			Asset:   "asset",
			PayTo:   "payto",
		},
		Pricing: x402PricingConfig{
			AtomicPer1KTokens:   10000,
			MinAmountAtomic:     1000,
			DefaultOutputTokens: 64,
		},
		ModelPricing: map[string]config.X402ModelPricing{
			"expensive": {
				PricePer1KAtomic: 20000,
			},
		},
	}
	req := &apiv1.InferenceRequest{
		Model: "expensive",
		Messages: []*apiv1.ChatMessage{
			{Role: "user", Content: "hello world"},
		},
	}
	got := computeInferenceRequirement(paywall, req)
	if got.Amount == "1000" || got.Amount == "1" {
		t.Fatalf("expected overridden dynamic amount, got %s", got.Amount)
	}
}

func TestEncodeDecodePaymentRequiredEnvelope(t *testing.T) {
	t.Parallel()
	pr := x402spike.PaymentRequired{
		X402Version: 2,
		Error:       "PAYMENT-SIGNATURE header is required",
		Resource: x402spike.ResourceInfo{
			URL: "http://127.0.0.1:8080/v1/chat/completions",
		},
		Accepts: []x402spike.PaymentRequirements{{
			Scheme:  "exact",
			Network: "eip155:84532",
			Amount:  "10000",
			Asset:   "asset",
			PayTo:   "payto",
		}},
	}
	raw := encodePaymentRequiredEnvelope("payment required", pr, x402spike.SettlementResponse{})
	parsed, ok := decodePaymentRequiredEnvelope(raw)
	if !ok {
		t.Fatalf("expected payment envelope to decode")
	}
	if parsed.PaymentRequiredHeader == "" {
		t.Fatalf("expected payment required header in envelope")
	}
}
