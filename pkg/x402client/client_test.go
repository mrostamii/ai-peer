package x402client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mrostamii/ai-peer/pkg/x402spike"
)

func TestDoWithPaymentRetriesAfter402(t *testing.T) {
	t.Parallel()
	var callCount int
	var firstBody, secondBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		raw, _ := io.ReadAll(r.Body)
		if callCount == 1 {
			firstBody = string(raw)
			pr := x402spike.PaymentRequired{
				X402Version: 2,
				Error:       "PAYMENT-SIGNATURE header is required",
				Resource: x402spike.ResourceInfo{
					URL:         "http://" + r.Host + r.URL.Path,
					Description: "paid endpoint",
					MimeType:    "application/json",
				},
				Accepts: []x402spike.PaymentRequirements{
					{
						Scheme:            "exact",
						Network:           "eip155:84532",
						Amount:            "10000",
						Asset:             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
						PayTo:             "0x209693Bc6afc0C5328bA36FaF03C514EF312287C",
						MaxTimeoutSeconds: 60,
						Extra: map[string]any{
							"name":    "USDC",
							"version": "2",
						},
					},
				},
			}
			header, _ := x402spike.EncodeBase64JSON(pr)
			w.Header().Set("PAYMENT-REQUIRED", header)
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(pr)
			return
		}
		secondBody = string(raw)
		if r.Header.Get("PAYMENT-SIGNATURE") == "" {
			t.Fatalf("expected PAYMENT-SIGNATURE on retry")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := &Client{
		PrivateKey: "0x4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4d4dfb6f9e9f5d1d7",
		NowProvider: func() time.Time {
			return time.Unix(1740672089, 0)
		},
	}
	client.HTTPClient = srv.Client()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"qwen2.5:3b"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.DoWithPayment(req)
	if err != nil {
		t.Fatalf("DoWithPayment returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if callCount != 2 {
		t.Fatalf("callCount=%d want 2", callCount)
	}
	if firstBody != secondBody {
		t.Fatalf("request body mismatch across retry")
	}
}

func TestDoWithPaymentPassesThroughNon402(t *testing.T) {
	t.Parallel()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := &Client{
		HTTPClient:  srv.Client(),
		PrivateKey:  "0x4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f4d4dfb6f9e9f5d1d7",
		NowProvider: time.Now,
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/health", nil)
	resp, err := client.DoWithPayment(req)
	if err != nil {
		t.Fatalf("DoWithPayment returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if callCount != 1 {
		t.Fatalf("callCount=%d want 1", callCount)
	}
}
