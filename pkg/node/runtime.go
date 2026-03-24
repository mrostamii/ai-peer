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
	ping "github.com/libp2p/go-libp2p/p2p/protocol/ping"
	noise "github.com/libp2p/go-libp2p/p2p/security/noise"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/mrostamii/ai-peer/pkg/backend/ollama"
	"github.com/mrostamii/ai-peer/pkg/config"
)

const bootstrapReconnectEvery = 30 * time.Second

type Runtime struct {
	host               host.Host
	dht                *dht.IpfsDHT
	bootstraps         []peer.AddrInfo
	reconnect          bool
	startedAt          time.Time
	metricsSrv         *http.Server
	inferencePaywall   *x402InferencePaywallConfig
	paymentDebtMu      sync.Mutex
	paymentDebtByPayer map[string]int64
	pendingPayMu       sync.Mutex
	pendingPayByKey    map[string]pendingInferenceResult

	inflightInference atomic.Int64
	statsMu           sync.RWMutex
	latencyEMAms      float64
	hasLatencySample  bool
	ttftEMAms         float64
	hasTTFTSample     bool
	decodeTPSEMA      float64
	hasDecodeSample   bool
}

func startBase(ctx context.Context, cfg *config.Config) (*Runtime, error) {
	useCustomBootstraps := len(cfg.Network.BootstrapPeers) > 0
	var bootstraps []peer.AddrInfo
	var err error
	if useCustomBootstraps {
		bootstraps, err = ParseBootstrapPeers(cfg.Network.BootstrapPeers)
		if err != nil {
			return nil, err
		}
		log.Printf("using %d custom bootstrap peer(s) from config", len(bootstraps))
	} else {
		bootstraps = dht.GetDefaultBootstrapPeerAddrInfos()
		log.Printf("network.bootstrap_peers is empty; using %d default DHT bootstrap peer(s)", len(bootstraps))
	}

	listenTCP := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.Listen.TCPPort)
	listenQUIC := fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", cfg.Listen.QUICPort)

	dhtMode := dht.ModeServer
	if cfg.Listen.TCPPort == 0 && cfg.Listen.QUICPort == 0 {
		dhtMode = dht.ModeClient
	}

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(listenTCP, listenQUIC),
		libp2p.Security(noise.ID, noise.New),
		libp2p.ResourceManager(&network.NullResourceManager{}),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
		libp2p.EnableNATService(),
		libp2p.EnableAutoNATv2(),
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
		host:               h,
		dht:                kdht,
		bootstraps:         bootstraps,
		reconnect:          useCustomBootstraps,
		startedAt:          time.Now(),
		paymentDebtByPayer: make(map[string]int64),
		pendingPayByKey:    make(map[string]pendingInferenceResult),
	}
	return r, nil
}

func Start(ctx context.Context, cfg *config.Config) (*Runtime, error) {
	r, err := startBase(ctx, cfg)
	if err != nil {
		return nil, err
	}
	r.inferencePaywall = buildInferencePaywallConfig(cfg)
	r.logDialAddrs()

	if r.reconnect {
		go r.bootstrapReconnectLoop(ctx)
	} else {
		log.Printf("default DHT bootstrap mode: reconnect loop disabled")
	}
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
				log.Printf("bootstrap reconnect warning: peer=%s err=%v", b.ID, err)
			}
			continue
		}
		log.Printf("bootstrap connected: peer=%s", b.ID)
	}
}

func ParseBootstrapPeers(raw []string) ([]peer.AddrInfo, error) {
	out := make([]peer.AddrInfo, 0, len(raw))
	for _, s := range raw {
		maddr, err := ma.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("invalid bootstrap multiaddr %q: %w", s, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return nil, fmt.Errorf("invalid bootstrap peer addr %q: %w", s, err)
		}
		out = append(out, *info)
	}
	return out, nil
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
