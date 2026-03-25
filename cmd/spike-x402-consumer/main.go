package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mrostamii/tooti/pkg/x402spike"
)

func main() {
	var (
		url        = flag.String("url", "http://127.0.0.1:8091/paid", "paid resource URL")
		privateKey = flag.String("private-key", "", "consumer EVM private key hex (defaults to EVM_PRIVATE_KEY env)")
		timeoutSec = flag.Int("timeout-sec", 20, "HTTP request timeout in seconds")
	)
	flag.Parse()

	if strings.TrimSpace(*privateKey) == "" {
		*privateKey = strings.TrimSpace(os.Getenv("EVM_PRIVATE_KEY"))
	}
	if strings.TrimSpace(*privateKey) == "" {
		fmt.Fprintln(os.Stderr, "missing private key: set -private-key or EVM_PRIVATE_KEY")
		os.Exit(2)
	}

	client := &http.Client{Timeout: time.Duration(*timeoutSec) * time.Second}
	firstReq, err := http.NewRequest(http.MethodGet, *url, nil)
	if err != nil {
		log.Fatal(err)
	}

	firstRes, err := client.Do(firstReq)
	if err != nil {
		log.Fatal(err)
	}
	defer firstRes.Body.Close()

	if firstRes.StatusCode != http.StatusPaymentRequired {
		fmt.Printf("first request status=%d (expected 402)\n", firstRes.StatusCode)
		return
	}

	requiredHeader := firstRes.Header.Get("PAYMENT-REQUIRED")
	if strings.TrimSpace(requiredHeader) == "" {
		log.Fatal("402 response missing PAYMENT-REQUIRED header")
	}
	var paymentRequired x402spike.PaymentRequired
	if err := x402spike.DecodeBase64JSON(requiredHeader, &paymentRequired); err != nil {
		log.Fatalf("decode PAYMENT-REQUIRED: %v", err)
	}
	if len(paymentRequired.Accepts) == 0 {
		log.Fatal("PAYMENT-REQUIRED has empty accepts list")
	}
	chosen := paymentRequired.Accepts[0]

	payload, err := x402spike.BuildPaymentPayload(*privateKey, chosen, paymentRequired.Resource, time.Now())
	if err != nil {
		log.Fatalf("build PAYMENT-SIGNATURE payload: %v", err)
	}
	paymentHeader, err := x402spike.EncodeBase64JSON(payload)
	if err != nil {
		log.Fatalf("encode PAYMENT-SIGNATURE: %v", err)
	}

	retryReq, err := http.NewRequest(http.MethodGet, *url, nil)
	if err != nil {
		log.Fatal(err)
	}
	retryReq.Header.Set("PAYMENT-SIGNATURE", paymentHeader)

	retryRes, err := client.Do(retryReq)
	if err != nil {
		log.Fatal(err)
	}
	defer retryRes.Body.Close()

	fmt.Printf("retry status=%d\n", retryRes.StatusCode)
	fmt.Printf("network=%s amount=%s asset=%s payTo=%s\n", chosen.Network, chosen.Amount, chosen.Asset, chosen.PayTo)
	fmt.Printf("signed from=%s nonce=%s\n", payload.Payload.Authorization.From, payload.Payload.Authorization.Nonce)

	settleHeader := retryRes.Header.Get("PAYMENT-RESPONSE")
	if strings.TrimSpace(settleHeader) != "" {
		var settle x402spike.SettlementResponse
		if err := x402spike.DecodeBase64JSON(settleHeader, &settle); err != nil {
			fmt.Printf("failed to decode PAYMENT-RESPONSE: %v\n", err)
		} else {
			raw, _ := json.MarshalIndent(settle, "", "  ")
			fmt.Printf("settlement=%s\n", raw)
		}
	}
}
