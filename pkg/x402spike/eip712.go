package x402spike

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	decredECDSA "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

const (
	eip712DomainType         = "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
	transferAuthType         = "TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"
	defaultDomainName        = "USD Coin"
	defaultDomainVersion     = "2"
	defaultTimeoutSeconds    = 60
	defaultAuthorizationSkew = 5
)

type ResourceInfo struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type PaymentRequirements struct {
	Scheme            string         `json:"scheme"`
	Network           string         `json:"network"`
	Amount            string         `json:"amount"`
	Asset             string         `json:"asset"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int64          `json:"maxTimeoutSeconds"`
	Extra             map[string]any `json:"extra,omitempty"`
}

type PaymentRequired struct {
	X402Version int                   `json:"x402Version"`
	Error       string                `json:"error,omitempty"`
	Resource    ResourceInfo          `json:"resource"`
	Accepts     []PaymentRequirements `json:"accepts"`
	Extensions  map[string]any        `json:"extensions,omitempty"`
}

type Authorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

type ExactEVMPayload struct {
	Signature     string        `json:"signature"`
	Authorization Authorization `json:"authorization"`
}

type PaymentPayload struct {
	X402Version int                 `json:"x402Version"`
	Resource    ResourceInfo        `json:"resource,omitempty"`
	Accepted    PaymentRequirements `json:"accepted"`
	Payload     ExactEVMPayload     `json:"payload"`
	Extensions  map[string]any      `json:"extensions,omitempty"`
}

type SettlementResponse struct {
	Success     bool           `json:"success"`
	ErrorReason string         `json:"errorReason,omitempty"`
	Payer       string         `json:"payer,omitempty"`
	Transaction string         `json:"transaction"`
	Network     string         `json:"network"`
	Extensions  map[string]any `json:"extensions,omitempty"`
}

func BuildPaymentPayload(privateKeyHex string, req PaymentRequirements, resource ResourceInfo, now time.Time) (PaymentPayload, error) {
	chainID, err := parseEIP155ChainID(req.Network)
	if err != nil {
		return PaymentPayload{}, err
	}
	priv, fromAddr, err := privateKeyToAddress(privateKeyHex)
	if err != nil {
		return PaymentPayload{}, err
	}
	toAddrBytes, err := parseAddress(req.PayTo)
	if err != nil {
		return PaymentPayload{}, fmt.Errorf("invalid payTo address: %w", err)
	}
	value := new(big.Int)
	if _, ok := value.SetString(req.Amount, 10); !ok {
		return PaymentPayload{}, fmt.Errorf("invalid amount: %q", req.Amount)
	}

	validAfter := now.Unix() - defaultAuthorizationSkew
	timeout := req.MaxTimeoutSeconds
	if timeout <= 0 {
		timeout = defaultTimeoutSeconds
	}
	validBefore := now.Unix() + timeout
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return PaymentPayload{}, fmt.Errorf("generate nonce: %w", err)
	}

	domainName := defaultDomainName
	domainVersion := defaultDomainVersion
	if rawName, ok := req.Extra["name"]; ok {
		if s, ok := rawName.(string); ok && strings.TrimSpace(s) != "" {
			domainName = s
		}
	}
	if rawVersion, ok := req.Extra["version"]; ok {
		if s, ok := rawVersion.(string); ok && strings.TrimSpace(s) != "" {
			domainVersion = s
		}
	}

	domainSeparator, err := buildDomainSeparator(domainName, domainVersion, chainID, req.Asset)
	if err != nil {
		return PaymentPayload{}, err
	}
	structHash, err := buildTransferWithAuthorizationHash(
		fromAddr,
		toAddrBytes,
		value,
		big.NewInt(validAfter),
		big.NewInt(validBefore),
		nonceBytes,
	)
	if err != nil {
		return PaymentPayload{}, err
	}

	digest := keccak256([]byte{0x19, 0x01}, domainSeparator, structHash)
	sigHex, err := signDigestHex(priv, digest)
	if err != nil {
		return PaymentPayload{}, err
	}

	return PaymentPayload{
		X402Version: 2,
		Resource:    resource,
		Accepted:    req,
		Payload: ExactEVMPayload{
			Signature: sigHex,
			Authorization: Authorization{
				From:        "0x" + hex.EncodeToString(fromAddr),
				To:          req.PayTo,
				Value:       req.Amount,
				ValidAfter:  strconvInt(validAfter),
				ValidBefore: strconvInt(validBefore),
				Nonce:       "0x" + hex.EncodeToString(nonceBytes),
			},
		},
		Extensions: map[string]any{},
	}, nil
}

func EncodeBase64JSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func DecodeBase64JSON(header string, out any) error {
	raw, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func parseEIP155ChainID(network string) (*big.Int, error) {
	parts := strings.Split(network, ":")
	if len(parts) != 2 || parts[0] != "eip155" {
		return nil, fmt.Errorf("unsupported network: %q", network)
	}
	n := new(big.Int)
	if _, ok := n.SetString(parts[1], 10); !ok {
		return nil, fmt.Errorf("invalid chain id: %q", parts[1])
	}
	return n, nil
}

func buildDomainSeparator(name, version string, chainID *big.Int, verifyingContract string) ([]byte, error) {
	typeHash := keccak256([]byte(eip712DomainType))
	contract, err := parseAddress(verifyingContract)
	if err != nil {
		return nil, fmt.Errorf("invalid asset contract: %w", err)
	}
	return keccak256(
		typeHash,
		keccak256([]byte(name)),
		keccak256([]byte(version)),
		u256(chainID),
		addressWord(contract),
	), nil
}

func buildTransferWithAuthorizationHash(
	fromAddr []byte,
	toAddr []byte,
	value *big.Int,
	validAfter *big.Int,
	validBefore *big.Int,
	nonce []byte,
) ([]byte, error) {
	if len(fromAddr) != 20 || len(toAddr) != 20 {
		return nil, errors.New("address must be 20 bytes")
	}
	if len(nonce) != 32 {
		return nil, errors.New("nonce must be 32 bytes")
	}
	typeHash := keccak256([]byte(transferAuthType))
	return keccak256(
		typeHash,
		addressWord(fromAddr),
		addressWord(toAddr),
		u256(value),
		u256(validAfter),
		u256(validBefore),
		nonce,
	), nil
}

func privateKeyToAddress(privateKeyHex string) (*secp256k1.PrivateKey, []byte, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(privateKeyHex), "0x")
	keyBytes, err := hex.DecodeString(clean)
	if err != nil {
		return nil, nil, fmt.Errorf("decode private key hex: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, nil, fmt.Errorf("private key must be 32 bytes, got %d", len(keyBytes))
	}
	priv := secp256k1.PrivKeyFromBytes(keyBytes)
	pub := priv.PubKey().SerializeUncompressed() // 65 bytes with 0x04 prefix
	pubHash := keccak256(pub[1:])
	return priv, pubHash[12:], nil
}

func signDigestHex(priv *secp256k1.PrivateKey, digest []byte) (string, error) {
	if len(digest) != 32 {
		return "", fmt.Errorf("digest must be 32 bytes, got %d", len(digest))
	}
	compact := decredECDSA.SignCompact(priv, digest, false) // [header|r|s]
	if len(compact) != 65 {
		return "", fmt.Errorf("unexpected compact signature length: %d", len(compact))
	}
	v := compact[0]
	rs := compact[1:]
	ethSig := make([]byte, 0, 65)
	ethSig = append(ethSig, rs...)
	ethSig = append(ethSig, v) // 27/28 value
	return "0x" + hex.EncodeToString(ethSig), nil
}

func parseAddress(addr string) ([]byte, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(addr), "0x")
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, err
	}
	if len(raw) != 20 {
		return nil, fmt.Errorf("address must be 20 bytes, got %d", len(raw))
	}
	return raw, nil
}

func addressWord(address []byte) []byte {
	out := make([]byte, 32)
	copy(out[12:], address)
	return out
}

func u256(n *big.Int) []byte {
	if n == nil {
		n = big.NewInt(0)
	}
	out := make([]byte, 32)
	b := n.Bytes()
	copy(out[32-len(b):], b)
	return out
}

func keccak256(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	return h.Sum(nil)
}

func strconvInt(v int64) string {
	return fmt.Sprintf("%d", v)
}
