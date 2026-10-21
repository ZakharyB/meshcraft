// Package coordinator implements the coordination service: peer
// registration (signature-verified), discovery, heartbeats and host
// lease arbitration. It stores NO world data (spec §4) — only peer
// presence and lease state.
package coordinator

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ZakharyB/meshcraft/internal/hosting"
	"github.com/ZakharyB/meshcraft/internal/identity"
)

// Registration is a peer's signed enrollment/heartbeat payload.
type Registration struct {
	PeerID    string `json:"peer_id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"` // hex
	Address   string `json:"address"`
	CanHost   bool   `json:"can_host"`
	FreeRAMMB int64  `json:"free_ram_mb"`
	Signature string `json:"signature"` // hex, over "peerID|name|address"
}

func (r Registration) signedBytes() []byte {
	return []byte(r.PeerID + "|" + r.Name + "|" + r.Address)
}

// Verify checks the signature and that peer_id matches the public key.
func (r Registration) Verify() error {
	if identity.PeerIDFromPublic(mustKey(r.PublicKey)) != r.PeerID {
		return fmt.Errorf("peer_id does not match public key")
	}
	sig, err := hex.DecodeString(r.Signature)
	if err != nil {
		return fmt.Errorf("bad signature encoding")
	}
	if !identity.Verify(r.PublicKey, r.signedBytes(), sig) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

func mustKey(hexKey string) []byte {
	b, _ := hex.DecodeString(hexKey)
	return b
}

// Sign fills r.Signature using id (helper for agents and tests).
func (r *Registration) Sign(id *identity.Identity) {
	r.PublicKey = id.PublicKeyHex()
	r.PeerID = id.PeerID
	r.Signature = hex.EncodeToString(id.Sign(r.signedBytes()))
}

// Server is the coordinator HTTP service.
type Server struct {
	mu       sync.Mutex
	peers    map[string]Registration
	lastSeen map[string]time.Time
	Auth     *hosting.Authority
	TTL      time.Duration // peer considered gone after this without heartbeat
}

// NewServer creates a coordinator with a 90s peer TTL.
func NewServer() *Server {
	return &Server{
		peers: map[string]Registration{}, lastSeen: map[string]time.Time{},
		Auth: hosting.NewAuthority(nil), TTL: 90 * time.Second,
	}
}

// Handler returns the coordinator's HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("GET /peers", s.handlePeers)
	mux.HandleFunc("POST /lease/acquire", s.handleLeaseAcquire)
	mux.HandleFunc("POST /lease/release", s.handleLeaseRelease)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var reg Registration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if err := reg.Verify(); err != nil {
		writeJSON(w, 403, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	s.peers[reg.PeerID] = reg
	s.lastSeen[reg.PeerID] = time.Now()
	s.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Registration{}
	cutoff := time.Now().Add(-s.TTL)
	for id, reg := range s.peers {
		if s.lastSeen[id].After(cutoff) {
			out = append(out, reg)
		}
	}
	writeJSON(w, 200, map[string]any{"peers": out})
}

// LivePeers returns currently-live registrations (used in-process too).
func (s *Server) LivePeers() []Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Registration{}
	cutoff := time.Now().Add(-s.TTL)
	for id, reg := range s.peers {
		if s.lastSeen[id].After(cutoff) {
			out = append(out, reg)
		}
	}
	return out
}

type leaseRequest struct {
	WorldID    string `json:"world_id"`
	PeerID     string `json:"peer_id"`
	Token      uint64 `json:"token"`
	TTLSeconds int    `json:"ttl_seconds"`
}

func (s *Server) handleLeaseAcquire(w http.ResponseWriter, r *http.Request) {
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WorldID == "" || req.PeerID == "" {
		writeJSON(w, 400, map[string]string{"error": "world_id and peer_id required"})
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	lease, err := s.Auth.Acquire(req.WorldID, req.PeerID, ttl)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, lease)
}

func (s *Server) handleLeaseRelease(w http.ResponseWriter, r *http.Request) {
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	ok := s.Auth.Release(req.WorldID, req.PeerID, req.Token)
	writeJSON(w, 200, map[string]bool{"released": ok})
}
