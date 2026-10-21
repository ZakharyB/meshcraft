// Package gateway implements the optional public Minecraft gateway
// (spec §3.3, ADR-0004): a TCP proxy on a reachable host that routes
// vanilla Java clients to the active host's Paper endpoint.
//
// Security: routes can only be registered with the world's current
// fencing token; per-IP rate limiting and a global connection cap are
// enforced; the gateway holds no world data and no keys.
package gateway

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ZakharyB/meshcraft/internal/hosting"
)

// Config for a gateway instance.
type Config struct {
	ListenAddr     string        // e.g. ":25565"
	MaxConns       int           // global concurrent cap (default 200)
	PerIPPerMinute int           // new-connection rate per IP (default 10)
	IdleTimeout    time.Duration // default 5m
}

// Gateway proxies TCP connections according to its routing table.
// One gateway process serves one public endpoint; the route maps the
// (single) public port to the active world host. Multi-world public
// hosting uses one gateway port per world — documented in GATEWAY.md.
type Gateway struct {
	cfg  Config
	auth *hosting.Authority

	mu    sync.Mutex
	route routeEntry // current route for this endpoint
	conns int
	rate  map[string][]time.Time
}

type routeEntry struct {
	WorldID  string
	Target   string // host:port of the active Paper server (via mesh/LAN address)
	HolderID string
	Token    uint64
}

// New creates a gateway.
func New(cfg Config, auth *hosting.Authority) *Gateway {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 200
	}
	if cfg.PerIPPerMinute <= 0 {
		cfg.PerIPPerMinute = 10
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if auth == nil {
		auth = hosting.NewAuthority(nil)
	}
	return &Gateway{cfg: cfg, auth: auth, rate: map[string][]time.Time{}}
}

// RegisterRoute installs a route. The (holder, token) pair must verify
// against the gateway's authority view — a stale host cannot re-register.
func (g *Gateway) RegisterRoute(worldID, target, holderID string, token uint64) error {
	if !g.auth.Verify(worldID, holderID, token) {
		return fmt.Errorf("route registration rejected: no valid lease for holder %s token %d", holderID, token)
	}
	g.mu.Lock()
	g.route = routeEntry{WorldID: worldID, Target: target, HolderID: holderID, Token: token}
	g.mu.Unlock()
	return nil
}

// ObserveLease feeds a lease into the gateway's authority view.
func (g *Gateway) ObserveLease(l hosting.Lease) bool { return g.auth.Observe(l) }

// Route returns the current route, if any.
func (g *Gateway) Route() (string, string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.route.WorldID == "" {
		return "", "", false
	}
	return g.route.WorldID, g.route.Target, true
}

func (g *Gateway) allowIP(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	kept := g.rate[ip][:0]
	for _, t := range g.rate[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= g.cfg.PerIPPerMinute {
		g.rate[ip] = kept
		return false
	}
	g.rate[ip] = append(kept, now)
	return true
}

// Serve accepts connections until the listener is closed.
func (g *Gateway) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go g.handle(conn)
	}
}

// ListenAndServe is Serve on the configured address.
func (g *Gateway) ListenAndServe() error {
	ln, err := net.Listen("tcp", g.cfg.ListenAddr)
	if err != nil {
		return err
	}
	return g.Serve(ln)
}

func (g *Gateway) handle(client net.Conn) {
	defer client.Close()
	host, _, _ := net.SplitHostPort(client.RemoteAddr().String())
	if !g.allowIP(host) {
		return // rate limited: drop silently
	}
	_, target, ok := g.Route()
	if !ok {
		return // no active world on this endpoint
	}
	g.mu.Lock()
	if g.conns >= g.cfg.MaxConns {
		g.mu.Unlock()
		return
	}
	g.conns++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.conns--
		g.mu.Unlock()
	}()

	upstream, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	// Idle timeout applies to both directions.
	deadline := time.Now().Add(g.cfg.IdleTimeout)
	_ = client.SetDeadline(deadline)
	_ = upstream.SetDeadline(deadline)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}
