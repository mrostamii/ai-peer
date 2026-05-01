package node

import (
	"crypto/rand"
	"testing"

	ic "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func newTestPeerID(t *testing.T) peer.ID {
	t.Helper()
	_, pub, err := ic.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIsAllowedInferencePeer_OpenAllowlist(t *testing.T) {
	t.Parallel()
	id := newTestPeerID(t)
	r := &Runtime{}
	if !r.isAllowedInferencePeer(id) {
		t.Fatal("empty allowlist must allow any peer")
	}
}

func TestIsAllowedInferencePeer_Whitelist(t *testing.T) {
	t.Parallel()
	good := newTestPeerID(t)
	bad := newTestPeerID(t)
	r := &Runtime{
		allowedGatewayPeers: map[peer.ID]struct{}{
			good: {},
		},
	}
	if !r.isAllowedInferencePeer(good) {
		t.Fatal("listed peer should be allowed")
	}
	if r.isAllowedInferencePeer(bad) {
		t.Fatal("non-listed peer must be rejected")
	}
}
