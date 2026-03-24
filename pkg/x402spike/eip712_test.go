package x402spike

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestBuildPaymentPayloadSignsExactEVMUSDC(t *testing.T) {
	req := PaymentRequirements{
		Scheme:            "exact",
		Network:           "eip155:84532",
		Amount:            "10000",
		Asset:             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		PayTo:             "0x209693Bc6afc0C5328bA36FaF03C514EF312287C",
		MaxTimeoutSeconds: 60,
		Extra: map[string]any{
			"name":    "USD Coin",
			"version": "2",
		},
	}
	resource := ResourceInfo{
		URL:         "http://127.0.0.1:8091/paid",
		Description: "x402 spike endpoint",
		MimeType:    "application/json",
	}

	payload, err := BuildPaymentPayload(
		"0x4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4d4dfb6f9e9f5d1d7",
		req,
		resource,
		time.Unix(1740672089, 0),
	)
	if err != nil {
		t.Fatalf("BuildPaymentPayload returned error: %v", err)
	}
	if payload.X402Version != 2 {
		t.Fatalf("unexpected x402Version: got %d want 2", payload.X402Version)
	}
	if payload.Accepted.Network != "eip155:84532" {
		t.Fatalf("unexpected accepted network: got %q", payload.Accepted.Network)
	}
	if got, want := payload.Payload.Authorization.Value, "10000"; got != want {
		t.Fatalf("unexpected authorization value: got %q want %q", got, want)
	}
	if got := len(payload.Payload.Signature); got != 132 {
		t.Fatalf("unexpected signature hex length: got %d want 132", got)
	}
	if payload.Payload.Authorization.Nonce == "" {
		t.Fatalf("expected nonce to be populated")
	}
}

func TestPaymentHeaderRoundTrip(t *testing.T) {
	pr := PaymentRequired{
		X402Version: 2,
		Error:       "PAYMENT-SIGNATURE header is required",
		Resource: ResourceInfo{
			URL: "http://127.0.0.1:8091/paid",
		},
		Accepts: []PaymentRequirements{
			{
				Scheme:            "exact",
				Network:           "eip155:84532",
				Amount:            "10000",
				Asset:             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
				PayTo:             "0x209693Bc6afc0C5328bA36FaF03C514EF312287C",
				MaxTimeoutSeconds: 60,
			},
		},
	}

	header, err := EncodeBase64JSON(pr)
	if err != nil {
		t.Fatalf("EncodeBase64JSON returned error: %v", err)
	}
	decodedJSON, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("header is not valid base64: %v", err)
	}
	var got PaymentRequired
	if err := json.Unmarshal(decodedJSON, &got); err != nil {
		t.Fatalf("decoded header is not valid PaymentRequired JSON: %v", err)
	}
	if got.X402Version != 2 {
		t.Fatalf("unexpected x402Version: got %d want 2", got.X402Version)
	}
	if got.Accepts[0].PayTo != pr.Accepts[0].PayTo {
		t.Fatalf("unexpected payTo after roundtrip")
	}
}
