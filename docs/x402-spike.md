# x402 Base Sepolia Spike (Go)

This spike demonstrates the x402 HTTP V2 flow in Go:

1. Request resource
2. Receive `402 Payment Required` + `PAYMENT-REQUIRED`
3. Build/sign `PAYMENT-SIGNATURE` (EIP-712/EIP-3009 for USDC)
4. Retry with payment header
5. Receive response + `PAYMENT-RESPONSE`

## Files

- `pkg/x402spike/eip712.go`: Minimal x402 data types + EIP-712 signing for USDC `transferWithAuthorization`
- `cmd/spike-x402-provider/main.go`: Tiny paid endpoint (`/paid`) that returns 402 and validates payment payload
- `cmd/spike-x402-consumer/main.go`: Tiny buyer client that performs 402->sign->retry

## Testnet Defaults

- Network: `eip155:84532` (Base Sepolia)
- USDC (Base Sepolia): `0x036CbD53842c5426634e7929541eC2318f3dCF7e`
- Scheme: `exact`

## Run Locally

Provider:

```bash
go run ./cmd/spike-x402-provider \
  -listen 127.0.0.1:8091 \
  -path /paid \
  -payto 0x209693Bc6afc0C5328bA36FaF03C514EF312287C \
  -facilitator https://x402.org/facilitator \
  -token-name USDC \
  -token-version 2
```

Consumer:

```bash
export EVM_PRIVATE_KEY=0xYOUR_CONSUMER_PRIVATE_KEY
go run ./cmd/spike-x402-consumer -url http://127.0.0.1:8091/paid
```

## Optional Real Settlement

To settle on chain, run provider with a facilitator URL:

```bash
go run ./cmd/spike-x402-provider \
  -payto 0x209693Bc6afc0C5328bA36FaF03C514EF312287C \
  -facilitator https://your-facilitator.example
```

When `-facilitator` is omitted, provider still validates protocol flow but returns a local placeholder transaction value (`0xspike-local-no-facilitator`) and does not broadcast on chain.

## Gateway Integration (ai-peer)

`ai-peer gateway start` now supports an optional x402 paywall for `POST /v1/chat/completions`.

Example:

```bash
go run ./cmd/ai-peer gateway start -file ./node.yaml \
  -x402-enable \
  -x402-facilitator https://x402.org/facilitator \
  -x402-network eip155:84532 \
  -x402-asset 0x036CbD53842c5426634e7929541eC2318f3dCF7e \
  -x402-amount 10000 \
  -x402-payto 0x209693Bc6afc0C5328bA36FaF03C514EF312287C \
  -x402-token-name USDC \
  -x402-token-version 2
```

When enabled:

- requests without `PAYMENT-SIGNATURE` get `402` + `PAYMENT-REQUIRED`
- valid paid retries proceed to inference and receive `PAYMENT-RESPONSE`

## Agent-friendly paid client

Use `ai-peer pay chat` to handle x402 automatically (request -> 402 -> sign -> retry):

```bash
export EVM_PRIVATE_KEY=0xYOUR_CONSUMER_PRIVATE_KEY

go run ./cmd/ai-peer pay chat \
  -url http://127.0.0.1:8080/v1/chat/completions \
  -model qwen2.5:3b \
  -message "say hi" \
  -stream true
```

This avoids manual `PAYMENT-SIGNATURE` handling and is intended as the base UX for agent integrations.