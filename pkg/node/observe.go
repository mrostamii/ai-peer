package node

import (
	"context"
	"fmt"
	"log"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

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
		if err := onHealth(data); err != nil {
			log.Printf("health subscribe: apply payload: %v", err)
		}
	}
}
