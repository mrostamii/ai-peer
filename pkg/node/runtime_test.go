package node

import "testing"

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
