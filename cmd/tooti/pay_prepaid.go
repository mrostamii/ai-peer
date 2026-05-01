package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mrostamii/tooti/pkg/x402client"
)

type payState struct {
	Profiles map[string]payProfile `json:"profiles"`
}

type payProfile struct {
	GatewayURL string `json:"gateway_url"`
	ConsumerID string `json:"consumer_id"`
	Wallet     string `json:"wallet"`
	APIKey     string `json:"api_key"`
	UpdatedAt  string `json:"updated_at"`
}

func parsePayAmountShortcut(raw string) (float64, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimSuffix(s, "usdc")
	s = strings.TrimSuffix(s, "usd")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// prepaidAPIKeyForChatURL returns explicitKey if set, otherwise the API key saved in pay-state
// for the gateway host derived from chatURL (same profile key as pay topup/balance).
func prepaidAPIKeyForChatURL(chatURL, explicitKey string) string {
	if k := strings.TrimSpace(explicitKey); k != "" {
		return k
	}
	u, err := url.Parse(strings.TrimSpace(chatURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	base := (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	state, err := loadPayState()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(state.Profiles[normalizeGatewayURL(base)].APIKey)
}

func runPayTopup(args []string) {
	fs := flag.NewFlagSet("pay topup", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "http://127.0.0.1:8080", "official gateway base URL")
	amountUSDC := fs.Float64("amount-usdc", 0, "USDC amount to top up")
	privateKey := fs.String("private-key", "", "EVM private key (defaults to EVM_PRIVATE_KEY)")
	_ = fs.Parse(args)

	if *amountUSDC <= 0 {
		log.Fatalf("amount-usdc must be > 0")
	}
	client, err := x402client.NewFromEnv()
	if err != nil {
		log.Fatalf("wallet error: %v", err)
	}
	if strings.TrimSpace(*privateKey) != "" {
		client.PrivateKey = strings.TrimSpace(*privateKey)
	}

	body := map[string]any{"amount_usdc": *amountUSDC}
	raw, err := json.Marshal(body)
	if err != nil {
		log.Fatalf("request marshal error: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, normalizeGatewayURL(*gatewayURL)+"/v1/prepaid/topup", bytes.NewReader(raw))
	if err != nil {
		log.Fatalf("request create error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.DoWithPayment(req)
	if err != nil {
		log.Fatalf("topup request failed: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		log.Fatalf("status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var out struct {
		ConsumerID  string  `json:"consumer_id"`
		Wallet      string  `json:"wallet"`
		AmountUSDC  float64 `json:"amount_usdc"`
		BalanceUSDC float64 `json:"balance_usdc"`
		APIKey      string  `json:"api_key"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		log.Fatalf("decode topup response: %v body=%s", err, string(respBody))
	}
	state, _ := loadPayState()
	gatewayKey := normalizeGatewayURL(*gatewayURL)
	profile := state.Profiles[gatewayKey]
	profile.GatewayURL = gatewayKey
	profile.ConsumerID = strings.TrimSpace(out.ConsumerID)
	profile.Wallet = strings.TrimSpace(out.Wallet)
	profile.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(out.APIKey) != "" {
		profile.APIKey = strings.TrimSpace(out.APIKey)
	}
	state.Profiles[gatewayKey] = profile
	if err := savePayState(state); err != nil {
		log.Printf("warning: failed to persist pay profile: %v", err)
	}
	fmt.Printf("consumer_id=%s\n", out.ConsumerID)
	fmt.Printf("wallet=%s\n", out.Wallet)
	fmt.Printf("credited_usdc=%.6f\n", out.AmountUSDC)
	fmt.Printf("balance_usdc=%.6f\n", out.BalanceUSDC)
	if strings.TrimSpace(profile.APIKey) != "" {
		fmt.Printf("api_key=%s\n", profile.APIKey)
	}
}

func runPayBalance(args []string) {
	fs := flag.NewFlagSet("pay balance", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "http://127.0.0.1:8080", "official gateway base URL")
	apiKey := fs.String("api-key", "", "prepaid API key (defaults to local saved profile)")
	_ = fs.Parse(args)

	key := strings.TrimSpace(*apiKey)
	if key == "" {
		state, _ := loadPayState()
		key = strings.TrimSpace(state.Profiles[normalizeGatewayURL(*gatewayURL)].APIKey)
	}
	if key == "" {
		log.Fatalf("api key is required (set -api-key or run pay topup first)")
	}
	respBody, err := doJSON(http.MethodGet, normalizeGatewayURL(*gatewayURL)+"/v1/prepaid/balance", key, nil)
	if err != nil {
		log.Fatalf("prepaid balance request failed: %v", err)
	}
	var resp struct {
		ConsumerID  string  `json:"consumer_id"`
		Wallet      string  `json:"wallet"`
		BalanceUSDC float64 `json:"balance_usdc"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Fatalf("decode prepaid balance response: %v", err)
	}
	fmt.Printf("consumer_id=%s\n", resp.ConsumerID)
	fmt.Printf("wallet=%s\n", resp.Wallet)
	fmt.Printf("balance_usdc=%.6f\n", resp.BalanceUSDC)
}

func runPayRotateKey(args []string) {
	fs := flag.NewFlagSet("pay rotate-key", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "http://127.0.0.1:8080", "official gateway base URL")
	apiKey := fs.String("api-key", "", "existing prepaid API key (defaults to local saved profile)")
	_ = fs.Parse(args)

	gatewayKey := normalizeGatewayURL(*gatewayURL)
	state, _ := loadPayState()
	key := strings.TrimSpace(*apiKey)
	if key == "" {
		key = strings.TrimSpace(state.Profiles[gatewayKey].APIKey)
	}
	if key == "" {
		log.Fatalf("api key is required (set -api-key or run pay topup first)")
	}
	respBody, err := doJSON(http.MethodPost, gatewayKey+"/v1/prepaid/api-keys/rotate", key, map[string]any{})
	if err != nil {
		log.Fatalf("rotate key request failed: %v", err)
	}
	var resp struct {
		ConsumerID string `json:"consumer_id"`
		NewAPIKey  string `json:"new_api_key"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Fatalf("decode rotate key response: %v", err)
	}
	if strings.TrimSpace(resp.NewAPIKey) == "" {
		log.Fatalf("rotate key response missing new_api_key")
	}
	profile := state.Profiles[gatewayKey]
	profile.GatewayURL = gatewayKey
	profile.ConsumerID = strings.TrimSpace(resp.ConsumerID)
	profile.APIKey = strings.TrimSpace(resp.NewAPIKey)
	profile.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	state.Profiles[gatewayKey] = profile
	if err := savePayState(state); err != nil {
		log.Printf("warning: failed to persist pay profile: %v", err)
	}
	fmt.Printf("consumer_id=%s\n", profile.ConsumerID)
	fmt.Printf("new_api_key=%s\n", profile.APIKey)
}

func normalizeGatewayURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "http://127.0.0.1:8080"
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(s, "/")
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

func doJSON(method, urlStr, apiKey string, body any) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, urlStr, payload)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respRaw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, string(respRaw))
	}
	return respRaw, nil
}

func loadPayState() (payState, error) {
	state := payState{Profiles: map[string]payProfile{}}
	p := payStatePath()
	raw, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return state, err
	}
	if len(raw) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.Profiles == nil {
		state.Profiles = map[string]payProfile{}
	}
	return state, nil
}

func savePayState(state payState) error {
	if state.Profiles == nil {
		state.Profiles = map[string]payProfile{}
	}
	p := payStatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

func payStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ".tooti-pay-state.json"
	}
	return filepath.Join(home, ".tooti", "pay-state.json")
}
