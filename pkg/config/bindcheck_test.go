package config

import (
	"net"
	"testing"
)

func TestEnsureTCPAddrAvailableDetectsConflict(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer ln.Close()

	if err := EnsureTCPAddrAvailable(ln.Addr().String()); err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestEnsureUDPAddrAvailableDetectsConflict(t *testing.T) {
	t.Parallel()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket() error = %v", err)
	}
	defer pc.Close()

	if err := EnsureUDPAddrAvailable(pc.LocalAddr().String()); err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}
