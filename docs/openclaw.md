# OpenClaw Integration

This guide wires `ai-peer` into OpenClaw as a custom OpenAI-compatible provider.

## Current maturity

- **Current level:** Level 1 (config-only)
- No plugin is required at this level.

## What this gives you

- OpenClaw can call `ai-peer` using OpenAI-compatible chat completions.
- Model listing comes from `ai-peer` gateway (`GET /v1/models`).
- Streaming works through the same `POST /v1/chat/completions` path.

## 1) Start ai-peer gateway

Use a local or remote-only gateway. Example:

```bash
./build/ai-peer gateway start -file ./node.yaml
```

Default mode is remote-only routing. If you intentionally want local fallback:

```bash
./build/ai-peer gateway start -file ./node.yaml -local-backend
```

## 2) Verify gateway before OpenClaw

```bash
curl -s http://127.0.0.1:8080/v1/models
```

Then test one completion:

```bash
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5:3b","stream":true,"messages":[{"role":"user","content":"say hi"}]}'
```

If your providers require x402 payments, use `ai-peer pay chat` for validation:

```bash
./build/ai-peer pay chat \
  -url http://127.0.0.1:8080/v1/chat/completions \
  -model qwen2.5:3b \
  -message "say hi" \
  -stream true
```

## 3) Add ai-peer as a provider in OpenClaw

Use the example in `docs/openclaw.json.example` and copy the provider block
into your OpenClaw config.

Key setting:

- `baseUrl` must point to your gateway with `/v1` suffix.

Examples:

- local gateway: `http://127.0.0.1:8080/v1`
- remote gateway: `http://<gateway-host>:8080/v1`

## 4) Validate from OpenClaw

After OpenClaw starts:

- list models and confirm ai-peer models appear
- run one non-stream chat
- run one stream chat
- verify gateway logs show remote `node_id` (not `local`) when using remote-only mode

## Troubleshooting

- **No models in OpenClaw**
  - Check `curl http://<gateway>/v1/models` first.
  - Ensure gateway can discover peers (bootstrap connectivity).
- **Gateway returns 503**
  - No provider found for requested model in remote-only mode.
  - Use exact model names (`llama3.2:latest` vs `llama3.2` mismatch).
- **Payment errors**
  - x402 is enforced by provider nodes. Confirm wallet/env and retry flow.

