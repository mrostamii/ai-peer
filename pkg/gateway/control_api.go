package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrostamii/tooti/pkg/x402spike"
)

type ProviderRegisterRequest struct {
	ProviderID    string         `json:"provider_id"`
	PeerID        string         `json:"peer_id"`
	Status        string         `json:"status"`
	WalletAddress string         `json:"wallet_address"`
	Metadata      map[string]any `json:"metadata"`
}

type ProviderHeartbeatRequest struct {
	ProviderID string `json:"provider_id"`
	PeerID     string `json:"peer_id"`
	Status     string `json:"status"`
}

type ProviderWalletRotateRequest struct {
	ProviderID      string `json:"provider_id"`
	NewWallet       string `json:"new_wallet"`
	RotationProof   string `json:"rotation_proof"`
	RequestedByPeer string `json:"requested_by_peer"`
}

type UsageEvent struct {
	RequestID     string    `json:"request_id"`
	GatewayID     string    `json:"gateway_id"`
	GatewayType   string    `json:"gateway_type"`
	ConsumerID    string    `json:"consumer_id"`
	ProviderID    string    `json:"provider_id"`
	Model         string    `json:"model"`
	TokensIn      int64     `json:"tokens_in"`
	TokensOut     int64     `json:"tokens_out"`
	CostUSDC      float64   `json:"cost_usdc"`
	LatencyMS     int64     `json:"latency_ms"`
	Status        string    `json:"status"`
	PaymentMethod string    `json:"payment_method"`
	CreatedAt     time.Time `json:"created_at"`
}

type UsageListFilter struct {
	ConsumerID string
	ProviderID string
	Limit      int
}

type APIKeyPrincipal struct {
	ConsumerID    string
	ConsumerType  string
	WalletAddress string
}

type PrepaidReserveResult struct {
	Approved     bool
	ReservedUSDC float64
	BalanceAfter float64
}

type TelemetryUsageBatchRequest struct {
	GatewayID     string       `json:"gateway_id"`
	GatewayPubKey string       `json:"gateway_pubkey"`
	BatchID       string       `json:"batch_id"`
	SentAt        string       `json:"sent_at"`
	Signature     string       `json:"signature"`
	Events        []UsageEvent `json:"events"`
}

type telemetrySignedPayload struct {
	GatewayID     string       `json:"gateway_id"`
	GatewayPubKey string       `json:"gateway_pubkey"`
	BatchID       string       `json:"batch_id"`
	SentAt        string       `json:"sent_at"`
	Events        []UsageEvent `json:"events"`
}

const telemetryMaxSkew = 10 * time.Minute

type ControlStore interface {
	UpsertProvider(context.Context, ProviderRegisterRequest) error
	HeartbeatProvider(context.Context, ProviderHeartbeatRequest) error
	RotateProviderWallet(context.Context, ProviderWalletRotateRequest) error
	RecordTelemetryBatch(context.Context, string, string, string, int) (bool, error)
	InsertUsageEvents(context.Context, []UsageEvent) (int, error)
	ListUsageEvents(context.Context, UsageListFilter) ([]UsageEvent, error)
	UpsertConsumerAPIKey(context.Context, string, string, string, string) error
	RevokeConsumerAPIKey(context.Context, string, string, string) (bool, error)
	LookupActiveAPIKey(context.Context, string) (*APIKeyPrincipal, error)
	LookupConsumerAPIKeyHash(context.Context, string) (string, error)
	ReservePrepaidBalance(context.Context, string, string, float64) (PrepaidReserveResult, error)
	FinalizePrepaidCharge(context.Context, string, string, float64, bool) (float64, error)
	CreditConsumerBalance(context.Context, string, float64, string) error
	CurrentConsumerBalance(context.Context, string) (float64, error)
	RecordX402PrepaidTopup(context.Context, string, string, float64) (bool, float64, error)
}

func (p *OpenAIProxy) handleProviderRegister(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req ProviderRegisterRequest
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.ProviderID = strings.TrimSpace(req.ProviderID)
	req.PeerID = strings.TrimSpace(req.PeerID)
	req.Status = strings.TrimSpace(req.Status)
	req.WalletAddress = strings.TrimSpace(req.WalletAddress)
	if req.ProviderID == "" || req.PeerID == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "provider_id and peer_id are required"))
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	if err := store.UpsertProvider(r.Context(), req); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "provider register failed"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"provider_id": req.ProviderID,
	})
}

func (p *OpenAIProxy) handleProviderHeartbeat(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req ProviderHeartbeatRequest
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.ProviderID = strings.TrimSpace(req.ProviderID)
	req.PeerID = strings.TrimSpace(req.PeerID)
	req.Status = strings.TrimSpace(req.Status)
	if req.ProviderID == "" || req.PeerID == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "provider_id and peer_id are required"))
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}
	if err := store.HeartbeatProvider(r.Context(), req); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "provider heartbeat failed"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"provider_id": req.ProviderID,
	})
}

func (p *OpenAIProxy) handleProviderWalletRotate(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req ProviderWalletRotateRequest
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.ProviderID = strings.TrimSpace(req.ProviderID)
	req.NewWallet = strings.TrimSpace(req.NewWallet)
	req.RotationProof = strings.TrimSpace(req.RotationProof)
	req.RequestedByPeer = strings.TrimSpace(req.RequestedByPeer)
	if req.ProviderID == "" || req.NewWallet == "" || req.RotationProof == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "provider_id, new_wallet, and rotation_proof are required"))
		return
	}
	if err := store.RotateProviderWallet(r.Context(), req); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "wallet rotation failed"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"provider_id": req.ProviderID,
		"wallet":      req.NewWallet,
	})
}

func (p *OpenAIProxy) handleTelemetryUsage(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req TelemetryUsageBatchRequest
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.GatewayID = strings.TrimSpace(req.GatewayID)
	req.GatewayPubKey = strings.TrimSpace(req.GatewayPubKey)
	req.BatchID = strings.TrimSpace(req.BatchID)
	req.SentAt = strings.TrimSpace(req.SentAt)
	req.Signature = strings.TrimSpace(req.Signature)
	if req.GatewayID == "" || req.GatewayPubKey == "" || req.BatchID == "" || req.SentAt == "" || req.Signature == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "gateway_id, gateway_pubkey, batch_id, sent_at, and signature are required"))
		return
	}
	if len(req.Events) == 0 {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "events must not be empty"))
		return
	}
	sentAt, err := time.Parse(time.RFC3339, req.SentAt)
	if err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "sent_at must be RFC3339 timestamp"))
		return
	}
	if math.Abs(time.Since(sentAt).Seconds()) > telemetryMaxSkew.Seconds() {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "telemetry sent_at outside allowed time window"))
		return
	}
	if err := verifyTelemetrySignature(req); err != nil {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "telemetry signature verification failed: "+err.Error()))
		return
	}
	if p.batchContainsOfficialEvent(req.Events) && !p.isAuthorizedOfficialControl(r) {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "official gateway telemetry requires control token"))
		return
	}
	ok, err := store.RecordTelemetryBatch(r.Context(), req.GatewayID, req.BatchID, req.Signature, len(req.Events))
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "telemetry batch idempotency check failed"))
		return
	}
	if !ok {
		_ = writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"gateway_id":    req.GatewayID,
			"batch_id":      req.BatchID,
			"received":      len(req.Events),
			"inserted_rows": 0,
			"duplicate":     true,
		})
		return
	}
	for i := range req.Events {
		req.Events[i].GatewayID = req.GatewayID
		if req.Events[i].GatewayType == "" {
			req.Events[i].GatewayType = "community"
		}
		if req.Events[i].CreatedAt.IsZero() {
			req.Events[i].CreatedAt = time.Now().UTC()
		}
	}
	inserted, err := store.InsertUsageEvents(r.Context(), req.Events)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "telemetry usage persist failed"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"gateway_id":    req.GatewayID,
		"batch_id":      req.BatchID,
		"received":      len(req.Events),
		"inserted_rows": inserted,
		"duplicate":     false,
	})
}

func (p *OpenAIProxy) handleAPIKeysCreate(w http.ResponseWriter, r *http.Request) {
	if !p.isAuthorizedOfficialControl(r) {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "official control token required"))
		return
	}
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req struct {
		ConsumerID    string `json:"consumer_id"`
		WalletAddress string `json:"wallet_address"`
		ConsumerType  string `json:"consumer_type"`
	}
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.ConsumerID = strings.TrimSpace(req.ConsumerID)
	req.WalletAddress = strings.TrimSpace(req.WalletAddress)
	req.ConsumerType = strings.TrimSpace(req.ConsumerType)
	if req.ConsumerID == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "consumer_id is required"))
		return
	}
	if req.ConsumerType == "" {
		req.ConsumerType = "prepaid"
	}
	if req.ConsumerType != "prepaid" && req.ConsumerType != "x402" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "consumer_type must be prepaid or x402"))
		return
	}
	apiKey, err := generateAPIKey()
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to generate api key"))
		return
	}
	hash := hashAPIKey(apiKey)
	if err := store.UpsertConsumerAPIKey(r.Context(), req.ConsumerID, req.WalletAddress, req.ConsumerType, hash); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to persist api key"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"consumer_id": req.ConsumerID,
		"api_key":     apiKey,
	})
}

func (p *OpenAIProxy) handleAPIKeysRevoke(w http.ResponseWriter, r *http.Request) {
	if !p.isAuthorizedOfficialControl(r) {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "official control token required"))
		return
	}
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req struct {
		ConsumerID string `json:"consumer_id"`
		APIKey     string `json:"api_key"`
		APIKeyHash string `json:"api_key_hash"`
		Reason     string `json:"reason"`
	}
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.ConsumerID = strings.TrimSpace(req.ConsumerID)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.APIKeyHash = strings.TrimSpace(req.APIKeyHash)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.ConsumerID == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "consumer_id is required"))
		return
	}
	hash := req.APIKeyHash
	if hash == "" && req.APIKey != "" {
		hash = hashAPIKey(req.APIKey)
	}
	if hash == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "api_key or api_key_hash is required"))
		return
	}
	revoked, err := store.RevokeConsumerAPIKey(r.Context(), req.ConsumerID, hash, req.Reason)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to revoke api key"))
		return
	}
	if !revoked {
		_ = writeJSON(w, http.StatusNotFound, openAIError(http.StatusNotFound, "active api key not found"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"consumer_id":  req.ConsumerID,
		"api_key_hash": hash,
		"revoked":      true,
	})
}

func (p *OpenAIProxy) handleAPIKeysRotate(w http.ResponseWriter, r *http.Request) {
	if !p.isAuthorizedOfficialControl(r) {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "official control token required"))
		return
	}
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req struct {
		ConsumerID    string `json:"consumer_id"`
		OldAPIKey     string `json:"old_api_key"`
		OldAPIKeyHash string `json:"old_api_key_hash"`
		WalletAddress string `json:"wallet_address"`
		ConsumerType  string `json:"consumer_type"`
	}
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.ConsumerID = strings.TrimSpace(req.ConsumerID)
	req.OldAPIKey = strings.TrimSpace(req.OldAPIKey)
	req.OldAPIKeyHash = strings.TrimSpace(req.OldAPIKeyHash)
	req.WalletAddress = strings.TrimSpace(req.WalletAddress)
	req.ConsumerType = strings.TrimSpace(req.ConsumerType)
	if req.ConsumerID == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "consumer_id is required"))
		return
	}
	if req.ConsumerType == "" {
		req.ConsumerType = "prepaid"
	}
	if req.ConsumerType != "prepaid" && req.ConsumerType != "x402" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "consumer_type must be prepaid or x402"))
		return
	}
	oldHash := req.OldAPIKeyHash
	if oldHash == "" && req.OldAPIKey != "" {
		oldHash = hashAPIKey(req.OldAPIKey)
	}
	if oldHash == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "old_api_key or old_api_key_hash is required"))
		return
	}
	revoked, err := store.RevokeConsumerAPIKey(r.Context(), req.ConsumerID, oldHash, "rotated")
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to revoke old api key"))
		return
	}
	if !revoked {
		_ = writeJSON(w, http.StatusNotFound, openAIError(http.StatusNotFound, "active old api key not found"))
		return
	}
	newAPIKey, err := generateAPIKey()
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to generate new api key"))
		return
	}
	newHash := hashAPIKey(newAPIKey)
	if err := store.UpsertConsumerAPIKey(r.Context(), req.ConsumerID, req.WalletAddress, req.ConsumerType, newHash); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to persist new api key"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"consumer_id":      req.ConsumerID,
		"old_api_key_hash": oldHash,
		"new_api_key":      newAPIKey,
	})
}

func (p *OpenAIProxy) handlePrepaidDepositConfirm(w http.ResponseWriter, r *http.Request) {
	if !p.isAuthorizedOfficialControl(r) {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "official control token required"))
		return
	}
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req struct {
		ConsumerID string  `json:"consumer_id"`
		AmountUSDC float64 `json:"amount_usdc"`
		Reference  string  `json:"reference"`
		TxHash     string  `json:"tx_hash"`
	}
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	req.ConsumerID = strings.TrimSpace(req.ConsumerID)
	req.Reference = strings.TrimSpace(req.Reference)
	req.TxHash = strings.TrimSpace(req.TxHash)
	if req.ConsumerID == "" {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "consumer_id is required"))
		return
	}
	if req.AmountUSDC <= 0 {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "amount_usdc must be > 0"))
		return
	}
	reference := req.Reference
	if reference == "" {
		reference = "deposit"
	}
	if req.TxHash != "" {
		reference = reference + ":" + req.TxHash
	}
	if err := store.CreditConsumerBalance(r.Context(), req.ConsumerID, req.AmountUSDC, reference); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to credit prepaid balance"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"consumer_id": req.ConsumerID,
		"amount_usdc": req.AmountUSDC,
		"reference":   reference,
	})
}

func (p *OpenAIProxy) handlePrepaidTopup(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	var req struct {
		AmountUSDC float64 `json:"amount_usdc"`
	}
	if err := decodeJSONBody(r.Body, &req); err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	if req.AmountUSDC <= 0 {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "amount_usdc must be > 0"))
		return
	}
	if p.prepaidTopupPaywall == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, openAIError(http.StatusServiceUnavailable, "x402 topup unavailable"))
		return
	}
	requirement := p.prepaidTopupPaywall.Requirement
	atomicAmount, err := usdcAmountToAtomicString(req.AmountUSDC)
	if err != nil {
		_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, err.Error()))
		return
	}
	requirement.Amount = atomicAmount
	paymentRequired := x402spike.PaymentRequired{
		X402Version: 2,
		Error:       "PAYMENT-SIGNATURE header is required",
		Resource: x402spike.ResourceInfo{
			URL:         requestURL(r),
			Description: "prepaid topup request",
			MimeType:    "application/json",
		},
		Accepts:    []x402spike.PaymentRequirements{requirement},
		Extensions: map[string]any{"intent": "prepaid_topup"},
	}
	paymentHeader := strings.TrimSpace(r.Header.Get("PAYMENT-SIGNATURE"))
	if paymentHeader == "" {
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return
	}
	var payload x402spike.PaymentPayload
	if err := x402spike.DecodeBase64JSON(paymentHeader, &payload); err != nil {
		paymentRequired.Error = "invalid PAYMENT-SIGNATURE header"
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return
	}
	if err := validateAcceptedPayment(payload.Accepted, requirement); err != nil {
		paymentRequired.Error = err.Error()
		writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
		return
	}
	settle := x402spike.SettlementResponse{
		Success:     true,
		Payer:       payload.Payload.Authorization.From,
		Transaction: "x402-topup-local",
		Network:     payload.Accepted.Network,
	}
	if strings.TrimSpace(p.prepaidTopupPaywall.FacilitatorURL) != "" {
		settled, _, _, err := settleWithFacilitator(p.prepaidTopupPaywall.FacilitatorURL, payload, requirement)
		if err != nil {
			paymentRequired.Error = "facilitator error: " + err.Error()
			writePaymentRequired(w, paymentRequired, x402spike.SettlementResponse{})
			return
		}
		if !settled.Success {
			reason := strings.TrimSpace(settled.ErrorReason)
			if reason == "" {
				reason = "payment settlement failed"
			}
			paymentRequired.Error = reason
			writePaymentRequired(w, paymentRequired, settled)
			return
		}
		settle = settled
	}
	if header, err := x402spike.EncodeBase64JSON(settle); err == nil {
		w.Header().Set("PAYMENT-RESPONSE", header)
	}
	consumerID := consumerIDFromWallet(payload.Payload.Authorization.From)
	fingerprint := hashAPIKey(paymentHeader)
	inserted, balance, err := store.RecordX402PrepaidTopup(r.Context(), consumerID, fingerprint, req.AmountUSDC)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to record prepaid topup"))
		return
	}

	existingHash, err := store.LookupConsumerAPIKeyHash(r.Context(), consumerID)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to read consumer api key state"))
		return
	}
	var newAPIKey string
	if strings.TrimSpace(existingHash) == "" {
		newAPIKey, err = generateAPIKey()
		if err != nil {
			_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to generate api key"))
			return
		}
		if err := store.UpsertConsumerAPIKey(r.Context(), consumerID, payload.Payload.Authorization.From, "prepaid", hashAPIKey(newAPIKey)); err != nil {
			_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to persist api key"))
			return
		}
	}
	resp := map[string]any{
		"ok":           true,
		"consumer_id":  consumerID,
		"wallet":       payload.Payload.Authorization.From,
		"amount_usdc":  req.AmountUSDC,
		"balance_usdc": balance,
		"payment_ref":  fingerprint,
		"idempotent":   !inserted,
	}
	if newAPIKey != "" {
		resp["api_key"] = newAPIKey
		resp["api_key_created"] = true
	} else {
		resp["api_key_created"] = false
	}
	_ = writeJSON(w, http.StatusOK, resp)
}

func (p *OpenAIProxy) handlePrepaidBalance(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	principal, _, ok := p.requireStrictAPIKey(w, r)
	if !ok {
		return
	}
	balance, err := store.CurrentConsumerBalance(r.Context(), principal.ConsumerID)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to read prepaid balance"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"consumer_id":   principal.ConsumerID,
		"consumer_type": principal.ConsumerType,
		"wallet":        principal.WalletAddress,
		"balance_usdc":  balance,
	})
}

func (p *OpenAIProxy) handlePrepaidRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	principal, oldHash, ok := p.requireStrictAPIKey(w, r)
	if !ok {
		return
	}
	revoked, err := store.RevokeConsumerAPIKey(r.Context(), principal.ConsumerID, oldHash, "self-rotated")
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to revoke api key"))
		return
	}
	if !revoked {
		_ = writeJSON(w, http.StatusNotFound, openAIError(http.StatusNotFound, "active api key not found"))
		return
	}
	newAPIKey, err := generateAPIKey()
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to generate api key"))
		return
	}
	if err := store.UpsertConsumerAPIKey(r.Context(), principal.ConsumerID, principal.WalletAddress, principal.ConsumerType, hashAPIKey(newAPIKey)); err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "failed to persist new api key"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"consumer_id": principal.ConsumerID,
		"new_api_key": newAPIKey,
	})
}

func (p *OpenAIProxy) handleUsageList(w http.ResponseWriter, r *http.Request) {
	store := p.requireControlStore(w)
	if store == nil {
		return
	}
	limit := 100
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, "limit must be a positive integer"))
			return
		}
		if n > 1000 {
			n = 1000
		}
		limit = n
	}
	events, err := store.ListUsageEvents(r.Context(), UsageListFilter{
		ConsumerID: strings.TrimSpace(r.URL.Query().Get("consumer_id")),
		ProviderID: strings.TrimSpace(r.URL.Query().Get("provider_id")),
		Limit:      limit,
	})
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "usage query failed"))
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   events,
	})
}

func (p *OpenAIProxy) requireControlStore(w http.ResponseWriter) ControlStore {
	if p.controlStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, openAIError(http.StatusServiceUnavailable, "control store not configured"))
		return nil
	}
	return p.controlStore
}

func (p *OpenAIProxy) requireStrictAPIKey(w http.ResponseWriter, r *http.Request) (*APIKeyPrincipal, string, bool) {
	if p.controlStore == nil {
		_ = writeJSON(w, http.StatusServiceUnavailable, openAIError(http.StatusServiceUnavailable, "api key validation unavailable"))
		return nil, "", false
	}
	raw := extractAPIKey(r)
	if strings.TrimSpace(raw) == "" {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "api key required"))
		return nil, "", false
	}
	hash := hashAPIKey(raw)
	principal, err := p.controlStore.LookupActiveAPIKey(r.Context(), hash)
	if err != nil {
		_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, "api key validation failed"))
		return nil, "", false
	}
	if principal == nil {
		_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, "invalid api key"))
		return nil, "", false
	}
	return principal, hash, true
}

func consumerIDFromWallet(wallet string) string {
	addr := strings.ToLower(strings.TrimSpace(wallet))
	return "wallet:" + addr
}

func usdcAmountToAtomicString(amountUSDC float64) (string, error) {
	if amountUSDC <= 0 {
		return "", fmt.Errorf("amount_usdc must be > 0")
	}
	atomic := int64(math.Round(amountUSDC * 1_000_000))
	if atomic <= 0 {
		return "", fmt.Errorf("amount_usdc too small")
	}
	return strconv.FormatInt(atomic, 10), nil
}

func decodeJSONBody(r io.Reader, out any) error {
	dec := json.NewDecoder(io.LimitReader(r, 4<<20))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid json body: %w", err)
	}
	return nil
}

func (p *OpenAIProxy) batchContainsOfficialEvent(events []UsageEvent) bool {
	for _, ev := range events {
		if strings.EqualFold(strings.TrimSpace(ev.GatewayType), "official") {
			return true
		}
	}
	return false
}

func (p *OpenAIProxy) isAuthorizedOfficialControl(r *http.Request) bool {
	token := strings.TrimSpace(p.controlAPIToken)
	if token == "" {
		return false
	}
	received := strings.TrimSpace(r.Header.Get("X-Tooti-Control-Token"))
	if strings.HasPrefix(strings.ToLower(received), "bearer ") {
		received = strings.TrimSpace(received[7:])
	}
	if received == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			received = strings.TrimSpace(auth[7:])
		}
	}
	if received == "" {
		return false
	}
	if len(received) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(received), []byte(token)) == 1
}

func generateAPIKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "tk_live_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashAPIKey(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return fmt.Sprintf("%x", sum[:])
}

func verifyTelemetrySignature(req TelemetryUsageBatchRequest) error {
	pub, err := decodeTelemetryPublicKey(req.GatewayPubKey)
	if err != nil {
		return err
	}
	sig, err := decodeTelemetrySignature(req.Signature)
	if err != nil {
		return err
	}
	msg, err := canonicalTelemetryMessage(req)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, msg, sig) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

func canonicalTelemetryMessage(req TelemetryUsageBatchRequest) ([]byte, error) {
	payload := telemetrySignedPayload{
		GatewayID:     req.GatewayID,
		GatewayPubKey: req.GatewayPubKey,
		BatchID:       req.BatchID,
		SentAt:        req.SentAt,
		Events:        req.Events,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return raw, nil
}

func decodeTelemetryPublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty gateway_pubkey")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil && len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	if raw, err := hex.DecodeString(s); err == nil && len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	return nil, fmt.Errorf("gateway_pubkey must decode to ed25519 32-byte key")
}

func decodeTelemetrySignature(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty signature")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(raw) == ed25519.SignatureSize {
		return raw, nil
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil && len(raw) == ed25519.SignatureSize {
		return raw, nil
	}
	if raw, err := hex.DecodeString(s); err == nil && len(raw) == ed25519.SignatureSize {
		return raw, nil
	}
	return nil, fmt.Errorf("signature must decode to ed25519 64-byte signature")
}
