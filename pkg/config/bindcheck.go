package config

import (
	"fmt"
	"net"
)

func EnsureTCPAddrAvailable(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp listen %s unavailable: %w", addr, err)
	}
	_ = ln.Close()
	return nil
}

func EnsureUDPAddrAvailable(addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("udp listen %s unavailable: %w", addr, err)
	}
	_ = pc.Close()
	return nil
}
