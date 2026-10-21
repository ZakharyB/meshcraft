package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ZakharyB/meshcraft/internal/coordinator"
	"github.com/ZakharyB/meshcraft/internal/identity"
)

// HTTPTransport is the Phase 1-2 LAN transport: signed registrations
// to a coordinator + direct agent-to-agent HTTP.
type HTTPTransport struct {
	CoordinatorURL string
	Identity       *identity.Identity
	Name           string
	Address        string // this agent's reachable base URL
	CanHost        bool
	FreeRAMMB      int64
	Client         *http.Client
}

func (t *HTTPTransport) http() *http.Client {
	if t.Client != nil {
		return t.Client
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// Announce posts a signed registration/heartbeat to the coordinator.
func (t *HTTPTransport) Announce(ctx context.Context) error {
	reg := coordinator.Registration{
		Name: t.Name, Address: t.Address, CanHost: t.CanHost, FreeRAMMB: t.FreeRAMMB,
	}
	reg.Sign(t.Identity)
	body, _ := json.Marshal(reg)
	req, err := http.NewRequestWithContext(ctx, "POST", t.CoordinatorURL+"/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("coordinator register: %s", resp.Status)
	}
	return nil
}

// Peers fetches live peers from the coordinator.
func (t *HTTPTransport) Peers(ctx context.Context) ([]PeerEndpoint, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", t.CoordinatorURL+"/peers", nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Peers []coordinator.Registration `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	eps := make([]PeerEndpoint, 0, len(out.Peers))
	for _, p := range out.Peers {
		eps = append(eps, PeerEndpoint{PeerID: p.PeerID, Name: p.Name, Address: p.Address})
	}
	return eps, nil
}

// Ping measures reachability of a peer agent's /healthz.
func (t *HTTPTransport) Ping(ctx context.Context, ep PeerEndpoint) (time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", ep.Address+"/healthz", nil)
	if err != nil {
		return 0, err
	}
	resp, err := t.http().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("peer %s healthz: %s", ep.PeerID, resp.Status)
	}
	return time.Since(start), nil
}
