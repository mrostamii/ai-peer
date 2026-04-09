package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/mrostamii/tooti/pkg/config"
)

// StartObserving starts libp2p + DHT like the full node, but does not advertise
// models, publish heartbeats, or expose metrics. It subscribes to HealthTopicID
// and passes each payload to onHealth (e.g. registry.ApplyHealthJSON).
func StartObserving(ctx context.Context, cfg *config.Config, onHealth func([]byte) error) (*Runtime, error) {
	if onHealth == nil {
		return nil, fmt.Errorf("onHealth callback is required")
	}
	r, err := startBase(ctx, cfg)
	if err != nil {
		return nil, err
	}
	r.logDialAddrs()

	if r.reconnect {
		go r.bootstrapReconnectLoop(ctx)
	} else {
		log.Printf("default DHT bootstrap mode: reconnect loop disabled")
	}

	ps, err := pubsub.NewGossipSub(ctx, r.host)
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("gossipsub: %w", err)
	}
	topic, err := ps.Join(HealthTopicID)
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("join health topic: %w", err)
	}
	sub, err := topic.Subscribe()
	if err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("subscribe health topic: %w", err)
	}
	go r.healthSubscribeLoop(ctx, sub, onHealth)
	// Seed routing table quickly when using default DHT bootstraps (no reconnect loop).
	go func() {
		r.ConnectBootstrapsOnce(ctx)
	}()
	return r, nil
}

func (r *Runtime) healthSubscribeLoop(ctx context.Context, sub *pubsub.Subscription, onHealth func([]byte) error) {
	defer sub.Cancel()
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("health subscribe: next message failed: %v", err)
			continue
		}
		if msg.GetFrom() == r.host.ID() {
			continue
		}
		data := msg.GetData()
		if len(data) == 0 {
			continue
		}
		r.logNewPeerFromHealth(msg.GetFrom().String(), data)
		if err := onHealth(data); err != nil {
			log.Printf("health subscribe: apply payload: %v", err)
		}
	}
}

func (r *Runtime) logNewPeerFromHealth(fromPeerID string, payload []byte) {
	if strings.TrimSpace(fromPeerID) == "" {
		return
	}
	r.peerLogMu.Lock()
	if _, ok := r.peerLogged[fromPeerID]; ok {
		r.peerLogMu.Unlock()
		return
	}
	r.peerLogged[fromPeerID] = struct{}{}
	r.peerLogMu.Unlock()

	var health HealthUpdate
	if err := json.Unmarshal(payload, &health); err != nil {
		log.Printf("network peer established: peer_id=%s addrs=%v models=[] pricing=[] decode_error=%q",
			fromPeerID,
			r.peerAddrStrings(fromPeerID),
			err.Error(),
		)
		return
	}

	models := append([]string(nil), health.Models...)
	sort.Strings(models)
	pricing := formatModelPricingForLog(health.ModelPricing)
	log.Printf("network peer established: peer_id=%s heartbeat_node_id=%s addrs=%v models=%v pricing=%v",
		fromPeerID,
		health.NodeID,
		r.peerAddrStrings(fromPeerID),
		models,
		pricing,
	)
}

func (r *Runtime) peerAddrStrings(peerID string) []string {
	id, err := peer.Decode(peerID)
	if err != nil {
		return nil
	}
	addrs := r.host.Peerstore().Addrs(id)
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.String())
	}
	sort.Strings(out)
	return out
}

func formatModelPricingForLog(pricing map[string]ModelPricingHint) []string {
	if len(pricing) == 0 {
		return nil
	}
	keys := make([]string, 0, len(pricing))
	for model := range pricing {
		keys = append(keys, model)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, model := range keys {
		p := pricing[model]
		out = append(out, fmt.Sprintf("%s:price_per_1k_atomic=%d,min_amount_atomic=%d,max_amount_atomic=%d,default_output_tokens=%d",
			model,
			p.PricePer1KAtomic,
			p.MinAmountAtomic,
			p.MaxAmountAtomic,
			p.DefaultOutputTokens,
		))
	}
	return out
}
