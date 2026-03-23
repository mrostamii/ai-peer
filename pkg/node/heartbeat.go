package node

import (
	"context"
	"encoding/json"
	"log"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// HealthTopicID is the gossipsub topic for JSON health heartbeats (v0.1).
const HealthTopicID = "/ai-peer/v0.1/health"

type HealthUpdate struct {
	NodeID      string   `json:"node_id"`
	UptimeSec   int64    `json:"uptime_sec"`
	Load        float64  `json:"load"`
	LatencyMs   int64    `json:"latency_ms"`
	TTFTMs      int64    `json:"ttft_ms"`
	DecodeTPS   float64  `json:"decode_tps"`
	TimestampMs int64    `json:"timestamp_ms"`
	Models      []string `json:"models,omitempty"`
}

type healthPublisher interface {
	Publish(context.Context, []byte) error
}

type gossipsubPublisher struct {
	topic *pubsub.Topic
}

func (p *gossipsubPublisher) Publish(ctx context.Context, data []byte) error {
	return p.topic.Publish(ctx, data)
}

func buildHealthUpdate(nodeID string, models []string, startedAt, now time.Time, load float64, latencyMs int64, ttftMs int64, decodeTPS float64) ([]byte, error) {
	uptime := now.Sub(startedAt)
	if uptime < 0 {
		uptime = 0
	}
	msg := HealthUpdate{
		NodeID:      nodeID,
		UptimeSec:   int64(uptime / time.Second),
		Load:        load,
		LatencyMs:   latencyMs,
		TTFTMs:      ttftMs,
		DecodeTPS:   decodeTPS,
		TimestampMs: now.UnixMilli(),
		Models:      models,
	}
	return json.Marshal(msg)
}

func publishHealthUpdate(ctx context.Context, pub healthPublisher, nodeID string, models []string, startedAt, now time.Time, load float64, latencyMs int64, ttftMs int64, decodeTPS float64) error {
	payload, err := buildHealthUpdate(nodeID, models, startedAt, now, load, latencyMs, ttftMs, decodeTPS)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, payload)
}

func (r *Runtime) healthHeartbeatLoop(ctx context.Context, interval time.Duration, pub healthPublisher, models []string) {
	load, latencyMs, ttftMs, decodeTPS := r.healthSnapshot()
	if err := publishHealthUpdate(ctx, pub, r.host.ID().String(), models, r.startedAt, time.Now(), load, latencyMs, ttftMs, decodeTPS); err != nil {
		log.Printf("health heartbeat warning: %v", err)
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			load, latencyMs, ttftMs, decodeTPS := r.healthSnapshot()
			if err := publishHealthUpdate(ctx, pub, r.host.ID().String(), models, r.startedAt, now, load, latencyMs, ttftMs, decodeTPS); err != nil {
				log.Printf("health heartbeat warning: %v", err)
			}
		}
	}
}
