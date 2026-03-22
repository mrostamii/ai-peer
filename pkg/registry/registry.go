package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mrostamii/ai-peer/pkg/apiv1"
)

// DefaultMissedHeartbeatLimit evicts a node after this many expected heartbeat
// intervals pass without an update (default 3 × 30s = 90s with stock config).
const DefaultMissedHeartbeatLimit = 3

// Registry is an in-memory view of remote nodes for v0.1 gateway routing.
// It is updated from gossip health messages and (optionally) NodeAnnounce payloads.
type Registry struct {
	mu                sync.RWMutex
	nodes             map[string]*NodeRecord
	heartbeatInterval time.Duration
	missedLimit       int
	clock             func() time.Time
}

// NodeRecord is a snapshot-friendly view of one peer.
type NodeRecord struct {
	NodeID          string
	Models          []string
	HardwareSummary string
	LocationHint    string
	PricingHint     string
	LastSeen        time.Time
	UptimeSec       int64
	Load            float64
	LatencyMs       int64
}

type healthJSON struct {
	NodeID      string  `json:"node_id"`
	UptimeSec   int64   `json:"uptime_sec"`
	Load        float64 `json:"load"`
	LatencyMs   int64   `json:"latency_ms"`
	TimestampMs int64   `json:"timestamp_ms"`
}

// Option configures Registry construction.
type Option func(*Registry)

// WithMissedHeartbeatLimit sets how many consecutive missed intervals evict a node.
func WithMissedHeartbeatLimit(n int) Option {
	return func(r *Registry) {
		if n > 0 {
			r.missedLimit = n
		}
	}
}

// WithClock replaces time resolution (tests).
func WithClock(now func() time.Time) Option {
	return func(r *Registry) {
		r.clock = now
	}
}

// New builds a registry. heartbeatInterval should match node.yaml heartbeat.interval_sec.
func New(heartbeatInterval time.Duration, opts ...Option) *Registry {
	if heartbeatInterval <= 0 {
		heartbeatInterval = 30 * time.Second
	}
	r := &Registry{
		nodes:             make(map[string]*NodeRecord),
		heartbeatInterval: heartbeatInterval,
		missedLimit:       DefaultMissedHeartbeatLimit,
		clock:             time.Now,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *Registry) now() time.Time {
	return r.clock()
}

// ApplyHealthProto merges a protobuf health update.
func (r *Registry) ApplyHealthProto(msg *apiv1.HealthUpdate) error {
	if msg == nil {
		return errors.New("nil health update")
	}
	id := msg.GetNodeId()
	if id == "" {
		return errors.New("empty node_id")
	}
	ts := r.now()
	if msg.TimestampMs > 0 {
		ts = time.UnixMilli(msg.TimestampMs)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.nodes[id]
	if rec == nil {
		rec = &NodeRecord{NodeID: id}
		r.nodes[id] = rec
	}
	rec.LastSeen = ts
	rec.UptimeSec = msg.GetUptimeSec()
	rec.Load = msg.GetLoad()
	rec.LatencyMs = msg.GetLatencyMs()
	return nil
}

// ApplyHealthJSON parses the v0.1 gossipsub JSON heartbeat (/ai-peer/v0.1/health).
func (r *Registry) ApplyHealthJSON(payload []byte) error {
	var hj healthJSON
	if err := json.Unmarshal(payload, &hj); err != nil {
		return fmt.Errorf("decode health json: %w", err)
	}
	if hj.NodeID == "" {
		return errors.New("empty node_id")
	}
	ts := r.now()
	if hj.TimestampMs > 0 {
		ts = time.UnixMilli(hj.TimestampMs)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.nodes[hj.NodeID]
	if rec == nil {
		rec = &NodeRecord{NodeID: hj.NodeID}
		r.nodes[hj.NodeID] = rec
	}
	rec.LastSeen = ts
	rec.UptimeSec = hj.UptimeSec
	rec.Load = hj.Load
	rec.LatencyMs = hj.LatencyMs
	return nil
}

// ApplyNodeAnnounceProto merges capability metadata for a node.
func (r *Registry) ApplyNodeAnnounceProto(msg *apiv1.NodeAnnounce) error {
	if msg == nil {
		return errors.New("nil announce")
	}
	id := msg.GetNodeId()
	if id == "" {
		return errors.New("empty node_id")
	}
	ts := r.now()
	if msg.TimestampMs > 0 {
		ts = time.UnixMilli(msg.TimestampMs)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.nodes[id]
	if rec == nil {
		rec = &NodeRecord{NodeID: id}
		r.nodes[id] = rec
	}
	rec.LastSeen = ts
	rec.Models = append([]string(nil), msg.GetModels()...)
	sort.Strings(rec.Models)
	rec.HardwareSummary = msg.GetHardwareSummary()
	rec.LocationHint = msg.GetLocationHint()
	rec.PricingHint = msg.GetPricingHint()
	return nil
}

// PruneStale removes nodes that have not been heard from within
// missedLimit × heartbeatInterval. Returns how many were removed.
func (r *Registry) PruneStale() int {
	cutoff := r.now().Add(-r.heartbeatInterval * time.Duration(r.missedLimit))
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for id, rec := range r.nodes {
		if rec.LastSeen.Before(cutoff) {
			delete(r.nodes, id)
			removed++
		}
	}
	return removed
}

// NodesForModel returns live nodes advertising model (exact string match).
func (r *Registry) NodesForModel(model string) []NodeRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []NodeRecord
	for _, rec := range r.nodes {
		if model == "" {
			continue
		}
		for _, m := range rec.Models {
			if m == model {
				out = append(out, *cloneRecord(rec))
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// List returns a sorted copy of all known nodes.
func (r *Registry) List() []NodeRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeRecord, 0, len(r.nodes))
	for _, rec := range r.nodes {
		out = append(out, *cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// Len returns the number of tracked nodes.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

func cloneRecord(rec *NodeRecord) *NodeRecord {
	cp := *rec
	cp.Models = append([]string(nil), rec.Models...)
	return &cp
}
