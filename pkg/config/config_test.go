package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	p := filepath.Join(d, "node.yaml")
	err := os.WriteFile(p, []byte(`node:
  name: "ovh-lon-1"
  identity_key_file: "./data/node_identity.key"
listen:
  tcp_port: 4001
  quic_port: 4001
network:
  bootstrap_peers:
    - "/ip4/51.195.145.102/tcp/4001/p2p/12D3Koo..."
backend:
  type: "ollama"
  base_url: "http://127.0.0.1:11434"
models:
  advertised:
    - "llama3.2:latest"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Listen.TCPPort != 4001 || cfg.Listen.QUICPort != 4001 {
		t.Fatalf("unexpected ports: %+v", cfg.Listen)
	}
	if cfg.Node.IdentityKeyFile != "./data/node_identity.key" {
		t.Fatalf("identity key file not loaded: %q", cfg.Node.IdentityKeyFile)
	}
	if cfg.Heartbeat.IntervalSec != 30 || cfg.Timeouts.FirstTokenSec != 30 || cfg.Timeouts.TotalRequestSec != 120 {
		t.Fatalf("defaults not applied: heartbeat=%d first=%d total=%d", cfg.Heartbeat.IntervalSec, cfg.Timeouts.FirstTokenSec, cfg.Timeouts.TotalRequestSec)
	}
	if cfg.Gateway.Listen != "127.0.0.1:8080" {
		t.Fatalf("gateway default not applied: %q", cfg.Gateway.Listen)
	}
}

func TestLoadInvalidConfig(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	p := filepath.Join(d, "bad-node.yaml")
	err := os.WriteFile(p, []byte(`node:
  name: ""
listen:
  tcp_port: 0
  quic_port: 4001
network:
  bootstrap_peers: []
backend:
  type: "unknown"
  base_url: "not a url"
models:
  advertised: []
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Load(p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "node.name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	p := filepath.Join(d, "unknown.yaml")
	err := os.WriteFile(p, []byte(`node:
  name: "x"
  unknown_field: true
listen:
  tcp_port: 4001
  quic_port: 4001
network:
  bootstrap_peers:
    - "/ip4/1.2.3.4/tcp/4001/p2p/12D3Koo..."
backend:
  type: "ollama"
  base_url: "http://127.0.0.1:11434"
models:
  advertised:
    - "llama3.2:latest"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Load(p)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "field unknown_field not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadAllowsEmptyBootstrapPeers(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	p := filepath.Join(d, "empty-bootstrap.yaml")
	err := os.WriteFile(p, []byte(`node:
  name: "x"
listen:
  tcp_port: 4001
  quic_port: 4001
network:
  bootstrap_peers: []
backend:
  type: "ollama"
  base_url: "http://127.0.0.1:11434"
models:
  advertised:
    - "llama3.2:latest"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Load(p)
	if err != nil {
		t.Fatalf("expected empty bootstrap peers to be allowed, got: %v", err)
	}
}

func TestLoadNATOptions(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	p := filepath.Join(d, "nat.yaml")
	err := os.WriteFile(p, []byte(`node:
  name: "x"
listen:
  tcp_port: 4001
  quic_port: 4001
network:
  bootstrap_peers: []
  disable_nat_traversal: true
  enable_relay_service: true
backend:
  type: "ollama"
  base_url: "http://127.0.0.1:11434"
models:
  advertised:
    - "llama3.2:latest"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Network.DisableNATTraversal {
		t.Fatalf("DisableNATTraversal=%v want true", cfg.Network.DisableNATTraversal)
	}
	if !cfg.Network.EnableRelayService {
		t.Fatalf("EnableRelayService=%v want true", cfg.Network.EnableRelayService)
	}
}

func TestLoadAppliesBackendDefaultsWhenMissing(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	p := filepath.Join(d, "no-backend.yaml")
	err := os.WriteFile(p, []byte(`node:
  name: "x"
listen:
  tcp_port: 4001
  quic_port: 4001
network:
  bootstrap_peers: []
models:
  advertised:
    - "llama3.2:latest"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Backend.Type != "ollama" {
		t.Fatalf("backend.type=%q want ollama", cfg.Backend.Type)
	}
	if cfg.Backend.BaseURL != "http://127.0.0.1:11434" {
		t.Fatalf("backend.base_url=%q want http://127.0.0.1:11434", cfg.Backend.BaseURL)
	}
}
