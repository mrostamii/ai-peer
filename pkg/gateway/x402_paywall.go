package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrostamii/tooti/pkg/x402spike"
)

type X402PaywallConfig struct {
	FacilitatorURL string
	Requirement    x402spike.PaymentRequirements
	TokenPricing   *X402TokenPricingConfig
	ModelPricing   map[string]X402TokenPricingConfig
}

type X402TokenPricingConfig struct {
	// AtomicPer1KTokens is the price in token atomic units per 1K estimated tokens.
	AtomicPer1KTokens int64
	// MinAmountAtomic is the minimum amount charged per request in atomic units.
	MinAmountAtomic int64
	// MaxAmountAtomic is the maximum amount charged per request in atomic units.
	MaxAmountAtomic int64
	// DefaultOutputTokens is used when max_tokens is not provided by the client.
	DefaultOutputTokens int64
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

func (p *OpenAIProxy) SetX402PrepaidTopupPaywall(cfg *X402PaywallConfig) {
	p.prepaidTopupPaywall = cfg
}

func (p *OpenAIProxy) enforceChatPayment(w http.ResponseWriter, r *http.Request, oreq *openAIChatRequest) bool {
	if p.chatPaywall == nil {
		return true
	}
	started := time.Now()
	reqURL := requestURL(r)
	requestID := strings.TrimSpace(r.Header.Get("X-Tooti-Request-ID"))
	requirement, inputTokens, outputTokens, totalTokens := p.computePaymentRequirement(oreq)
	paymentRequired := x402spike.PaymentRequired{
		X402Version: 2,
		Error:       "PAYMENT-SIGNATURE header is required",
		Resource: x402spike.ResourceInfo{
			URL:         reqURL,
			Description: "paid chat completion request",
			MimeType:    "application/json",
		},
		Accepts:    []x402spike.PaymentRequirements{requirement},
		Extensions: map[string]any{},
	}
	paymentHeader := r.Header.Get("PAYMENT-SIGNATURE")
	if strings.TrimSpace(paymentHeader) == "" {
		p.logRequest(map[string]any{
			"event":      "x402_payment_required",
			"request_id": requestID,
			"url":        reqURL,
			"network":    requirement.Network,
			"asset":      requirement.Asset,
			"amount":     requirement.Amount,
			"pay_to":     requirement.PayTo,
			"tokens_in":  inputTokens,
			"tokens_out": outputTokens,
			"tokens_est": totalTokens,
			"reason":     "missing_payment_signature",
			"latency_ms": time.Since(started).Milliseconds(),
		})
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return false
	}

	var payload x402spike.PaymentPayload
	if err := x402spike.DecodeBase64JSON(paymentHeader, &payload); err != nil {
		p.logRequest(map[string]any{
			"event":      "x402_payment_rejected",
			"request_id": requestID,
			"url":        reqURL,
			"reason":     "invalid_payment_signature_header",
			"error":      err.Error(),
			"latency_ms": time.Since(started).Milliseconds(),
		})
		paymentRequired.Error = "invalid PAYMENT-SIGNATURE header"
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return false
	}
	if err := validateAcceptedPayment(payload.Accepted, requirement); err != nil {
		p.logRequest(map[string]any{
			"event":      "x402_payment_rejected",
			"request_id": requestID,
			"url":        reqURL,
			"payer":      payload.Payload.Authorization.From,
			"reason":     "accepted_requirement_mismatch",
			"error":      err.Error(),
			"latency_ms": time.Since(started).Milliseconds(),
		})
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
		p.logRequest(map[string]any{
			"event":      "x402_payment_accepted",
			"request_id": requestID,
			"url":        reqURL,
			"payer":      settle.Payer,
			"network":    settle.Network,
			"tx":         settle.Transaction,
			"mode":       "local_no_facilitator",
			"latency_ms": time.Since(started).Milliseconds(),
		})
		return true
	}

	settle, verifyMS, settleMS, err := settleWithFacilitator(p.chatPaywall.FacilitatorURL, payload, requirement)
	if err != nil {
		p.logRequest(map[string]any{
			"event":       "x402_payment_rejected",
			"request_id":  requestID,
			"url":         reqURL,
			"payer":       payload.Payload.Authorization.From,
			"reason":      "facilitator_error",
			"error":       err.Error(),
			"verify_ms":   verifyMS,
			"settle_ms":   settleMS,
			"latency_ms":  time.Since(started).Milliseconds(),
			"facilitator": strings.TrimSpace(p.chatPaywall.FacilitatorURL),
		})
		paymentRequired.Error = "facilitator error: " + err.Error()
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return false
	}
	if !settle.Success {
		if strings.TrimSpace(settle.ErrorReason) != "" {
			paymentRequired.Error = "payment settlement failed: " + strings.TrimSpace(settle.ErrorReason)
		} else {
			paymentRequired.Error = "payment settlement failed"
		}
		p.logRequest(map[string]any{
			"event":       "x402_payment_rejected",
			"request_id":  requestID,
			"url":         reqURL,
			"payer":       settle.Payer,
			"reason":      "settlement_failed",
			"error":       settle.ErrorReason,
			"verify_ms":   verifyMS,
			"settle_ms":   settleMS,
			"latency_ms":  time.Since(started).Milliseconds(),
			"facilitator": strings.TrimSpace(p.chatPaywall.FacilitatorURL),
		})
		writePaymentRequired(w, paymentRequired, settle)
		return false
	}
	if header, err := x402spike.EncodeBase64JSON(settle); err == nil {
		w.Header().Set("PAYMENT-RESPONSE", header)
	}
	p.logRequest(map[string]any{
		"event":       "x402_payment_accepted",
		"request_id":  requestID,
		"url":         reqURL,
		"payer":       settle.Payer,
		"network":     settle.Network,
		"tx":          settle.Transaction,
		"verify_ms":   verifyMS,
		"settle_ms":   settleMS,
		"latency_ms":  time.Since(started).Milliseconds(),
		"facilitator": strings.TrimSpace(p.chatPaywall.FacilitatorURL),
	})
	return true
}

func (p *OpenAIProxy) computePaymentRequirement(req *openAIChatRequest) (x402spike.PaymentRequirements, int64, int64, int64) {
	requirement := p.chatPaywall.Requirement
	if p.chatPaywall == nil || p.chatPaywall.TokenPricing == nil {
		return requirement, 0, 0, 0
	}
	pricing := p.resolveTokenPricing(req)
	if pricing.AtomicPer1KTokens <= 0 {
		return requirement, 0, 0, 0
	}
	inputTokens := estimateInputTokens(req)
	outputTokens := pricing.DefaultOutputTokens
	if req != nil && req.MaxTokens != nil && *req.MaxTokens > 0 {
		v := int64(*req.MaxTokens)
		if v > outputTokens {
			outputTokens = v
		}
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	totalTokens := inputTokens + outputTokens
	if totalTokens < 1 {
		totalTokens = 1
	}
	amount := (totalTokens*pricing.AtomicPer1KTokens + 999) / 1000
	if pricing.MinAmountAtomic > 0 && amount < pricing.MinAmountAtomic {
		amount = pricing.MinAmountAtomic
	}
	if pricing.MaxAmountAtomic > 0 && amount > pricing.MaxAmountAtomic {
		amount = pricing.MaxAmountAtomic
	}
	if amount < 1 {
		amount = 1
	}
	requirement.Amount = strconv.FormatInt(amount, 10)
	return requirement, inputTokens, outputTokens, totalTokens
}

func (p *OpenAIProxy) resolveTokenPricing(req *openAIChatRequest) X402TokenPricingConfig {
	if p == nil || p.chatPaywall == nil || p.chatPaywall.TokenPricing == nil {
		return X402TokenPricingConfig{}
	}
	pricing := *p.chatPaywall.TokenPricing
	if req == nil {
		return pricing
	}
	model := strings.TrimSpace(req.Model)
	if model == "" || len(p.chatPaywall.ModelPricing) == 0 {
		return pricing
	}
	if m, ok := p.chatPaywall.ModelPricing[model]; ok {
		if m.AtomicPer1KTokens > 0 {
			pricing.AtomicPer1KTokens = m.AtomicPer1KTokens
		}
		if m.MinAmountAtomic > 0 {
			pricing.MinAmountAtomic = m.MinAmountAtomic
		}
		if m.MaxAmountAtomic > 0 {
			pricing.MaxAmountAtomic = m.MaxAmountAtomic
		}
		if m.DefaultOutputTokens > 0 {
			pricing.DefaultOutputTokens = m.DefaultOutputTokens
		}
	}
	return pricing
}

func estimateInputTokens(req *openAIChatRequest) int64 {
	if req == nil || len(req.Messages) == 0 {
		return 0
	}
	var total int64
	for _, msg := range req.Messages {
		// Lightweight token estimate for pre-inference paywall pricing.
		// Typical English text is close to 4 chars/token.
		contentTokens := int64((len(msg.Content) + 3) / 4)
		roleTokens := int64((len(msg.Role) + 3) / 4)
		total += contentTokens + roleTokens + 4
	}
	return total
}

func settleWithFacilitator(
	facilitatorURL string,
	payload x402spike.PaymentPayload,
	req x402spike.PaymentRequirements,
) (x402spike.SettlementResponse, int64, int64, error) {
	facilitatorURL = strings.TrimRight(strings.TrimSpace(facilitatorURL), "/")
	requestBody := x402VerifyRequest{
		X402Version:         2,
		PaymentPayload:      payload,
		PaymentRequirements: req,
	}
	verifyStarted := time.Now()
	var verifyRes x402VerifyResponse
	if err := postJSON(facilitatorURL+"/verify", requestBody, &verifyRes); err != nil {
		return x402spike.SettlementResponse{}, time.Since(verifyStarted).Milliseconds(), 0, err
	}
	verifyMS := time.Since(verifyStarted).Milliseconds()
	if !verifyRes.IsValid {
		return x402spike.SettlementResponse{
			Success:     false,
			ErrorReason: verifyRes.InvalidReason,
			Payer:       verifyRes.Payer,
			Transaction: "",
			Network:     req.Network,
		}, verifyMS, 0, nil
	}
	settleStarted := time.Now()
	var settleRes x402spike.SettlementResponse
	if err := postJSON(facilitatorURL+"/settle", requestBody, &settleRes); err != nil {
		return x402spike.SettlementResponse{}, verifyMS, time.Since(settleStarted).Milliseconds(), err
	}
	return settleRes, verifyMS, time.Since(settleStarted).Milliseconds(), nil
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
