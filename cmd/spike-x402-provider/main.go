package main

import (
	"bytes"
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

type verifyRequest struct {
	X402Version         int                           `json:"x402Version"`
	PaymentPayload      x402spike.PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements x402spike.PaymentRequirements `json:"paymentRequirements"`
}

type verifyResponse struct {
	IsValid       bool   `json:"isValid"`
	InvalidReason string `json:"invalidReason,omitempty"`
	Payer         string `json:"payer,omitempty"`
}

func main() {
	var (
		listen      = flag.String("listen", "127.0.0.1:8091", "HTTP listen address")
		path        = flag.String("path", "/paid", "paid endpoint path")
		network     = flag.String("network", "eip155:84532", "CAIP-2 network (Base Sepolia is eip155:84532)")
		asset       = flag.String("asset", "0x036CbD53842c5426634e7929541eC2318f3dCF7e", "ERC-20 token contract address")
		amount      = flag.String("amount", "10000", "payment amount in atomic units (10000 = 0.01 USDC)")
		payTo       = flag.String("payto", "", "provider wallet address receiving payment")
		domainName  = flag.String("token-name", "USD Coin", "EIP-712 token name")
		domainVer   = flag.String("token-version", "2", "EIP-712 token version")
		facilitator = flag.String("facilitator", "", "optional facilitator base URL (enables real onchain settlement)")
	)
	flag.Parse()

	if strings.TrimSpace(*payTo) == "" {
		fmt.Fprintln(os.Stderr, "missing required -payto (provider wallet address)")
		os.Exit(2)
	}

	requirement := x402spike.PaymentRequirements{
		Scheme:            "exact",
		Network:           *network,
		Amount:            *amount,
		Asset:             *asset,
		PayTo:             *payTo,
		MaxTimeoutSeconds: 60,
		Extra: map[string]any{
			"name":    *domainName,
			"version": *domainVer,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc(*path, func(w http.ResponseWriter, r *http.Request) {
		resource := x402spike.ResourceInfo{
			URL:         fmt.Sprintf("http://%s%s", *listen, *path),
			Description: "x402 spike endpoint",
			MimeType:    "application/json",
		}
		paymentRequired := x402spike.PaymentRequired{
			X402Version: 2,
			Error:       "PAYMENT-SIGNATURE header is required",
			Resource:    resource,
			Accepts:     []x402spike.PaymentRequirements{requirement},
			Extensions:  map[string]any{},
		}

		header := r.Header.Get("PAYMENT-SIGNATURE")
		if strings.TrimSpace(header) == "" {
			writePaymentRequired(w, paymentRequired)
			return
		}

		var payload x402spike.PaymentPayload
		if err := x402spike.DecodeBase64JSON(header, &payload); err != nil {
			paymentRequired.Error = "invalid PAYMENT-SIGNATURE header"
			writePaymentRequired(w, paymentRequired)
			return
		}
		if err := validatePayload(payload, requirement); err != nil {
			paymentRequired.Error = err.Error()
			writePaymentRequired(w, paymentRequired)
			return
		}

		settleRes := x402spike.SettlementResponse{
			Success:     true,
			Payer:       payload.Payload.Authorization.From,
			Transaction: "0xspike-local-no-facilitator",
			Network:     requirement.Network,
		}
		if strings.TrimSpace(*facilitator) != "" {
			res, err := verifyAndSettleViaFacilitator(*facilitator, payload, requirement)
			if err != nil {
				paymentRequired.Error = "facilitator error: " + err.Error()
				writePaymentRequired(w, paymentRequired)
				return
			}
			settleRes = res
			if !settleRes.Success {
				paymentRequired.Error = "payment settlement failed"
				writePaymentRequiredWithSettlement(w, paymentRequired, settleRes)
				return
			}
		}

		headerValue, err := x402spike.EncodeBase64JSON(settleRes)
		if err != nil {
			http.Error(w, "failed to encode PAYMENT-RESPONSE", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("PAYMENT-RESPONSE", headerValue)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          true,
			"provider":    requirement.PayTo,
			"amount":      requirement.Amount,
			"network":     requirement.Network,
			"transaction": settleRes.Transaction,
		})
	})

	log.Printf("x402 spike provider listening at http://%s%s", *listen, *path)
	if strings.TrimSpace(*facilitator) == "" {
		log.Printf("warning: running without facilitator, so no real onchain settlement will happen")
	}
	if err := http.ListenAndServe(*listen, mux); err != nil {
		log.Fatal(err)
	}
}

func verifyAndSettleViaFacilitator(
	facilitator string,
	payload x402spike.PaymentPayload,
	requirement x402spike.PaymentRequirements,
) (x402spike.SettlementResponse, error) {
	facilitator = strings.TrimRight(strings.TrimSpace(facilitator), "/")
	body := verifyRequest{
		X402Version:         2,
		PaymentPayload:      payload,
		PaymentRequirements: requirement,
	}
	verifyURL := facilitator + "/verify"
	settleURL := facilitator + "/settle"

	var verifyRes verifyResponse
	if err := postJSON(verifyURL, body, &verifyRes); err != nil {
		return x402spike.SettlementResponse{}, err
	}
	if !verifyRes.IsValid {
		return x402spike.SettlementResponse{
			Success:     false,
			ErrorReason: verifyRes.InvalidReason,
			Payer:       verifyRes.Payer,
			Transaction: "",
			Network:     requirement.Network,
		}, nil
	}

	var settleRes x402spike.SettlementResponse
	if err := postJSON(settleURL, body, &settleRes); err != nil {
		return x402spike.SettlementResponse{}, err
	}
	return settleRes, nil
}

func postJSON(url string, reqBody any, out any) error {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s returned status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func validatePayload(payload x402spike.PaymentPayload, req x402spike.PaymentRequirements) error {
	if payload.X402Version != 2 {
		return fmt.Errorf("invalid x402Version: got %d", payload.X402Version)
	}
	if payload.Accepted.Scheme != req.Scheme ||
		payload.Accepted.Network != req.Network ||
		payload.Accepted.Asset != req.Asset ||
		payload.Accepted.PayTo != req.PayTo ||
		payload.Accepted.Amount != req.Amount {
		return fmt.Errorf("PAYMENT-SIGNATURE accepted requirement mismatch")
	}
	if strings.TrimSpace(payload.Payload.Signature) == "" {
		return fmt.Errorf("missing signature in payload")
	}
	if payload.Payload.Authorization.To != req.PayTo {
		return fmt.Errorf("authorization recipient mismatch")
	}
	if payload.Payload.Authorization.Value != req.Amount {
		return fmt.Errorf("authorization value mismatch")
	}
	return nil
}

func writePaymentRequired(w http.ResponseWriter, paymentRequired x402spike.PaymentRequired) {
	writePaymentRequiredWithSettlement(w, paymentRequired, x402spike.SettlementResponse{})
}

func writePaymentRequiredWithSettlement(
	w http.ResponseWriter,
	paymentRequired x402spike.PaymentRequired,
	settlement x402spike.SettlementResponse,
) {
	headerValue, err := x402spike.EncodeBase64JSON(paymentRequired)
	if err != nil {
		http.Error(w, "failed to encode PAYMENT-REQUIRED", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("PAYMENT-REQUIRED", headerValue)
	if settlement.Network != "" || settlement.Transaction != "" || settlement.ErrorReason != "" {
		if settleHeader, err := x402spike.EncodeBase64JSON(settlement); err == nil {
			w.Header().Set("PAYMENT-RESPONSE", settleHeader)
		}
	}
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(paymentRequired)
}
