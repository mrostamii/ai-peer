// Spike-libp2p is a Phase 0.2 hands-on exercise: join the public IPFS Kademlia DHT,
// advertise a fixed rendezvous CID, discover a peer via FindProviders, open a stream,
// and exchange one line of text. Use it to measure discovery latency and to observe
// whether connections are direct or relayed (NAT / hole-punching signal).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/ipfs/go-cid"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
)

const (
	echoProto   = protocol.ID("/ai-peer/spike-echo/1.0.0")
	rendezvous  = "ai-peer-phase0-dht-rendezvous-v1"
	dialTimeout = 3 * time.Minute
	// Bootstrap returns before the routing table is usually populated; Provide/FindProviders need ≥1 peer.
	dhtWarmupTimeout = 2 * time.Minute
)

// waitForDHTRoutingPeers blocks until the Kademlia routing table has at least wantMin peers or maxWait elapses.
func waitForDHTRoutingPeers(ctx context.Context, kdht *dht.IpfsDHT, wantMin int, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	var lastLog time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := kdht.RoutingTable().Size()
		if n >= wantMin {
			log.Printf("DHT routing table ready (%d peer(s))", n)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DHT routing table still empty after %v (size=%d, need at least %d): check outbound UDP/TCP (firewall/VPN) or use -bootstrap with a reachable /ip4/.../p2p/... address", maxWait, n, wantMin)
		}
		if time.Since(lastLog) > 5*time.Second {
			log.Printf("waiting for DHT peers (routing table size=%d, need at least %d)...", n, wantMin)
			lastLog = time.Now()
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func spikeCID() (cid.Cid, error) {
	pref := cid.Prefix{
		Version:  1,
		Codec:    cid.Raw,
		MhType:   multihash.SHA2_256,
		MhLength: -1,
	}
	return pref.Sum([]byte(rendezvous))
}

type multiaddrList []string

func (m *multiaddrList) String() string { return strings.Join(*m, ",") }

func (m *multiaddrList) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func parseBootstrapAddrInfos(raw []string) ([]peer.AddrInfo, error) {
	var out []peer.AddrInfo
	for _, s := range raw {
		maddr, err := ma.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("bootstrap %q: %w", s, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return nil, fmt.Errorf("bootstrap %q: %w", s, err)
		}
		out = append(out, *info)
	}
	return out, nil
}

func validatePort(name string, p int) error {
	if p < 0 || p > 65535 {
		return fmt.Errorf("%s must be in [0, 65535], got %d", name, p)
	}
	return nil
}

func listenAddrStrings(tcpPort, quicPort int) []string {
	return []string{
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", tcpPort),
		fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", quicPort),
	}
}

func mergeBootstrap(extra []peer.AddrInfo) func() []peer.AddrInfo {
	return func() []peer.AddrInfo {
		base := dht.GetDefaultBootstrapPeerAddrInfos()
		if len(extra) == 0 {
			return base
		}
		return append(append([]peer.AddrInfo{}, extra...), base...)
	}
}

func printHostAddrs(tag string, h interface {
	ID() peer.ID
	Addrs() []ma.Multiaddr
}) {
	pi := peer.AddrInfo{ID: h.ID(), Addrs: h.Addrs()}
	addrs, err := peer.AddrInfoToP2pAddrs(&pi)
	if err != nil {
		log.Printf("%s: could not format addrs: %v", tag, err)
		return
	}
	log.Printf("%s peer ID: %s", tag, h.ID())
	for _, a := range addrs {
		log.Printf("%s dial addr: %s", tag, a)
	}
}

func connSummary(h interface {
	Network() network.Network
}, remote peer.ID) string {
	var parts []string
	for _, c := range h.Network().ConnsToPeer(remote) {
		rm := c.RemoteMultiaddr().String()
		kind := "direct"
		if strings.Contains(rm, "p2p-circuit") {
			kind = "relay"
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", rm, kind))
	}
	if len(parts) == 0 {
		return "(no active conns)"
	}
	return strings.Join(parts, "; ")
}

func runListen(ctx context.Context, bootstraps []peer.AddrInfo, tcpPort, quicPort int) error {
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(listenAddrStrings(tcpPort, quicPort)...),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
	)
	if err != nil {
		return err
	}
	defer h.Close()

	kdht, err := dht.New(ctx, h,
		dht.Mode(dht.ModeServer),
		dht.BootstrapPeersFunc(mergeBootstrap(bootstraps)),
	)
	if err != nil {
		return err
	}
	defer kdht.Close()

	if err := kdht.Bootstrap(ctx); err != nil {
		return fmt.Errorf("dht bootstrap: %w", err)
	}
	if err := waitForDHTRoutingPeers(ctx, kdht, 1, dhtWarmupTimeout); err != nil {
		return err
	}

	h.SetStreamHandler(echoProto, func(s network.Stream) {
		defer s.Close()
		rw := bufio.NewReadWriter(bufio.NewReader(s), bufio.NewWriter(s))
		line, err := rw.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("echo read: %v", err)
			return
		}
		line = strings.TrimSpace(line)
		_, _ = fmt.Fprintf(rw, "echo:%s\n", line)
		_ = rw.Flush()
	})

	cidKey, err := spikeCID()
	if err != nil {
		return err
	}

	log.Printf("DHT Provide starting for CID %s (rendezvous key %q)", cidKey, rendezvous)
	t0 := time.Now()
	if err := kdht.Provide(ctx, cidKey, true); err != nil {
		return fmt.Errorf("provide: %w", err)
	}
	log.Printf("DHT Provide finished in %s", time.Since(t0))

	printHostAddrs("server", h)

	<-ctx.Done()
	return ctx.Err()
}

func runDial(ctx context.Context, bootstraps []peer.AddrInfo, directPeer string, message string, tcpPort, quicPort int) error {
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(listenAddrStrings(tcpPort, quicPort)...),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
	)
	if err != nil {
		return err
	}
	defer h.Close()

	kdht, err := dht.New(ctx, h,
		dht.Mode(dht.ModeClient),
		dht.BootstrapPeersFunc(mergeBootstrap(bootstraps)),
	)
	if err != nil {
		return err
	}
	defer kdht.Close()

	if err := kdht.Bootstrap(ctx); err != nil {
		return fmt.Errorf("dht bootstrap: %w", err)
	}
	if directPeer == "" {
		if err := waitForDHTRoutingPeers(ctx, kdht, 1, dhtWarmupTimeout); err != nil {
			return err
		}
	}

	printHostAddrs("client", h)

	cidKey, err := spikeCID()
	if err != nil {
		return err
	}

	var target peer.AddrInfo
	findCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	if directPeer != "" {
		maddr, err := ma.NewMultiaddr(directPeer)
		if err != nil {
			return fmt.Errorf("parse -peer: %w", err)
		}
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return fmt.Errorf("parse -peer p2p addr: %w", err)
		}
		target = *info
		log.Printf("using -peer (skipping DHT find): %s", directPeer)
	} else {
		log.Printf("FindProviders on %s (timeout %v)", cidKey, dialTimeout)
		tDiscover := time.Now()
		ch := kdht.FindProvidersAsync(findCtx, cidKey, 16)
		var found bool
		for p := range ch {
			if p.ID == "" || p.ID == h.ID() {
				continue
			}
			if len(p.Addrs) == 0 {
				continue
			}
			target = p
			found = true
			break
		}
		if !found {
			return errors.New("FindProviders: no provider with addresses (try -bootstrap with server dial addr or -peer)")
		}
		log.Printf("first provider after %s: id=%s addrs=%v", time.Since(tDiscover), target.ID, target.Addrs)
	}

	tConnect := time.Now()
	if err := h.Connect(findCtx, target); err != nil {
		return fmt.Errorf("connect to %s: %w", target.ID, err)
	}
	log.Printf("Connect OK in %s; %s", time.Since(tConnect), connSummary(h, target.ID))

	tStream := time.Now()
	s, err := h.NewStream(findCtx, target.ID, echoProto)
	if err != nil {
		return fmt.Errorf("new stream: %w", err)
	}
	defer s.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(s), bufio.NewWriter(s))
	if _, err := fmt.Fprintf(rw, "%s\n", message); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	reply, err := rw.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read reply: %w", err)
	}
	log.Printf("round-trip after stream open: %s", time.Since(tStream))
	log.Printf("reply: %s", strings.TrimSpace(reply))
	log.Printf("final conns: %s", connSummary(h, target.ID))
	return nil
}

func main() {
	var (
		listen     = flag.Bool("listen", false, "server: DHT provide + echo handler until interrupted")
		bootstrap  multiaddrList
		peerAddr   = flag.String("peer", "", "client: optional full /ip4/.../p2p/<id> (skips DHT discovery)")
		msg        = flag.String("msg", "hello from ai-peer spike", "client: one-line payload")
		tcpPort    = flag.Int("tcp-port", 0, "local TCP listen port (0 = random)")
		quicPort   = flag.Int("quic-port", 0, "local QUIC UDP listen port (0 = random)")
	)
	flag.Var(&bootstrap, "bootstrap", "extra bootstrap multiaddr (repeatable); merged before default IPFS bootstraps")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	binfos, err := parseBootstrapAddrInfos(bootstrap)
	if err != nil {
		log.Fatal(err)
	}

	if *listen && *peerAddr != "" {
		log.Fatal("with -listen, do not pass -peer (client only); -msg is ignored on the server")
	}
	if err := validatePort("tcp-port", *tcpPort); err != nil {
		log.Fatal(err)
	}
	if err := validatePort("quic-port", *quicPort); err != nil {
		log.Fatal(err)
	}

	if *listen {
		if err := runListen(ctx, binfos, *tcpPort, *quicPort); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal(err)
		}
		return
	}
	if err := runDial(ctx, binfos, *peerAddr, *msg, *tcpPort, *quicPort); err != nil {
		log.Fatal(err)
	}
}
