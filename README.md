# tooti

**Decentralized AI inference protocol**; coordination, routing, and (later) economics over libp2p, with an OpenAI-compatible gateway.

## Solution

We build the **protocol layer**, not the inference engine. Operators plug in Ollama, vLLM, llama.cpp, or other backends. This project focuses on:

1. **Discovery**: who is online, which models they serve, hardware signals
2. **Routing**: latency- and cost-aware request routing across heterogeneous nodes
3. **Trust**: reputation, staking, proof-of-inference
4. **Payments**: USDC settlement on Base, x402-style flows where appropriate
5. **API gateway**: single OpenAI-compatible surface for clients

### In scope vs out of scope


| We build                                    | We do not build                     |
| ------------------------------------------- | ----------------------------------- |
| libp2p coordination, health, capability ads | CUDA / tensor kernels               |
| Smart router (latency / cost / reputation)  | Model training or fine-tuning       |
| Node agent (Go binary wrapping backends)    | Inference engine internals          |
| OpenAI-compatible gateway                   | Our own token (USDC-only direction) |


### Audiences

- **Node operators:** install one binary, point at a local inference backend, earn USDC per request (once payments land).  
- **Developers:** OpenAI-compatible HTTP API; routing and settlement stay behind the scenes.

Detailed tasks and launch checklist are tracked in the execution plan.

## Current status (v0.1)

The project now provides a working decentralized Ollama network over libp2p with:

- node agent with config-driven model advertisement
- OpenAI-compatible gateway (`/v1/models`, `/v1/chat/completions`, streaming)
- multi-node discovery and model-aware routing
- retry logic on node failure
- heartbeat-based network registry
- basic CLI operations (`node`, `network`, `gateway`)
- OpenClaw integration guide (`docs/openclaw.md`, current maturity noted inside)

### Quick run

```bash
go build -o tooti ./cmd/tooti

# Validate config first
./tooti config-check -file ./node.yaml

# Start node
./tooti node start -file ./node.yaml

# Start gateway (same or another host)
./tooti gateway start -file ./node.yaml
```

Then call the OpenAI-compatible API:

```bash
curl -s http://127.0.0.1:8080/v1/models
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"llama3.2:latest","stream":true,"messages":[{"role":"user","content":"say hi"}]}'
```

**Go modules:** this repo includes a `replace` for `github.com/libp2p/go-libp2p/core` so imports resolve unambiguously to `github.com/libp2p/go-libp2p` v0.48.0 (see `go.mod`).

## OpenClaw integration

- Guide: `docs/openclaw.md`
- Example provider config: `docs/openclaw.json.example`

## Repository layout

```
cmd/tooti/          # main binary entrypoint
proto/                # coordination .proto (v0.1)
pkg/apiv1/            # generated protobuf Go types
pkg/registry/         # in-memory node registry (gateway / routing)
pkg/                  # shared libraries
contracts/            # Solidity / deployment artifacts (Phase 2+)
docs/                 # additional documentation
deploy/               # deployment configs
```

## License

MIT

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md).