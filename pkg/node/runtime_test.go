package node

import (
	"context"
	"testing"

	"github.com/mrostamii/tooti/pkg/config"
	ma "github.com/multiformats/go-multiaddr"
)

func TestParseBootstrapPeers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	input := []string{"/ip4/51.195.145.102/tcp/4001/p2p/12D3KooWQXVG5RfM8P6Y1k9ihR1RUmfDfM2hPsoYwhhYp2Gy1AHJ"}
	peers, err := ParseBootstrapPeers(ctx, input)
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
	_, err := ParseBootstrapPeers(context.Background(), []string{"not-a-multiaddr"})
	if err == nil {
		t.Fatal("expected error for invalid multiaddr")
	}
}

func TestParseBootstrapPeersMergesSamePeerID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := "12D3KooWQXVG5RfM8P6Y1k9ihR1RUmfDfM2hPsoYwhhYp2Gy1AHJ"
	raw := []string{
		"/ip4/10.0.0.1/tcp/4001/p2p/" + id,
		"/ip4/10.0.0.2/udp/4001/quic-v1/p2p/" + id,
	}
	peers, err := ParseBootstrapPeers(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 {
		t.Fatalf("expected 1 merged bootstrap, got %d", len(peers))
	}
	if len(peers[0].Addrs) != 2 {
		t.Fatalf("expected 2 addrs for same peer, got %d", len(peers[0].Addrs))
	}
}

func TestParseBootstrapPeersDNSAddrMockResolve(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	idA := "12D3KooWQXVG5RfM8P6Y1k9ihR1RUmfDfM2hPsoYwhhYp2Gy1AHJ"
	idB := "12D3KooWRpDfHqHBJHHpDcTTb2Dmz4UoYHHXiApihxDwN13KqTw4"
	mock := func(_ context.Context, _ ma.Multiaddr) ([]ma.Multiaddr, error) {
		m1, err := ma.NewMultiaddr("/ip4/10.1.0.1/tcp/4001/p2p/" + idA)
		if err != nil {
			t.Fatal(err)
		}
		m2, err := ma.NewMultiaddr("/ip4/10.1.0.2/tcp/4001/p2p/" + idB)
		if err != nil {
			t.Fatal(err)
		}
		return []ma.Multiaddr{m1, m2}, nil
	}
	peers, err := parseBootstrapPeers(ctx, []string{"/dnsaddr/tooti.test.invalid"}, mock)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 bootstraps from dnsaddr expand, got %d", len(peers))
	}
}

func TestParseBootstrapPeersRejectsIPWithoutP2P(t *testing.T) {
	t.Parallel()
	_, err := ParseBootstrapPeers(context.Background(), []string{"/ip4/10.0.0.1/tcp/4001"})
	if err == nil {
		t.Fatal("expected error without /p2p")
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
