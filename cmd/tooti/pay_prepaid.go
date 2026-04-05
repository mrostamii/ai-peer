package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
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

func runPayTopup(args []string) {
	fs := flag.NewFlagSet("pay topup", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "http://127.0.0.1:8080", "official gateway base URL")
	amountUSDC := fs.Float64("amount-usdc", 0, "USDC amount to top up")
	network := fs.String("network", "eip155:84532", "network in CAIP-2 format")
	rpcURL := fs.String("rpc-url", "", "RPC URL for transaction broadcast")
	token := fs.String("token", "0x036CbD53842c5426634e7929541eC2318f3dCF7e", "USDC token contract address")
	receiver := fs.String("receiver", "", "official gateway receiver wallet")
	privateKey := fs.String("private-key", "", "EVM private key (defaults to EVM_PRIVATE_KEY)")
	confirmations := fs.Int64("confirmations", 1, "required confirmations before reporting tx as settled")
	waitTimeout := fs.Duration("wait-timeout", 4*time.Minute, "max wait time for tx confirmations")
	_ = fs.Parse(args)

	if *amountUSDC <= 0 {
		log.Fatalf("amount-usdc must be > 0")
	}
	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatalf("rpc-url is required")
	}
	if strings.TrimSpace(*receiver) == "" {
		log.Fatalf("receiver is required")
	}
	pk := strings.TrimSpace(*privateKey)
	if pk == "" {
		pk = strings.TrimSpace(os.Getenv("EVM_PRIVATE_KEY"))
	}
	if pk == "" {
		log.Fatalf("private key is required (set -private-key or EVM_PRIVATE_KEY)")
	}
	atomic, err := usdcAtomicFromFloat(*amountUSDC)
	if err != nil {
		log.Fatalf("invalid amount-usdc: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *waitTimeout)
	defer cancel()
	txHash, fromWallet, err := sendERC20Transfer(ctx, strings.TrimSpace(*rpcURL), pk, strings.TrimSpace(*token), strings.TrimSpace(*receiver), atomic)
	if err != nil {
		log.Fatalf("broadcast transfer failed: %v", err)
	}
	if err := waitForTxConfirmations(ctx, strings.TrimSpace(*rpcURL), txHash, *confirmations); err != nil {
		log.Fatalf("confirmation wait failed for tx %s: %v", txHash, err)
	}

	body := map[string]any{
		"tx_hash":        txHash,
		"network":        strings.TrimSpace(*network),
		"wallet_address": fromWallet,
	}
	respBody, err := doJSON(http.MethodPost, normalizeGatewayURL(*gatewayURL)+"/v1/prepaid/pay", "", body)
	if err != nil {
		log.Fatalf("prepaid pay request failed: %v", err)
	}
	var resp struct {
		ConsumerID  string  `json:"consumer_id"`
		Wallet      string  `json:"wallet"`
		AmountUSDC  float64 `json:"amount_usdc"`
		BalanceUSDC float64 `json:"balance_usdc"`
		TxHash      string  `json:"tx_hash"`
		APIKey      string  `json:"api_key"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		log.Fatalf("decode prepaid pay response: %v", err)
	}
	state, _ := loadPayState()
	gatewayKey := normalizeGatewayURL(*gatewayURL)
	profile := state.Profiles[gatewayKey]
	profile.GatewayURL = gatewayKey
	profile.ConsumerID = strings.TrimSpace(resp.ConsumerID)
	profile.Wallet = strings.TrimSpace(resp.Wallet)
	profile.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(resp.APIKey) != "" {
		profile.APIKey = strings.TrimSpace(resp.APIKey)
	}
	state.Profiles[gatewayKey] = profile
	if err := savePayState(state); err != nil {
		log.Printf("warning: failed to persist pay profile: %v", err)
	}

	fmt.Printf("tx_hash=%s\n", resp.TxHash)
	fmt.Printf("consumer_id=%s\n", resp.ConsumerID)
	fmt.Printf("wallet=%s\n", resp.Wallet)
	fmt.Printf("credited_usdc=%.6f\n", resp.AmountUSDC)
	fmt.Printf("balance_usdc=%.6f\n", resp.BalanceUSDC)
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

func usdcAtomicFromFloat(amount float64) (*big.Int, error) {
	if amount <= 0 {
		return nil, errors.New("amount must be > 0")
	}
	atomic := int64(math.Round(amount * 1_000_000))
	if atomic <= 0 {
		return nil, errors.New("amount too small")
	}
	return big.NewInt(atomic), nil
}

func sendERC20Transfer(ctx context.Context, rpcURL, privateKeyHex, tokenAddr, receiver string, amountAtomic *big.Int) (string, string, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return "", "", fmt.Errorf("dial rpc: %w", err)
	}
	defer client.Close()

	keyHex := strings.TrimPrefix(strings.TrimSpace(privateKeyHex), "0x")
	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return "", "", fmt.Errorf("decode private key: %w", err)
	}
	fromAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	token := common.HexToAddress(strings.TrimSpace(tokenAddr))
	toAddr := common.HexToAddress(strings.TrimSpace(receiver))

	data := encodeERC20TransferData(toAddr, amountAtomic)
	nonce, err := client.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		return "", "", fmt.Errorf("load nonce: %w", err)
	}
	chainID, err := client.NetworkID(ctx)
	if err != nil {
		return "", "", fmt.Errorf("load chain id: %w", err)
	}
	tipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		tipCap = big.NewInt(1_000_000_000)
	}
	head, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("load latest head: %w", err)
	}
	baseFee := big.NewInt(0)
	if head.BaseFee != nil {
		baseFee = head.BaseFee
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tipCap)
	if feeCap.Cmp(tipCap) < 0 {
		feeCap = new(big.Int).Set(tipCap)
	}
	call := ethereum.CallMsg{From: fromAddr, To: &token, Data: data}
	gasLimit, err := client.EstimateGas(ctx, call)
	if err != nil {
		gasLimit = 100_000
	}
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &token,
		Value:     big.NewInt(0),
		Data:      data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), privKey)
	if err != nil {
		return "", "", fmt.Errorf("sign tx: %w", err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return "", "", fmt.Errorf("send tx: %w", err)
	}
	return signed.Hash().Hex(), strings.ToLower(fromAddr.Hex()), nil
}

func waitForTxConfirmations(ctx context.Context, rpcURL, txHash string, confirmations int64) error {
	if confirmations <= 0 {
		confirmations = 1
	}
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return fmt.Errorf("dial rpc: %w", err)
	}
	defer client.Close()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	hash := common.HexToHash(strings.TrimSpace(txHash))
	for {
		receipt, err := client.TransactionReceipt(ctx, hash)
		if err == nil && receipt != nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				return fmt.Errorf("transaction reverted")
			}
			head, err := client.BlockNumber(ctx)
			if err != nil {
				return fmt.Errorf("fetch latest block: %w", err)
			}
			if head+1 >= receipt.BlockNumber.Uint64()+uint64(confirmations) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func encodeERC20TransferData(to common.Address, amount *big.Int) []byte {
	methodID := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]
	paddedTo := common.LeftPadBytes(to.Bytes(), 32)
	paddedAmount := common.LeftPadBytes(amount.Bytes(), 32)
	var out []byte
	out = append(out, methodID...)
	out = append(out, paddedTo...)
	out = append(out, paddedAmount...)
	return out
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
