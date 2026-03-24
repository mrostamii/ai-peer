package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrostamii/ai-peer/pkg/apiv1"
	"github.com/mrostamii/ai-peer/pkg/config"
	"github.com/mrostamii/ai-peer/pkg/x402spike"
)

const (
	inferenceParamPaymentSignature = "payment_signature"
	inferenceParamResourceURL      = "x402_resource_url"
	inferenceParamMaxTokens        = "max_tokens"
)

type x402InferencePaywallConfig struct {
	FacilitatorURL string
	Requirement    x402spike.PaymentRequirements
	Pricing        x402PricingConfig
	ModelPricing   map[string]config.X402ModelPricing
}

type x402PricingConfig struct {
	AtomicPer1KTokens   int64
	MinAmountAtomic     int64
	DefaultOutputTokens int64
}

type x402RemoteErrorEnvelope struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	PaymentRequired string `json:"payment_required,omitempty"`
	PaymentResponse string `json:"payment_response,omitempty"`
}

type PaymentRequiredError struct {
	Message               string
	PaymentRequiredHeader string
	PaymentResponseHeader string
}

func (e *PaymentRequiredError) Error() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "payment required"
	}
	return e.Message
}

func buildInferencePaywallConfig(cfg *config.Config) *x402InferencePaywallConfig {
	if cfg == nil || !cfg.Node.X402.Enabled {
		return nil
	}
	return &x402InferencePaywallConfig{
		FacilitatorURL: strings.TrimSpace(cfg.Node.X402.FacilitatorURL),
		Requirement: x402spike.PaymentRequirements{
			Scheme:            "exact",
			Network:           strings.TrimSpace(cfg.Node.X402.Network),
			Amount:            "1",
			Asset:             strings.TrimSpace(cfg.Node.X402.Asset),
			PayTo:             strings.TrimSpace(cfg.Node.X402.PayTo),
			MaxTimeoutSeconds: 60,
			Extra: map[string]any{
				"name":    strings.TrimSpace(cfg.Node.X402.TokenName),
				"version": strings.TrimSpace(cfg.Node.X402.TokenVersion),
			},
		},
		Pricing: x402PricingConfig{
			AtomicPer1KTokens:   cfg.Node.X402.PricePer1KAtomic,
			MinAmountAtomic:     cfg.Node.X402.MinAmountAtomic,
			DefaultOutputTokens: cfg.Node.X402.DefaultOutputTokens,
		},
		ModelPricing: cfg.Models.ModelPricing,
	}
}

func buildAdvertisedModelPricing(cfg *config.Config) map[string]ModelPricingHint {
	if cfg == nil || !cfg.Node.X402.Enabled || cfg.Node.X402.PricePer1KAtomic <= 0 {
		return nil
	}
	out := make(map[string]ModelPricingHint, len(cfg.Models.Advertised))
	for _, model := range cfg.Models.Advertised {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		effective := ModelPricingHint{
			PricePer1KAtomic:    cfg.Node.X402.PricePer1KAtomic,
			MinAmountAtomic:     cfg.Node.X402.MinAmountAtomic,
			DefaultOutputTokens: cfg.Node.X402.DefaultOutputTokens,
		}
		if perModel, ok := cfg.Models.ModelPricing[model]; ok {
			if perModel.PricePer1KAtomic > 0 {
				effective.PricePer1KAtomic = perModel.PricePer1KAtomic
			}
			if perModel.MinAmountAtomic > 0 {
				effective.MinAmountAtomic = perModel.MinAmountAtomic
			}
			if perModel.MaxAmountAtomic > 0 {
				effective.MaxAmountAtomic = perModel.MaxAmountAtomic
			}
			if perModel.DefaultOutputTokens > 0 {
				effective.DefaultOutputTokens = perModel.DefaultOutputTokens
			}
		}
		out[model] = effective
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r *Runtime) enforceInferencePayment(req *apiv1.InferenceRequest) (string, bool) {
	if r == nil || r.inferencePaywall == nil {
		return "", true
	}
	paywall := r.inferencePaywall
	resourceURL := "http://ai-peer.local/v1/chat/completions"
	if req != nil && req.GetParams() != nil {
		if got := strings.TrimSpace(req.GetParams()[inferenceParamResourceURL]); got != "" {
			resourceURL = got
		}
	}
	requirement := computeInferenceRequirement(paywall, req)
	paymentRequired := x402spike.PaymentRequired{
		X402Version: 2,
		Error:       "PAYMENT-SIGNATURE header is required",
		Resource: x402spike.ResourceInfo{
			URL:         resourceURL,
			Description: "paid inference request",
			MimeType:    "application/json",
		},
		Accepts:    []x402spike.PaymentRequirements{requirement},
		Extensions: map[string]any{},
	}

	paymentHeader := ""
	if req != nil && req.GetParams() != nil {
		paymentHeader = strings.TrimSpace(req.GetParams()[inferenceParamPaymentSignature])
	}
	if paymentHeader == "" {
		return encodePaymentRequiredEnvelope("PAYMENT-SIGNATURE header is required", paymentRequired, x402spike.SettlementResponse{}), false
	}

	var payload x402spike.PaymentPayload
	if err := x402spike.DecodeBase64JSON(paymentHeader, &payload); err != nil {
		paymentRequired.Error = "invalid PAYMENT-SIGNATURE header"
		return encodePaymentRequiredEnvelope(paymentRequired.Error, paymentRequired, x402spike.SettlementResponse{}), false
	}
	if err := validateAcceptedPayment(payload.Accepted, requirement); err != nil {
		paymentRequired.Error = err.Error()
		return encodePaymentRequiredEnvelope(paymentRequired.Error, paymentRequired, x402spike.SettlementResponse{}), false
	}

	if strings.TrimSpace(paywall.FacilitatorURL) == "" {
		return "", true
	}
	settle, _, _, err := settleWithFacilitator(paywall.FacilitatorURL, payload, requirement)
	if err != nil {
		paymentRequired.Error = "facilitator error: " + err.Error()
		return encodePaymentRequiredEnvelope(paymentRequired.Error, paymentRequired, x402spike.SettlementResponse{}), false
	}
	if !settle.Success {
		if strings.TrimSpace(settle.ErrorReason) != "" {
			paymentRequired.Error = "payment settlement failed: " + strings.TrimSpace(settle.ErrorReason)
		} else {
			paymentRequired.Error = "payment settlement failed"
		}
		return encodePaymentRequiredEnvelope(paymentRequired.Error, paymentRequired, settle), false
	}
	return "", true
}

func computeInferenceRequirement(paywall *x402InferencePaywallConfig, req *apiv1.InferenceRequest) x402spike.PaymentRequirements {
	requirement := paywall.Requirement
	pricing := resolveInferencePricing(paywall, req)
	if pricing.AtomicPer1KTokens <= 0 {
		requirement.Amount = "1"
		return requirement
	}
	inputTokens := estimateInferenceInputTokens(req)
	outputTokens := pricing.DefaultOutputTokens
	if req != nil && req.GetParams() != nil {
		if raw := strings.TrimSpace(req.GetParams()[inferenceParamMaxTokens]); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
				// Provider-side baseline prevents clients from forcing very low prepay.
				if v > outputTokens {
					outputTokens = v
				}
			}
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
	if amount < 1 {
		amount = 1
	}
	requirement.Amount = strconv.FormatInt(amount, 10)
	return requirement
}

func resolveInferencePricing(paywall *x402InferencePaywallConfig, req *apiv1.InferenceRequest) x402PricingConfig {
	pricing := paywall.Pricing
	if paywall == nil || req == nil || len(paywall.ModelPricing) == 0 {
		return pricing
	}
	model := strings.TrimSpace(req.GetModel())
	if model == "" {
		return pricing
	}
	if mp, ok := paywall.ModelPricing[model]; ok {
		if mp.PricePer1KAtomic > 0 {
			pricing.AtomicPer1KTokens = mp.PricePer1KAtomic
		}
		if mp.MinAmountAtomic > 0 {
			pricing.MinAmountAtomic = mp.MinAmountAtomic
		}
		if mp.DefaultOutputTokens > 0 {
			pricing.DefaultOutputTokens = mp.DefaultOutputTokens
		}
	}
	return pricing
}

func estimateInferenceInputTokens(req *apiv1.InferenceRequest) int64 {
	if req == nil || len(req.GetMessages()) == 0 {
		return 0
	}
	var total int64
	for _, msg := range req.GetMessages() {
		contentTokens := int64((len(msg.GetContent()) + 3) / 4)
		roleTokens := int64((len(msg.GetRole()) + 3) / 4)
		total += contentTokens + roleTokens + 4
	}
	return total
}

func encodePaymentRequiredEnvelope(message string, pr x402spike.PaymentRequired, settle x402spike.SettlementResponse) string {
	requiredHeader, err := x402spike.EncodeBase64JSON(pr)
	if err != nil {
		return `{"code":"payment_required","message":"failed to encode payment requirement"}`
	}
	env := x402RemoteErrorEnvelope{
		Code:            "payment_required",
		Message:         message,
		PaymentRequired: requiredHeader,
	}
	if settle.Network != "" || settle.Transaction != "" || settle.ErrorReason != "" {
		if settleHeader, err := x402spike.EncodeBase64JSON(settle); err == nil {
			env.PaymentResponse = settleHeader
		}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return `{"code":"payment_required","message":"payment required"}`
	}
	return string(raw)
}

func decodePaymentRequiredEnvelope(msg string) (*PaymentRequiredError, bool) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil, false
	}
	var env x402RemoteErrorEnvelope
	if err := json.Unmarshal([]byte(msg), &env); err != nil {
		return nil, false
	}
	if env.Code != "payment_required" || strings.TrimSpace(env.PaymentRequired) == "" {
		return nil, false
	}
	return &PaymentRequiredError{
		Message:               strings.TrimSpace(env.Message),
		PaymentRequiredHeader: strings.TrimSpace(env.PaymentRequired),
		PaymentResponseHeader: strings.TrimSpace(env.PaymentResponse),
	}, true
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
