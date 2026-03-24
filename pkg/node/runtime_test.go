package node

import (
	"testing"

	"github.com/mrostamii/ai-peer/pkg/config"
)

func TestParseBootstrapPeers(t *testing.T) {
	t.Parallel()
	input := []string{"/ip4/51.195.145.102/tcp/4001/p2p/12D3KooWQXVG5RfM8P6Y1k9ihR1RUmfDfM2hPsoYwhhYp2Gy1AHJ"}
	peers, err := ParseBootstrapPeers(input)
	if err != nil {
		t.Fatalf("ParseBootstrapPeers() error = %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 bootstrap, got %d", len(peers))
	}
	if peers[0].ID.String() != "12D3KooWQXVG5RfM8P6Y1k9ihR1RUmfDfM2hPsoYwhhYp2Gy1AHJ" {
		t.Fatalf("unexpected peer id: %s", peers[0].ID)
	}
}

func TestParseBootstrapPeersInvalid(t *testing.T) {
	t.Parallel()
	_, err := ParseBootstrapPeers([]string{"not-a-multiaddr"})
	if err == nil {
		t.Fatal("expected error for invalid multiaddr")
	}
}

func TestResolveNATConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	nc := resolveNATConfig(cfg, 1)
	if !nc.TraversalEnabled {
		t.Fatalf("TraversalEnabled=%v want true", nc.TraversalEnabled)
	}
	if nc.RelayServiceEnabled {
		t.Fatalf("RelayServiceEnabled=%v want false", nc.RelayServiceEnabled)
	}
	if !nc.AutoRelayEnabled {
		t.Fatalf("AutoRelayEnabled=%v want true", nc.AutoRelayEnabled)
	}
}

func TestResolveNATConfigDisabled(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Network.DisableNATTraversal = true
	cfg.Network.EnableRelayService = true
	nc := resolveNATConfig(cfg, 1)
	if nc.TraversalEnabled {
		t.Fatalf("TraversalEnabled=%v want false", nc.TraversalEnabled)
	}
	if !nc.RelayServiceEnabled {
		t.Fatalf("RelayServiceEnabled=%v want true", nc.RelayServiceEnabled)
	}
	if nc.AutoRelayEnabled {
		t.Fatalf("AutoRelayEnabled=%v want false", nc.AutoRelayEnabled)
	}
}
