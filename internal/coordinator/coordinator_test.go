package coordinator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZakharyB/meshcraft/internal/identity"
)

func TestRegisterVerifyAndLeaseFlow(t *testing.T) {
	srv := NewServer()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	id, _ := identity.Generate()
	reg := Registration{Name: "pc1", Address: "http://127.0.0.1:4747", CanHost: true}
	reg.Sign(id)
	body, _ := json.Marshal(reg)
	resp, err := http.Post(ts.URL+"/register", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("valid registration: resp=%v err=%v", resp, err)
	}
	resp.Body.Close()

	// Tampered registration must be rejected.
	reg.Name = "attacker"
	body, _ = json.Marshal(reg)
	resp, err = http.Post(ts.URL+"/register", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("tampered registration accepted: resp=%v err=%v", resp, err)
	}
	resp.Body.Close()

	peers := srv.LivePeers()
	if len(peers) != 1 || peers[0].Name != "pc1" {
		t.Fatalf("live peers: %+v", peers)
	}

	// Lease flow over HTTP: acquire, conflict, release, re-acquire.
	post := func(path string, v any) (int, map[string]any) {
		b, _ := json.Marshal(v)
		r, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	code, lease := post("/lease/acquire", map[string]any{"world_id": "w1", "peer_id": id.PeerID, "ttl_seconds": 60})
	if code != 200 || lease["fencing_token"].(float64) != 1 {
		t.Fatalf("acquire: code=%d lease=%v", code, lease)
	}
	code, _ = post("/lease/acquire", map[string]any{"world_id": "w1", "peer_id": "someone-else", "ttl_seconds": 60})
	if code != 409 {
		t.Fatalf("conflicting acquire: code=%d", code)
	}
	code, out := post("/lease/release", map[string]any{"world_id": "w1", "peer_id": id.PeerID, "token": 1})
	if code != 200 || out["released"] != true {
		t.Fatalf("release: code=%d out=%v", code, out)
	}
	code, lease = post("/lease/acquire", map[string]any{"world_id": "w1", "peer_id": "someone-else", "ttl_seconds": 60})
	if code != 200 || lease["fencing_token"].(float64) != 2 {
		t.Fatalf("re-acquire after release: code=%d lease=%v", code, lease)
	}
}
