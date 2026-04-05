package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

const (
	erc20TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55aeb"
)

type PrepaidOnchainConfig struct {
	Network         string
	RPCURL          string
	TokenAddress    string
	ReceiverAddress string
	Confirmations   int64
}

type VerifiedPrepaidDeposit struct {
	TxHash       string
	Network      string
	FromWallet   string
	ToWallet     string
	TokenAddress string
	AmountAtomic string
	AmountUSDC   float64
	BlockNumber  int64
}

func (p *OpenAIProxy) SetPrepaidOnchainConfig(cfg *PrepaidOnchainConfig) {
	if cfg == nil {
		p.prepaidOnchain = nil
		return
	}
	clean := *cfg
	clean.Network = strings.TrimSpace(clean.Network)
	clean.RPCURL = strings.TrimSpace(clean.RPCURL)
	clean.TokenAddress = strings.ToLower(strings.TrimSpace(clean.TokenAddress))
	clean.ReceiverAddress = strings.ToLower(strings.TrimSpace(clean.ReceiverAddress))
	if clean.Confirmations <= 0 {
		clean.Confirmations = 1
	}
	p.prepaidOnchain = &clean
}

func (p *OpenAIProxy) verifyPrepaidDepositTx(ctx context.Context, network, txHash, walletHint string) (*VerifiedPrepaidDeposit, error) {
	if p.prepaidOnchain == nil {
		return nil, fmt.Errorf("prepaid onchain verification is not configured")
	}
	cfg := p.prepaidOnchain
	if strings.TrimSpace(cfg.RPCURL) == "" || strings.TrimSpace(cfg.TokenAddress) == "" || strings.TrimSpace(cfg.ReceiverAddress) == "" {
		return nil, fmt.Errorf("prepaid onchain verifier missing rpc/token/receiver config")
	}
	if strings.TrimSpace(cfg.Network) != "" && strings.TrimSpace(network) != "" && !strings.EqualFold(cfg.Network, network) {
		return nil, fmt.Errorf("unsupported network: got=%s want=%s", network, cfg.Network)
	}
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return nil, fmt.Errorf("tx_hash is required")
	}
	receipt, err := fetchTransactionReceipt(ctx, cfg.RPCURL, txHash)
	if err != nil {
		return nil, fmt.Errorf("fetch receipt: %w", err)
	}
	if receipt == nil {
		return nil, fmt.Errorf("transaction receipt not found")
	}
	if !strings.EqualFold(strings.TrimSpace(receipt.Status), "0x1") {
		return nil, fmt.Errorf("transaction reverted")
	}
	if err := requireConfirmations(ctx, cfg.RPCURL, receipt.BlockNumber, cfg.Confirmations); err != nil {
		return nil, err
	}

	token := normalizeHexAddress(cfg.TokenAddress)
	receiver := normalizeHexAddress(cfg.ReceiverAddress)
	walletHint = normalizeHexAddress(walletHint)
	transferTopic := strings.ToLower(erc20TransferTopic)

	var (
		foundFrom   string
		totalAtomic = big.NewInt(0)
	)
	for _, lg := range receipt.Logs {
		if len(lg.Topics) < 3 {
			continue
		}
		if normalizeHexAddress(lg.Address) != token {
			continue
		}
		if strings.ToLower(strings.TrimSpace(lg.Topics[0])) != transferTopic {
			continue
		}
		from := normalizeTopicAddress(lg.Topics[1])
		to := normalizeTopicAddress(lg.Topics[2])
		if to != receiver {
			continue
		}
		if walletHint != "" && from != walletHint {
			continue
		}
		if strings.TrimSpace(lg.Data) == "" {
			continue
		}
		amount, ok := parseHexBigInt(lg.Data)
		if !ok {
			continue
		}
		if amount.Sign() <= 0 {
			continue
		}
		totalAtomic = new(big.Int).Add(totalAtomic, amount)
		if foundFrom == "" {
			foundFrom = from
		}
	}
	if foundFrom == "" || totalAtomic.Sign() <= 0 {
		return nil, fmt.Errorf("no matching USDC transfer log found for receiver")
	}
	amountUSDC, _ := new(big.Rat).SetFrac(totalAtomic, big.NewInt(1_000_000)).Float64()
	return &VerifiedPrepaidDeposit{
		TxHash:       strings.ToLower(strings.TrimSpace(txHash)),
		Network:      chooseNetwork(cfg.Network, network),
		FromWallet:   foundFrom,
		ToWallet:     receiver,
		TokenAddress: token,
		AmountAtomic: totalAtomic.String(),
		AmountUSDC:   amountUSDC,
		BlockNumber:  parseHexInt64(receipt.BlockNumber),
	}, nil
}

func requireConfirmations(ctx context.Context, rpcURL, blockNumberHex string, minConfirmations int64) error {
	if strings.TrimSpace(blockNumberHex) == "" {
		return fmt.Errorf("receipt block number missing")
	}
	headHex, err := fetchLatestBlockNumber(ctx, rpcURL)
	if err != nil {
		return fmt.Errorf("fetch latest block: %w", err)
	}
	head := parseHexUint64(headHex)
	blockNumber := parseHexUint64(blockNumberHex)
	want := uint64(minConfirmations - 1)
	if minConfirmations <= 1 {
		want = 0
	}
	if head < blockNumber+want {
		return fmt.Errorf("insufficient confirmations: have=%d need>=%d", head-blockNumber+1, minConfirmations)
	}
	return nil
}

func chooseNetwork(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}

type txReceipt struct {
	Status      string `json:"status"`
	BlockNumber string `json:"blockNumber"`
	Logs        []struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
		Data    string   `json:"data"`
	} `json:"logs"`
}

func fetchTransactionReceipt(ctx context.Context, rpcURL, txHash string) (*txReceipt, error) {
	var out txReceipt
	if err := rpcCall(ctx, rpcURL, "eth_getTransactionReceipt", []any{txHash}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func fetchLatestBlockNumber(ctx context.Context, rpcURL string) (string, error) {
	var out string
	if err := rpcCall(ctx, rpcURL, "eth_blockNumber", []any{}, &out); err != nil {
		return "", err
	}
	return out, nil
}

func rpcCall(ctx context.Context, rpcURL, method string, params []any, out any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(rpcURL), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var decoded struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if decoded.Error != nil {
		return fmt.Errorf("%s", decoded.Error.Message)
	}
	if bytes.Equal(decoded.Result, []byte("null")) {
		return fmt.Errorf("not found")
	}
	return json.Unmarshal(decoded.Result, out)
}

func parseHexBigInt(v string) (*big.Int, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(v, "0x"))
	if s == "" {
		return big.NewInt(0), true
	}
	n := new(big.Int)
	_, ok := n.SetString(s, 16)
	return n, ok
}

func parseHexUint64(v string) uint64 {
	s := strings.TrimSpace(strings.TrimPrefix(v, "0x"))
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseUint(s, 16, 64)
	return n
}

func parseHexInt64(v string) int64 {
	return int64(parseHexUint64(v))
}

func normalizeHexAddress(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "" {
		return s
	}
	s = strings.TrimPrefix(s, "0x")
	if len(s) > 40 {
		s = s[len(s)-40:]
	}
	if len(s) < 40 {
		s = strings.Repeat("0", 40-len(s)) + s
	}
	return "0x" + s
}

func normalizeTopicAddress(topic string) string {
	return normalizeHexAddress(topic)
}
