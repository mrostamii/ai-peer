package node

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"
)

const (
	modelKeyPrefix      = "ai-peer/v0.1/model/"
	capabilityKeyPrefix = "ai-peer/v0.1/capability/"
)

type providerRouter interface {
	Provide(context.Context, cid.Cid, bool) error
}

type providerFinder interface {
	FindProvidersAsync(context.Context, cid.Cid, int) <-chan peer.AddrInfo
}

func modelProviderCID(model string) (cid.Cid, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return cid.Cid{}, fmt.Errorf("model is empty")
	}
	return discoveryCID(modelKeyPrefix + model)
}

func capabilityProviderCID(model string, hw HardwareInfo, pricePer1K string) (cid.Cid, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return cid.Cid{}, fmt.Errorf("model is empty")
	}
	pricePer1K = strings.TrimSpace(pricePer1K)
	if pricePer1K == "" {
		pricePer1K = "0"
	}
	key := capabilityKeyPrefix +
		url.PathEscape(model) + "/" +
		url.PathEscape(hw.OS) + "/" +
		url.PathEscape(hw.Arch) + "/" +
		url.PathEscape(hw.GPU) + "/price/" +
		url.PathEscape(pricePer1K)
	return discoveryCID(key)
}

func discoveryCID(key string) (cid.Cid, error) {
	pref := cid.Prefix{
		Version:  1,
		Codec:    cid.Raw,
		MhType:   multihash.SHA2_256,
		MhLength: -1,
	}
	return pref.Sum([]byte(key))
}

func advertiseCapabilities(ctx context.Context, router providerRouter, models []string, hw HardwareInfo, pricePer1K string) error {
	seen := map[string]struct{}{}
	for _, model := range models {
		m := strings.TrimSpace(model)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}

		modelCID, err := modelProviderCID(m)
		if err != nil {
			return err
		}
		if err := router.Provide(ctx, modelCID, true); err != nil {
			return fmt.Errorf("provide model %q: %w", m, err)
		}

		capCID, err := capabilityProviderCID(m, hw, pricePer1K)
		if err != nil {
			return err
		}
		if err := router.Provide(ctx, capCID, true); err != nil {
			return fmt.Errorf("provide capability %q: %w", m, err)
		}
	}
	return nil
}

func (r *Runtime) advertiseCapabilitiesLoop(ctx context.Context, models []string, hw HardwareInfo, pricePer1K string) {
	if err := advertiseCapabilities(ctx, r.dht, models, hw, pricePer1K); err != nil {
		logDiscoveryError(err)
	}

	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := advertiseCapabilities(ctx, r.dht, models, hw, pricePer1K); err != nil {
				logDiscoveryError(err)
			}
		}
	}
}

func logDiscoveryError(err error) {
	// Keep discovery failures visible without bringing down the node.
	log.Printf("discovery advertisement warning: %v", err)
}

func findModelProviders(ctx context.Context, finder providerFinder, self peer.ID, model string, limit int) ([]peer.AddrInfo, error) {
	if limit <= 0 {
		limit = 16
	}
	key, err := modelProviderCID(model)
	if err != nil {
		return nil, err
	}

	out := make([]peer.AddrInfo, 0, limit)
	for p := range finder.FindProvidersAsync(ctx, key, limit) {
		if p.ID == "" || p.ID == self || len(p.Addrs) == 0 {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *Runtime) FindModelProviders(ctx context.Context, model string, limit int) ([]peer.AddrInfo, error) {
	return findModelProviders(ctx, r.dht, r.host.ID(), model, limit)
}
