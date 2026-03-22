package node

import (
	"path/filepath"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestLoadOrCreateIdentityStableAcrossCalls(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "node_identity.key")

	k1, err := loadOrCreateIdentity(p)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity() first call error = %v", err)
	}
	k2, err := loadOrCreateIdentity(p)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity() second call error = %v", err)
	}
	id1, err := peer.IDFromPublicKey(k1.GetPublic())
	if err != nil {
		t.Fatalf("peer.IDFromPublicKey(first): %v", err)
	}
	id2, err := peer.IDFromPublicKey(k2.GetPublic())
	if err != nil {
		t.Fatalf("peer.IDFromPublicKey(second): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("peer id changed across reload: %s vs %s", id1, id2)
	}
}
