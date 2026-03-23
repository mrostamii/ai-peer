package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mrostamii/ai-peer/pkg/x402spike"
)

type X402PaywallConfig struct {
	FacilitatorURL string
	Requirement    x402spike.PaymentRequirements
}

type x402VerifyRequest struct {
	X402Version         int                           `json:"x402Version"`
	PaymentPayload      x402spike.PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements x402spike.PaymentRequirements `json:"paymentRequirements"`
}

type x402VerifyResponse struct {
	IsValid       bool   `json:"isValid"`
	InvalidReason string `json:"invalidReason,omitempty"`
	Payer         string `json:"payer,omitempty"`
}

func (p *OpenAIProxy) SetX402ChatPaywall(cfg *X402PaywallConfig) {
	p.chatPaywall = cfg
}

func (p *OpenAIProxy) enforceChatPayment(w http.ResponseWriter, r *http.Request) bool {
	if p.chatPaywall == nil {
		return true
	}
	paymentRequired := x402spike.PaymentRequired{
		X402Version: 2,
		Error:       "PAYMENT-SIGNATURE header is required",
		Resource: x402spike.ResourceInfo{
			URL:         requestURL(r),
			Description: "paid chat completion request",
			MimeType:    "application/json",
		},
		Accepts:    []x402spike.PaymentRequirements{p.chatPaywall.Requirement},
		Extensions: map[string]any{},
	}
	paymentHeader := r.Header.Get("PAYMENT-SIGNATURE")
	if strings.TrimSpace(paymentHeader) == "" {
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return false
	}

	var payload x402spike.PaymentPayload
	if err := x402spike.DecodeBase64JSON(paymentHeader, &payload); err != nil {
		paymentRequired.Error = "invalid PAYMENT-SIGNATURE header"
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return false
	}
	if err := validateAcceptedPayment(payload.Accepted, p.chatPaywall.Requirement); err != nil {
		paymentRequired.Error = err.Error()
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return false
	}

	if strings.TrimSpace(p.chatPaywall.FacilitatorURL) == "" {
		// Dev mode: accept signed payload without settlement.
		settle := x402spike.SettlementResponse{
			Success:     true,
			Payer:       payload.Payload.Authorization.From,
			Transaction: "0xspike-local-no-facilitator",
			Network:     payload.Accepted.Network,
		}
		if header, err := x402spike.EncodeBase64JSON(settle); err == nil {
			w.Header().Set("PAYMENT-RESPONSE", header)
		}
		return true
	}

	settle, err := settleWithFacilitator(p.chatPaywall.FacilitatorURL, payload, p.chatPaywall.Requirement)
	if err != nil {
		paymentRequired.Error = "facilitator error: " + err.Error()
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return false
	}
	if !settle.Success {
		paymentRequired.Error = "payment settlement failed"
		writePaymentRequired(w, paymentRequired, settle)
		return false
	}
	if header, err := x402spike.EncodeBase64JSON(settle); err == nil {
		w.Header().Set("PAYMENT-RESPONSE", header)
	}
	return true
}

func settleWithFacilitator(
	facilitatorURL string,
	payload x402spike.PaymentPayload,
	req x402spike.PaymentRequirements,
) (x402spike.SettlementResponse, error) {
	facilitatorURL = strings.TrimRight(strings.TrimSpace(facilitatorURL), "/")
	requestBody := x402VerifyRequest{
		X402Version:         2,
		PaymentPayload:      payload,
		PaymentRequirements: req,
	}
	var verifyRes x402VerifyResponse
	if err := postJSON(facilitatorURL+"/verify", requestBody, &verifyRes); err != nil {
		return x402spike.SettlementResponse{}, err
	}
	if !verifyRes.IsValid {
		return x402spike.SettlementResponse{
			Success:     false,
			ErrorReason: verifyRes.InvalidReason,
			Payer:       verifyRes.Payer,
			Transaction: "",
			Network:     req.Network,
		}, nil
	}
	var settleRes x402spike.SettlementResponse
	if err := postJSON(facilitatorURL+"/settle", requestBody, &settleRes); err != nil {
		return x402spike.SettlementResponse{}, err
	}
	return settleRes, nil
}

func postJSON(url string, reqBody any, out any) error {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
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

func validateAcceptedPayment(got, want x402spike.PaymentRequirements) error {
	if got.Scheme != want.Scheme ||
		got.Network != want.Network ||
		got.Amount != want.Amount ||
		got.Asset != want.Asset ||
		got.PayTo != want.PayTo {
		return fmt.Errorf("PAYMENT-SIGNATURE accepted requirement mismatch")
	}
	return nil
}

func requestURL(r *http.Request) string {
	scheme := "http"
	if r != nil && r.TLS != nil {
		scheme = "https"
	}
	host := ""
	path := ""
	if r != nil {
		host = r.Host
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	return scheme + "://" + host + path
}

func writePaymentRequired(w http.ResponseWriter, pr x402spike.PaymentRequired, settle x402spike.SettlementResponse) {
	requiredHeader, err := x402spike.EncodeBase64JSON(pr)
	if err != nil {
		http.Error(w, "failed to encode PAYMENT-REQUIRED", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("PAYMENT-REQUIRED", requiredHeader)
	if settle.Network != "" || settle.Transaction != "" || settle.ErrorReason != "" {
		if settleHeader, err := x402spike.EncodeBase64JSON(settle); err == nil {
			w.Header().Set("PAYMENT-RESPONSE", settleHeader)
		}
	}
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(pr)
}
