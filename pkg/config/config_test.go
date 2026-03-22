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
	if cfg.Heartbeat.IntervalSec != 30 || cfg.Timeouts.FirstTokenSec != 30 || cfg.Timeouts.TotalRequestSec != 120 {
		t.Fatalf("defaults not applied: heartbeat=%d first=%d total=%d", cfg.Heartbeat.IntervalSec, cfg.Timeouts.FirstTokenSec, cfg.Timeouts.TotalRequestSec)
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
