package node

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mrostamii/tooti/pkg/apiv1"
	"github.com/mrostamii/tooti/pkg/config"
	"github.com/mrostamii/tooti/pkg/x402spike"
)

const (
	inferenceParamMaxTokens = "max_tokens"
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

type inferencePaymentSession struct {
	Payer           string
	Model           string
	PriorDebtAtomic int64
	PrepaidAtomic   int64
	Pricing         x402PricingConfig
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

func computeInferenceRequirement(paywall *x402InferencePaywallConfig, req *apiv1.InferenceRequest) x402spike.PaymentRequirements {
	reqOut, _ := computeInferenceRequirementAndAmount(paywall, req)
	return reqOut
}

func computeInferenceRequirementAndAmount(paywall *x402InferencePaywallConfig, req *apiv1.InferenceRequest) (x402spike.PaymentRequirements, int64) {
	requirement := paywall.Requirement
	pricing := resolveInferencePricing(paywall, req)
	if pricing.AtomicPer1KTokens <= 0 {
		requirement.Amount = "1"
		return requirement, 1
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
	return requirement, amount
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

func (r *Runtime) getPaymentDebt(payer string) int64 {
	payer = strings.ToLower(strings.TrimSpace(payer))
	if r == nil || payer == "" {
		return 0
	}
	r.paymentDebtMu.Lock()
	defer r.paymentDebtMu.Unlock()
	return r.paymentDebtByPayer[payer]
}

func (r *Runtime) setPaymentDebt(payer string, debt int64) {
	payer = strings.ToLower(strings.TrimSpace(payer))
	if r == nil || payer == "" {
		return
	}
	if debt < 0 {
		debt = 0
	}
	r.paymentDebtMu.Lock()
	defer r.paymentDebtMu.Unlock()
	if debt == 0 {
		delete(r.paymentDebtByPayer, payer)
		return
	}
	r.paymentDebtByPayer[payer] = debt
}

func (r *Runtime) reconcileActualUsage(session *inferencePaymentSession, tokensUsed int64) {
	if r == nil || session == nil || session.Payer == "" {
		return
	}
	if session.Pricing.AtomicPer1KTokens <= 0 {
		return
	}
	actualDue := r.computeActualDueAtomic(session, tokensUsed)
	outstanding := session.PriorDebtAtomic + actualDue - session.PrepaidAtomic
	if outstanding < 0 {
		outstanding = 0
	}
	r.setPaymentDebt(session.Payer, outstanding)
	if outstanding > 0 {
		logInferenceEvent(map[string]any{
			"event":             "x402_payment_underpaid",
			"payer":             session.Payer,
			"model":             session.Model,
			"prepaid_atomic":    session.PrepaidAtomic,
			"actual_due_atomic": actualDue,
			"prior_debt_atomic": session.PriorDebtAtomic,
			"new_debt_atomic":   outstanding,
			"tokens_used":       tokensUsed,
		})
	}
}

func (r *Runtime) computeActualDueAtomic(session *inferencePaymentSession, tokensUsed int64) int64 {
	if session == nil {
		return 0
	}
	if tokensUsed < 1 {
		tokensUsed = 1
	}
	actualDue := (tokensUsed*session.Pricing.AtomicPer1KTokens + 999) / 1000
	if session.Pricing.MinAmountAtomic > 0 && actualDue < session.Pricing.MinAmountAtomic {
		actualDue = session.Pricing.MinAmountAtomic
	}
	if actualDue < 1 {
		actualDue = 1
	}
	return actualDue
}
