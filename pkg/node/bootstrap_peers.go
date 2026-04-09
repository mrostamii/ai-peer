package node

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	madns "github.com/multiformats/go-multiaddr-dns"
)

// ParseBootstrapPeers turns config strings into AddrInfos. Each entry may be:
//   - A full p2p multiaddr, e.g. /ip4/.../tcp/4001/p2p/12D3KooW...
//   - A dnsaddr (or dns4/dns6/dns) multiaddr without /p2p/..., e.g. /dnsaddr/discover.tooti.network.
//     Those are resolved with DNS (TXT for dnsaddr); every resolved line must end with /p2p/<peer-id>.
//   - A dnsaddr plus /p2p/<id> is treated as a normal p2p multiaddr (resolved at dial time by libp2p).
//
// Multiple resolved addresses for the same peer id are merged into one AddrInfo.
func ParseBootstrapPeers(ctx context.Context, raw []string) ([]peer.AddrInfo, error) {
	return parseBootstrapPeers(ctx, raw, madns.Resolve)
}

func parseBootstrapPeers(ctx context.Context, raw []string, resolve func(context.Context, ma.Multiaddr) ([]ma.Multiaddr, error)) ([]peer.AddrInfo, error) {
	byID := make(map[peer.ID]peer.AddrInfo)
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		maddr, err := ma.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("invalid bootstrap multiaddr %q: %w", s, err)
		}

		_, errP2P := maddr.ValueForProtocol(ma.P_P2P)
		if errP2P == nil {
			info, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				return nil, fmt.Errorf("invalid bootstrap peer addr %q: %w", s, err)
			}
			mergeBootstrapAddrInfo(byID, *info)
			continue
		}
		if !errors.Is(errP2P, ma.ErrProtocolNotFound) {
			return nil, fmt.Errorf("bootstrap %q: %w", s, errP2P)
		}

		if !madns.Matches(maddr) {
			return nil, fmt.Errorf("bootstrap %q must include /p2p/<peer-id> or be a resolvable dnsaddr/dns4/dns6/dns multiaddr (without /p2p) to expand from DNS", s)
		}

		resolved, err := resolve(ctx, maddr)
		if err != nil {
			return nil, fmt.Errorf("resolve bootstrap %q: %w", s, err)
		}
		if len(resolved) == 0 {
			return nil, fmt.Errorf("bootstrap %q resolved to zero addresses", s)
		}
		var any bool
		for _, r := range resolved {
			info, err := peer.AddrInfoFromP2pAddr(r)
			if err != nil {
				continue
			}
			mergeBootstrapAddrInfo(byID, *info)
			any = true
		}
		if !any {
			return nil, fmt.Errorf("bootstrap %q: DNS resolution returned no valid p2p multiaddrs", s)
		}
	}

	out := make([]peer.AddrInfo, 0, len(byID))
	for _, ai := range byID {
		out = append(out, ai)
	}
	return out, nil
}

func mergeBootstrapAddrInfo(byID map[peer.ID]peer.AddrInfo, ai peer.AddrInfo) {
	if ai.ID == "" {
		return
	}
	if ex, ok := byID[ai.ID]; ok {
		ex.Addrs = appendUniqueMultiaddrs(ex.Addrs, ai.Addrs)
		byID[ai.ID] = ex
		return
	}
	byID[ai.ID] = peer.AddrInfo{
		ID:    ai.ID,
		Addrs: append([]ma.Multiaddr(nil), ai.Addrs...),
	}
}

func appendUniqueMultiaddrs(base, extra []ma.Multiaddr) []ma.Multiaddr {
	for _, a := range extra {
		if a == nil {
			continue
		}
		dup := false
		for _, b := range base {
			if b != nil && b.Equal(a) {
				dup = true
				break
			}
		}
		if !dup {
			base = append(base, a)
		}
	}
	return base
}
