// Package transport defines the peer-transport boundary (ADR-0002).
//
// Phase 1-2 implementation is authenticated HTTP between agents for
// LAN/test use. The production NAT-traversal implementation (tsnet /
// WireGuard-class, direct-first with encrypted relay fallback) plugs
// in behind this same surface: hosting, scheduler and Lua code never
// see which tunnel carried a message.
package transport

import (
	"context"
	"time"
)

// PeerEndpoint is how to reach one peer.
type PeerEndpoint struct {
	PeerID  string `json:"peer_id"`
	Name    string `json:"name"`
	Address string `json:"address"` // agent base URL (LAN impl) or mesh address
}

// Transport sends control messages between agents.
type Transport interface {
	// Announce registers this peer with the coordinator.
	Announce(ctx context.Context) error
	// Peers lists currently known peers from the coordinator.
	Peers(ctx context.Context) ([]PeerEndpoint, error)
	// Ping checks a peer agent's liveness; returns round-trip time.
	Ping(ctx context.Context, ep PeerEndpoint) (time.Duration, error)
}
