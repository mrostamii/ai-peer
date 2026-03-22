package node

import (
	"context"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

type fakeProviderRouter struct {
	provided map[string]int
}

func (f *fakeProviderRouter) Provide(_ context.Context, c cid.Cid, _ bool) error {
	if f.provided == nil {
		f.provided = map[string]int{}
	}
	f.provided[c.String()]++
	return nil
}

func TestModelProviderCID(t *testing.T) {
	t.Parallel()
	c1, err := modelProviderCID("llama3.2:latest")
	if err != nil {
		t.Fatalf("modelProviderCID() error = %v", err)
	}
	c2, err := modelProviderCID("llama3.2:latest")
	if err != nil {
		t.Fatalf("modelProviderCID() error = %v", err)
	}
	if c1.String() != c2.String() {
		t.Fatalf("expected deterministic cid, got %s and %s", c1, c2)
	}
}

func TestAdvertiseCapabilitiesProvidesModelAndCapabilityKeys(t *testing.T) {
	t.Parallel()
	r := &fakeProviderRouter{}
	hw := HardwareInfo{OS: "linux", Arch: "amd64", GPU: "nvidia"}
	if err := advertiseCapabilities(context.Background(), r, []string{"llama3.2:latest", "llama3.2:latest"}, hw, "0"); err != nil {
		t.Fatalf("advertiseCapabilities() error = %v", err)
	}
	if len(r.provided) != 2 {
		t.Fatalf("expected 2 provider records (model + capability), got %d", len(r.provided))
	}
}

type fakeFinder struct {
	out []peer.AddrInfo
}

func (f *fakeFinder) FindProvidersAsync(_ context.Context, _ cid.Cid, _ int) <-chan peer.AddrInfo {
	ch := make(chan peer.AddrInfo, len(f.out))
	for _, p := range f.out {
		ch <- p
	}
	close(ch)
	return ch
}

func TestFindModelProvidersExcludesSelfAndEmptyAddrs(t *testing.T) {
	t.Parallel()
	addr, err := ma.NewMultiaddr("/ip4/1.2.3.4/tcp/4001")
	if err != nil {
		t.Fatalf("NewMultiaddr() error = %v", err)
	}
	self := peer.ID("self")
	f := &fakeFinder{
		out: []peer.AddrInfo{
			{ID: self},
			{ID: peer.ID("noaddrs")},
			{ID: peer.ID("p1"), Addrs: []ma.Multiaddr{addr}},
		},
	}
	got, err := findModelProviders(context.Background(), f, self, "llama3.2:latest", 5)
	if err != nil {
		t.Fatalf("findModelProviders() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("unexpected providers: %#v", got)
	}
}
