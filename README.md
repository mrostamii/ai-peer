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

## Phase 0.2 — research spikes (hands-on)

Runnable programs under `cmd/spike-*` map to the execution plan’s **0.2 Research Spikes** (x402 and Base/Solidity spikes are deferred there).

**Step-by-step testing:** [docs/SPIKE-TESTING.md](docs/SPIKE-TESTING.md)

### `spike-libp2p` — DHT rendezvous + echo

Joins the public IPFS Kademlia DHT (same bootstrap peers as kubo), **Provides** a fixed rendezvous CID, and discovers the server with **FindProviders**. Logs **Provide** duration, time until the first provider appears, **Connect** latency, and whether the active path looks **direct** or **relay** (`p2p-circuit` in the multiaddr).

```bash
# Terminal A — server (Ctrl+C to stop)
go run ./cmd/spike-libp2p -listen

# Same machine — pass one printed "dial addr" so the client seeds the routing table quickly
go run ./cmd/spike-libp2p -bootstrap '/ip4/127.0.0.1/tcp/…/p2p/<server-peer-id>'

# Or skip DHT discovery and measure only connect/stream (baseline)
go run ./cmd/spike-libp2p -peer '/ip4/…/p2p/<id>' -msg 'hello'
```

For **internet / NAT** experiments, run the server on a reachable host (or home + VPS), use `-bootstrap` with the server’s full dial address, and compare logs with and without **UDP/QUIC** and relay paths.

### `spike-ollama` — Ollama HTTP API

Calls **`GET /api/tags`**, a non-streaming **`POST /api/chat`**, and optionally a **streaming** chat (newline-delimited JSON). Requires [Ollama](https://ollama.com/) listening on the chosen base URL (default `http://127.0.0.1:11434`).

```bash
go run ./cmd/spike-ollama -model llama3.2 -stream
```

### `spike-openai-proxy` — OpenAI-shaped gateway → Ollama

Minimal **`GET /v1/models`** and **`POST /v1/chat/completions`** (non-streaming only in this spike) that proxy to Ollama’s **`/api/tags`** and **`/api/chat`**.

```bash
# Terminal A
go run ./cmd/spike-openai-proxy -listen 127.0.0.1:8080

# Terminal B
curl -s http://127.0.0.1:8080/v1/models
curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"llama3.2","messages":[{"role":"user","content":"Hi"}]}'
```

Point Cursor or any OpenAI client at `http://127.0.0.1:8080/v1` to exercise the same path.

**Go modules:** this repo includes a `replace` for `github.com/libp2p/go-libp2p/core` so imports resolve unambiguously to `github.com/libp2p/go-libp2p` v0.48.0 (see `go.mod`).

## Repository layout

```
cmd/ai-peer/          # main binary entrypoint (expanded in Phase 1)
cmd/spike-libp2p/     # Phase 0.2 libp2p + DHT spike
cmd/spike-ollama/     # Phase 0.2 Ollama API client spike
cmd/spike-openai-proxy/ # Phase 0.2 OpenAI-compatible → Ollama proxy spike
pkg/                  # shared libraries
contracts/            # Solidity / deployment artifacts (Phase 2+)
docs/                 # additional documentation
deploy/               # deployment configs
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
