package node

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ping "github.com/libp2p/go-libp2p/p2p/protocol/ping"
	noise "github.com/libp2p/go-libp2p/p2p/security/noise"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/mrostamii/tooti/pkg/backend/ollama"
	"github.com/mrostamii/tooti/pkg/config"
)

const bootstrapReconnectEvery = 30 * time.Second

// TootiDHTProtocolPrefix is the libp2p Kademlia namespace for the Tooti network
// (DHT sub-protocols become /tooti/kad/1.0.0, etc., instead of /ipfs/...).
const TootiDHTProtocolPrefix = "/tooti"

type Runtime struct {
	host                host.Host
	dht                 *dht.IpfsDHT
	bootstraps          []peer.AddrInfo
	startedAt           time.Time
	metricsSrv          *http.Server
	allowedGatewayPeers map[peer.ID]struct{}
	paymentDebtMu       sync.Mutex
	paymentDebtByPayer  map[string]int64
	peerLogMu           sync.Mutex
	peerLogged          map[string]struct{}
	peerConnMu          sync.Mutex
	peerConnCount       map[peer.ID]int
	peerLastGone        map[peer.ID]time.Time
	peerDisconTmr       map[peer.ID]*time.Timer
	bootstrapPeerIDs    map[peer.ID]struct{}

	inflightInference atomic.Int64
	statsMu           sync.RWMutex
	latencyEMAms      float64
	hasLatencySample  bool
	ttftEMAms         float64
	hasTTFTSample     bool
	decodeTPSEMA      float64
	hasDecodeSample   bool
}

type natConfig struct {
	TraversalEnabled    bool
	RelayServiceEnabled bool
	AutoRelayEnabled    bool
}

const peerLogDebounce = 10 * time.Second

func (r *Runtime) Listen(network.Network, ma.Multiaddr)         {}
func (r *Runtime) ListenClose(network.Network, ma.Multiaddr)    {}
func (r *Runtime) OpenedStream(network.Network, network.Stream) {}
func (r *Runtime) ClosedStream(network.Network, network.Stream) {}

func (r *Runtime) Connected(_ network.Network, c network.Conn) {
	r.peerConnMu.Lock()
	defer r.peerConnMu.Unlock()

	id := c.RemotePeer()
	prev := r.peerConnCount[id]
	r.peerConnCount[id] = prev + 1

	if t, ok := r.peerDisconTmr[id]; ok {
		t.Stop()
		delete(r.peerDisconTmr, id)
	}

	if prev == 0 {
		lastGone, wasGone := r.peerLastGone[id]
		delete(r.peerLastGone, id)
		if (!wasGone || time.Since(lastGone) >= peerLogDebounce) && r.shouldLogPeerLifecycle(id) {
			log.Printf("peer connected: peer=%s", formatPeerEndpoint(id, c.RemoteMultiaddr()))
		}
	}
}

func (r *Runtime) Disconnected(_ network.Network, c network.Conn) {
	r.peerConnMu.Lock()
	defer r.peerConnMu.Unlock()

	id := c.RemotePeer()
	prev := r.peerConnCount[id]
	if prev <= 1 {
		delete(r.peerConnCount, id)
		r.peerLastGone[id] = time.Now()
		addr := c.RemoteMultiaddr().String()
		r.peerDisconTmr[id] = time.AfterFunc(peerLogDebounce, func() {
			r.peerConnMu.Lock()
			defer r.peerConnMu.Unlock()
			if r.peerConnCount[id] > 0 {
				return
			}
			delete(r.peerDisconTmr, id)
			if r.shouldLogPeerLifecycle(id) {
				log.Printf("peer disconnected: peer=%s", formatPeerEndpoint(id, ma.StringCast(addr)))
			}
		})
		return
	}
	r.peerConnCount[id] = prev - 1
}

func (r *Runtime) shouldLogPeerLifecycle(id peer.ID) bool {
	if _, ok := r.bootstrapPeerIDs[id]; ok {
		return true
	}
	r.peerLogMu.Lock()
	defer r.peerLogMu.Unlock()
	_, ok := r.peerLogged[id.String()]
	return ok
}

func resolveNATConfig(cfg *config.Config, bootstrapCount int) natConfig {
	traversal := !cfg.Network.DisableNATTraversal
	return natConfig{
		TraversalEnabled:    traversal,
		RelayServiceEnabled: cfg.Network.EnableRelayService,
		AutoRelayEnabled:    traversal && bootstrapCount > 0,
	}
}

func startBase(ctx context.Context, cfg *config.Config) (*Runtime, error) {
	bootstraps, err := ParseBootstrapPeers(ctx, cfg.Network.BootstrapPeers)
	if err != nil {
		return nil, err
	}
	log.Printf("Tooti DHT (prefix %s): %d bootstrap peer(s)", TootiDHTProtocolPrefix, len(bootstraps))

	listenTCP := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.Listen.TCPPort)
	listenQUIC := fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", cfg.Listen.QUICPort)

	dhtMode := dht.ModeServer
	if cfg.Listen.TCPPort == 0 && cfg.Listen.QUICPort == 0 {
		dhtMode = dht.ModeClient
	}

	natCfg := resolveNATConfig(cfg, len(bootstraps))
	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(listenTCP, listenQUIC),
		libp2p.Security(noise.ID, noise.New),
		libp2p.ResourceManager(&network.NullResourceManager{}),
		libp2p.EnableRelay(),
	}
	if natCfg.TraversalEnabled {
		opts = append(opts,
			libp2p.EnableHolePunching(),
			libp2p.NATPortMap(),
			libp2p.EnableNATService(),
			libp2p.EnableAutoNATv2(),
		)
	}
	if natCfg.AutoRelayEnabled {
		opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(bootstraps))
	}
	if natCfg.RelayServiceEnabled {
		opts = append(opts, libp2p.EnableRelayService())
	}
	if cfg.Node.IdentityKeyFile != "" {
		key, err := loadOrCreateIdentity(cfg.Node.IdentityKeyFile)
		if err != nil {
			return nil, err
		}
		opts = append(opts, libp2p.Identity(key))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	kdht, err := dht.New(ctx, h,
		dht.Mode(dhtMode),
		dht.BootstrapPeers(bootstraps...),
		dht.ProtocolPrefix(protocol.ID(TootiDHTProtocolPrefix)),
	)
	if err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("create dht: %w", err)
	}

	if err := kdht.Bootstrap(ctx); err != nil {
		_ = kdht.Close()
		_ = h.Close()
		return nil, fmt.Errorf("bootstrap dht: %w", err)
	}

	r := &Runtime{
		host:                h,
		dht:                 kdht,
		bootstraps:          bootstraps,
		startedAt:           time.Now(),
		allowedGatewayPeers: make(map[peer.ID]struct{}),
		paymentDebtByPayer:  make(map[string]int64),
		peerLogged:          make(map[string]struct{}),
		peerConnCount:       make(map[peer.ID]int),
		peerLastGone:        make(map[peer.ID]time.Time),
		peerDisconTmr:       make(map[peer.ID]*time.Timer),
		bootstrapPeerIDs:    make(map[peer.ID]struct{}),
	}
	for _, b := range bootstraps {
		if b.ID != "" {
			r.bootstrapPeerIDs[b.ID] = struct{}{}
		}
	}
	h.Network().Notify(r)
	log.Printf("network nat: traversal=%t auto_relay=%t relay_service=%t bootstrap_candidates=%d",
		natCfg.TraversalEnabled, natCfg.AutoRelayEnabled, natCfg.RelayServiceEnabled, len(bootstraps))
	return r, nil
}

func Start(ctx context.Context, cfg *config.Config) (*Runtime, error) {
	r, err := startBase(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for _, ps := range cfg.Node.AllowedGatewayPeers {
		ps = strings.TrimSpace(ps)
		if ps == "" {
			continue
		}
		pid, err := peer.Decode(ps)
		if err != nil {
			_ = r.dht.Close()
			_ = r.host.Close()
			return nil, fmt.Errorf("node.allowed_gateway_peers: invalid peer id %q: %w", ps, err)
		}
		r.allowedGatewayPeers[pid] = struct{}{}
	}
	if len(r.allowedGatewayPeers) > 0 {
		log.Printf("inference access: allowlist active (%d official gateway peer id(s))", len(r.allowedGatewayPeers))
	}
	r.logDialAddrs()

	go r.bootstrapReconnectLoop(ctx)
	if cfg.Metrics.Enabled {
		metricsSrv, err := startMetricsServer(ctx, cfg.Metrics.Listen)
		if err != nil {
			_ = r.dht.Close()
			_ = r.host.Close()
			return nil, err
		}
		r.metricsSrv = metricsSrv
	}
	hw := DetectHardware()
	pricePer1K := "0"
	if cfg.Node.X402.Enabled && cfg.Node.X402.PricePer1KAtomic > 0 {
		pricePer1K = fmt.Sprintf("%d", cfg.Node.X402.PricePer1KAtomic)
	}
	modelPricing := buildAdvertisedModelPricing(cfg)
	go r.advertiseCapabilitiesLoop(ctx, cfg.Models.Advertised, hw, pricePer1K)
	r.registerInferenceHandler(ollama.New(cfg.Backend.BaseURL))
	r.registerInferenceStreamHandler(ollama.New(cfg.Backend.BaseURL))
	ps, err := pubsub.NewGossipSub(ctx, r.host)
	if err != nil {
		log.Printf("health heartbeat disabled: init gossipsub failed: %v", err)
		return r, nil
	}
	healthTopic, err := ps.Join(HealthTopicID)
	if err != nil {
		log.Printf("health heartbeat disabled: join topic %q failed: %v", HealthTopicID, err)
		return r, nil
	}
	go r.healthHeartbeatLoop(ctx, time.Duration(cfg.Heartbeat.IntervalSec)*time.Second, &gossipsubPublisher{topic: healthTopic}, cfg.Models.Advertised, modelPricing)
	return r, nil
}

func StartQueryOnly(ctx context.Context, cfg *config.Config) (*Runtime, error) {
	return startBase(ctx, cfg)
}

func (r *Runtime) Close() error {
	var errs []string
	if r.dht != nil {
		if err := r.dht.Close(); err != nil {
			errs = append(errs, "dht close: "+err.Error())
		}
	}
	if r.metricsSrv != nil {
		if err := stopMetricsServer(r.metricsSrv); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if r.host != nil {
		if err := r.host.Close(); err != nil {
			errs = append(errs, "host close: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) logDialAddrs() {
	info := peer.AddrInfo{ID: r.host.ID(), Addrs: r.host.Addrs()}
	addrs, err := peer.AddrInfoToP2pAddrs(&info)
	if err != nil {
		log.Printf("node peer id: %s (addr format error: %v)", r.host.ID(), err)
		return
	}
	log.Printf("node peer id: %s", r.host.ID())
	for _, a := range addrs {
		log.Printf("node dial addr: %s", a)
	}
}

func (r *Runtime) bootstrapReconnectLoop(ctx context.Context) {
	t := time.NewTicker(bootstrapReconnectEvery)
	defer t.Stop()

	r.connectBootstraps(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.connectBootstraps(ctx, true)
		}
	}
}

func (r *Runtime) connectBootstraps(ctx context.Context, logErrors bool) {
	for _, b := range r.bootstraps {
		if b.ID == "" || b.ID == r.host.ID() {
			continue
		}
		if r.host.Network().Connectedness(b.ID) == network.Connected {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := r.host.Connect(dialCtx, b)
		cancel()
		if err != nil {
			if logErrors {
				log.Printf("bootstrap reconnect warning: peer=%v err=%v", formatPeerAddrInfo(b), err)
			}
			continue
		}
		log.Printf("bootstrap connected: peer=%v", formatPeerAddrInfo(b))
	}
}

func formatPeerEndpoint(id peer.ID, addr ma.Multiaddr) string {
	if addr == nil {
		return fmt.Sprintf("/p2p/%s", id)
	}
	p2pAddrs, err := peer.AddrInfoToP2pAddrs(&peer.AddrInfo{
		ID:    id,
		Addrs: []ma.Multiaddr{addr},
	})
	if err != nil || len(p2pAddrs) == 0 {
		return fmt.Sprintf("%s/p2p/%s", strings.TrimSuffix(addr.String(), "/"), id)
	}
	return p2pAddrs[0].String()
}

func formatPeerAddrInfo(info peer.AddrInfo) []string {
	p2pAddrs, err := peer.AddrInfoToP2pAddrs(&info)
	if err != nil || len(p2pAddrs) == 0 {
		out := make([]string, 0, len(info.Addrs))
		for _, a := range info.Addrs {
			out = append(out, formatPeerEndpoint(info.ID, a))
		}
		return out
	}
	out := make([]string, 0, len(p2pAddrs))
	for _, a := range p2pAddrs {
		out = append(out, a.String())
	}
	return out
}

func (r *Runtime) ConnectBootstrapsOnce(ctx context.Context) {
	r.connectBootstraps(ctx, false)
}

func (r *Runtime) ConnectedPeers() []peer.AddrInfo {
	if r.host == nil {
		return nil
	}
	peers := r.host.Network().Peers()
	out := make([]peer.AddrInfo, 0, len(peers))
	for _, id := range peers {
		out = append(out, peer.AddrInfo{
			ID:    id,
			Addrs: r.host.Peerstore().Addrs(id),
		})
	}
	return out
}

func (r *Runtime) PingPeer(ctx context.Context, target peer.ID) (time.Duration, error) {
	if r.host == nil {
		return 0, fmt.Errorf("host not initialized")
	}
	ch := ping.Ping(ctx, r.host, target)
	select {
	case res, ok := <-ch:
		if !ok {
			return 0, fmt.Errorf("ping channel closed")
		}
		if res.Error != nil {
			return 0, res.Error
		}
		return res.RTT, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (r *Runtime) isAllowedInferencePeer(id peer.ID) bool {
	if r == nil || id == "" {
		return false
	}
	if len(r.allowedGatewayPeers) == 0 {
		return true
	}
	_, ok := r.allowedGatewayPeers[id]
	return ok
}
