# ai-peer

**Decentralized AI inference protocol** — coordination, routing, and (later) economics over libp2p, with an OpenAI-compatible gateway. This repository is the working home for the project ([github.com/mrostamii/ai-peer](https://github.com/mrostamii/ai-peer)).

> **One-liner:** The Kubernetes of decentralized AI inference — an orchestration and economic layer that connects GPU operators to consumers so decentralized inference can be economically viable.

## Problem

Centralized inference is expensive and gated; many GPUs sit idle. Prior decentralized efforts proved technical feasibility (Petals, Exo, Parallax, etc.) but lacked a durable **economic coordination layer**.

## Solution

We build the **protocol layer**, not the inference engine. Operators plug in Ollama, vLLM, llama.cpp, or other backends. This project focuses on:

1. **Discovery** — who is online, which models they serve, hardware signals  
2. **Routing** — latency- and cost-aware request routing across heterogeneous nodes  
3. **Trust** — reputation, staking, proof-of-inference (roadmap)  
4. **Payments** — USDC settlement on Base, x402-style flows where appropriate (roadmap)  
5. **API gateway** — single OpenAI-compatible surface for clients  

### In scope vs out of scope

| We build | We do not build |
|----------|-----------------|
| libp2p coordination, health, capability ads | CUDA / tensor kernels |
| Smart router (latency / cost / reputation) | Model training or fine-tuning |
| Node agent (Go binary wrapping backends) | Inference engine internals |
| OpenAI-compatible gateway | Our own token (USDC-only direction) |

### Audiences

- **Node operators:** install one binary, point at a local inference backend, earn USDC per request (once payments land).  
- **Developers:** OpenAI-compatible HTTP API; routing and settlement stay behind the scenes.

## Roadmap (high level)

| Version | Focus |
|---------|--------|
| **v0.1** | Decentralized Ollama — P2P network, gateway, no chain |
| **v0.2** | Payments — USDC per request |
| **v0.3** | Reputation + cooperative credits |
| **v0.4+** | Proof of inference, MCP, hardening |

Detailed tasks live in the internal execution plan (Phase 0 = foundation and research spikes).

## Repository layout

```
cmd/ai-peer/   # main binary entrypoint (expanded in Phase 1)
pkg/           # shared libraries
contracts/     # Solidity / deployment artifacts (Phase 2+)
docs/          # additional documentation
deploy/        # deployment configs
```

## Requirements

- [Go](https://go.dev/dl/) (see `go.mod` for the toolchain version)

## Build

```bash
go build -o ai-peer ./cmd/ai-peer
./ai-peer
```

```bash
go test ./...
```

## License

MIT — see [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
