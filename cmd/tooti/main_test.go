package main

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mrostamii/tooti/pkg/node"
)

func TestBuildKnownNodeModelMap(t *testing.T) {
	peerA := "12D3KooWQ7AzWqW3w7sUWif3vwdpcqo6xqR4Qjv6GmS9PM8qUCNw"
	peerB := "12D3KooWG2i5v5SE5vJ9V6q5UPfQ3ygfNo9aVY7wM8tnjY4V8Nbx"
	peerC := "12D3KooWC8uNyxR4J7Kj7W6Q6tNmbJ9g9UeN1QQh6uSwtTb4Y9Eh"

	known := map[string]struct{}{
		peerA: {},
		peerB: {},
	}

	avail := []node.ModelAvailability{
		{
			Model: "llama3.2:latest",
			Providers: []peer.AddrInfo{
				{ID: mustPeerID(t, peerB)},
				{ID: mustPeerID(t, peerA)},
				{ID: mustPeerID(t, peerC)},
			},
		},
		{
			Model: "qwen2.5:7b",
			Providers: []peer.AddrInfo{
				{ID: mustPeerID(t, peerA)},
			},
		},
		{
			Model: "llama3.2:latest",
			Providers: []peer.AddrInfo{
				{ID: mustPeerID(t, peerA)},
			},
		},
	}

	got := buildKnownNodeModelMap(avail, known)

	if len(got) != 2 {
		t.Fatalf("len(got)=%d want 2", len(got))
	}

	if models := got[peerA]; len(models) != 2 || models[0] != "llama3.2:latest" || models[1] != "qwen2.5:7b" {
		t.Fatalf("peer-a models=%v want [llama3.2:latest qwen2.5:7b]", models)
	}
	if models := got[peerB]; len(models) != 1 || models[0] != "llama3.2:latest" {
		t.Fatalf("peer-b models=%v want [llama3.2:latest]", models)
	}
	if _, ok := got[peerC]; ok {
		t.Fatal("peer-c should be filtered out")
	}
}

func mustPeerID(t *testing.T, id string) peer.ID {
	t.Helper()
	pid, err := peer.Decode(id)
	if err != nil {
		t.Fatalf("peer.Decode(%q): %v", id, err)
	}
	return pid
}
