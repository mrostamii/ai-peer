package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockControlStore struct {
	upsertProviderCalled    bool
	insertUsageEventsCalled bool
	recordBatchCalled       bool
	upsertConsumerCalled    bool
	revokeConsumerCalled    bool
	revokeConsumerReturn    bool
	lookupAPIKeyCalled      bool
	lookupAPIKeyResult      *APIKeyPrincipal
	reservePrepaidCalled    bool
	reservePrepaidResult    PrepaidReserveResult
	finalizePrepaidCalled   bool
	creditBalanceCalled     bool
	currentBalance          float64
	consumerAPIKeyHash      string
	recordPrepaidCalled     bool
	recordPrepaidInserted   bool
	usageEvents             []UsageEvent
}

func (m *mockControlStore) UpsertProvider(_ context.Context, _ ProviderRegisterRequest) error {
	m.upsertProviderCalled = true
	return nil
}

func (m *mockControlStore) HeartbeatProvider(_ context.Context, _ ProviderHeartbeatRequest) error {
	return nil
}

func (m *mockControlStore) RotateProviderWallet(_ context.Context, _ ProviderWalletRotateRequest) error {
	return nil
}

func (m *mockControlStore) RecordTelemetryBatch(_ context.Context, _ string, _ string, _ string, _ int) (bool, error) {
	m.recordBatchCalled = true
	return true, nil
}

func (m *mockControlStore) InsertUsageEvents(_ context.Context, events []UsageEvent) (int, error) {
	m.insertUsageEventsCalled = true
	m.usageEvents = append(m.usageEvents, events...)
	return len(events), nil
}

func (m *mockControlStore) ListUsageEvents(_ context.Context, _ UsageListFilter) ([]UsageEvent, error) {
	return m.usageEvents, nil
}

func (m *mockControlStore) UpsertConsumerAPIKey(_ context.Context, _ string, _ string, _ string, _ string) error {
	m.upsertConsumerCalled = true
	return nil
}

func (m *mockControlStore) RevokeConsumerAPIKey(_ context.Context, _ string, _ string, _ string) (bool, error) {
	m.revokeConsumerCalled = true
	return m.revokeConsumerReturn, nil
}

func (m *mockControlStore) LookupActiveAPIKey(_ context.Context, _ string) (*APIKeyPrincipal, error) {
	m.lookupAPIKeyCalled = true
	return m.lookupAPIKeyResult, nil
}

func (m *mockControlStore) LookupConsumerAPIKeyHash(_ context.Context, _ string) (string, error) {
	return m.consumerAPIKeyHash, nil
}

func (m *mockControlStore) ReservePrepaidBalance(_ context.Context, _ string, _ string, _ float64) (PrepaidReserveResult, error) {
	m.reservePrepaidCalled = true
	return m.reservePrepaidResult, nil
}

func (m *mockControlStore) FinalizePrepaidCharge(_ context.Context, _ string, _ string, _ float64, _ bool) (float64, error) {
	m.finalizePrepaidCalled = true
	return 0, nil
}

func (m *mockControlStore) CreditConsumerBalance(_ context.Context, _ string, _ float64, _ string) error {
	m.creditBalanceCalled = true
	return nil
}

func (m *mockControlStore) CurrentConsumerBalance(_ context.Context, _ string) (float64, error) {
	return m.currentBalance, nil
}

func (m *mockControlStore) RecordX402PrepaidTopup(_ context.Context, _ string, _ string, _ float64) (bool, float64, error) {
	m.recordPrepaidCalled = true
	return m.recordPrepaidInserted, m.currentBalance, nil
}

func TestHandleProviderRegisterRequiresControlStore(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/provider/register", strings.NewReader(`{"provider_id":"p1","peer_id":"peer1"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleProviderRegister(rr, req)
	if rr.Result().StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusServiceUnavailable)
	}
}

func TestHandleProviderRegisterSuccess(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{}
	p.SetControlStore(store)
	req := httptest.NewRequest(http.MethodPost, "/v1/provider/register", strings.NewReader(`{
		"provider_id":"provider-1",
		"peer_id":"12D3KooXYZ",
		"wallet_address":"0xabc"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleProviderRegister(rr, req)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !store.upsertProviderCalled {
		t.Fatal("expected UpsertProvider to be called")
	}
}

func TestHandleTelemetryUsageRequiresSignature(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{}
	p.SetControlStore(store)
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry/usage", strings.NewReader(`{
		"gateway_id":"gw-1",
		"gateway_pubkey":"pub-1",
		"batch_id":"batch-1",
		"sent_at":"2026-01-01T00:00:00Z",
		"events":[{"request_id":"req-1","model":"llama","status":"ok","payment_method":"x402"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleTelemetryUsage(rr, req)
	if rr.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusBadRequest)
	}
	if store.insertUsageEventsCalled {
		t.Fatal("expected InsertUsageEvents not to be called")
	}
}

func TestHandleTelemetryUsageSuccess(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{}
	p.SetControlStore(store)
	raw := signedTelemetryJSON(t, "gw-1", "batch-1", time.Now().UTC(), []map[string]any{
		{"request_id": "req-1", "model": "llama", "status": "ok", "payment_method": "x402"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry/usage", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleTelemetryUsage(rr, req)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !store.insertUsageEventsCalled {
		t.Fatal("expected InsertUsageEvents to be called")
	}
	if !store.recordBatchCalled {
		t.Fatal("expected RecordTelemetryBatch to be called")
	}
	if len(store.usageEvents) != 1 {
		t.Fatalf("usage events=%d want 1", len(store.usageEvents))
	}
	if store.usageEvents[0].GatewayID != "gw-1" {
		t.Fatalf("gateway id not propagated: %q", store.usageEvents[0].GatewayID)
	}
}

func TestHandleTelemetryUsageRejectsOldTimestamp(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{}
	p.SetControlStore(store)
	raw := signedTelemetryJSON(t, "gw-1", "batch-old", time.Now().UTC().Add(-30*time.Minute), []map[string]any{
		{"request_id": "req-1", "model": "llama", "status": "ok", "payment_method": "x402"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry/usage", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleTelemetryUsage(rr, req)
	if rr.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusUnauthorized)
	}
}

func TestHandleTelemetryUsageRejectsBadSignature(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{}
	p.SetControlStore(store)
	raw := signedTelemetryJSON(t, "gw-1", "batch-bad", time.Now().UTC(), []map[string]any{
		{"request_id": "req-1", "model": "llama", "status": "ok", "payment_method": "x402"},
	})
	raw = strings.Replace(raw, `"signature":"`, `"signature":"broken`, 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry/usage", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleTelemetryUsage(rr, req)
	if rr.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusUnauthorized)
	}
}

func TestHandleAPIKeysCreateRequiresOfficialToken(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	p.SetControlStore(&mockControlStore{})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/api-keys", strings.NewReader(`{"consumer_id":"c1"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.handleAPIKeysCreate(rr, req)
	if rr.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusUnauthorized)
	}
}

func TestHandleAPIKeysCreateSuccess(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{}
	p.SetControlStore(store)
	p.SetControlAPIToken("secret-token")
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/api-keys", strings.NewReader(`{"consumer_id":"c1","consumer_type":"prepaid"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tooti-Control-Token", "secret-token")
	rr := httptest.NewRecorder()
	p.handleAPIKeysCreate(rr, req)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !store.upsertConsumerCalled {
		t.Fatal("expected UpsertConsumerAPIKey to be called")
	}
}

func TestHandlePrepaidDepositConfirmSuccess(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{}
	p.SetControlStore(store)
	p.SetControlAPIToken("secret-token")
	req := httptest.NewRequest(http.MethodPost, "/v1/prepaid/deposits/confirm", strings.NewReader(`{
		"consumer_id":"c1",
		"amount_usdc":5,
		"reference":"deposit",
		"tx_hash":"0xabc"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")
	rr := httptest.NewRecorder()
	p.handlePrepaidDepositConfirm(rr, req)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !store.creditBalanceCalled {
		t.Fatal("expected CreditConsumerBalance to be called")
	}
}

func TestHandleAPIKeysRevokeSuccess(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{revokeConsumerReturn: true}
	p.SetControlStore(store)
	p.SetControlAPIToken("secret-token")
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/api-keys/revoke", strings.NewReader(`{
		"consumer_id":"c1",
		"api_key":"tk_live_x",
		"reason":"manual"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")
	rr := httptest.NewRecorder()
	p.handleAPIKeysRevoke(rr, req)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !store.revokeConsumerCalled {
		t.Fatal("expected RevokeConsumerAPIKey to be called")
	}
}

func TestHandleAPIKeysRotateSuccess(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProxy("127.0.0.1:0", "http://unused", nil)
	store := &mockControlStore{revokeConsumerReturn: true}
	p.SetControlStore(store)
	p.SetControlAPIToken("secret-token")
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/api-keys/rotate", strings.NewReader(`{
		"consumer_id":"c1",
		"old_api_key":"tk_live_old",
		"consumer_type":"prepaid"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tooti-Control-Token", "secret-token")
	rr := httptest.NewRecorder()
	p.handleAPIKeysRotate(rr, req)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Result().StatusCode, http.StatusOK)
	}
	if !store.revokeConsumerCalled {
		t.Fatal("expected RevokeConsumerAPIKey to be called")
	}
	if !store.upsertConsumerCalled {
		t.Fatal("expected UpsertConsumerAPIKey to be called for new key")
	}
}

func signedTelemetryJSON(t *testing.T, gatewayID, batchID string, sentAt time.Time, events []map[string]any) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rawEvents, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("Marshal events: %v", err)
	}
	var typedEvents []UsageEvent
	if err := json.Unmarshal(rawEvents, &typedEvents); err != nil {
		t.Fatalf("Unmarshal events: %v", err)
	}
	req := TelemetryUsageBatchRequest{
		GatewayID:     gatewayID,
		GatewayPubKey: base64.RawURLEncoding.EncodeToString(pub),
		BatchID:       batchID,
		SentAt:        sentAt.Format(time.RFC3339),
		Events:        typedEvents,
	}
	msg, err := canonicalTelemetryMessage(req)
	if err != nil {
		t.Fatalf("canonicalTelemetryMessage: %v", err)
	}
	sig := ed25519.Sign(priv, msg)
	req.Signature = base64.RawURLEncoding.EncodeToString(sig)
	final, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal final payload: %v", err)
	}
	return string(final)
}
