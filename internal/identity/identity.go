// Package identity implements Ed25519 device identities.
//
// A device identity is a long-lived Ed25519 keypair. The peer ID is the
// hex-encoded SHA-256 of the public key. Private keys are stored with
// 0600 permissions and never leave the device; signatures are used for
// coordinator registration and config-bundle verification.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Identity is a device's keypair plus its derived peer ID.
type Identity struct {
	PeerID string
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
}

// Generate creates a fresh identity.
func Generate() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Identity{PeerID: PeerIDFromPublic(pub), pub: pub, priv: priv}, nil
}

// PeerIDFromPublic derives the stable peer ID for a public key.
func PeerIDFromPublic(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:])
}

// PublicKey returns the raw public key bytes.
func (id *Identity) PublicKey() ed25519.PublicKey { return id.pub }

// PublicKeyHex returns the hex-encoded public key (used on the wire).
func (id *Identity) PublicKeyHex() string { return hex.EncodeToString(id.pub) }

// Sign signs msg with the device private key.
func (id *Identity) Sign(msg []byte) []byte { return ed25519.Sign(id.priv, msg) }

// Verify checks a signature against a hex-encoded public key.
func Verify(pubHex string, msg, sig []byte) bool {
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), msg, sig)
}

// Save persists the private key (0600) and public key under dir.
func (id *Identity) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.key"), id.priv.Seed(), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "identity.pub"), []byte(id.PublicKeyHex()), 0o644)
}

// Load reads an identity previously written by Save.
func Load(dir string) (*Identity, error) {
	seed, err := os.ReadFile(filepath.Join(dir, "identity.key"))
	if err != nil {
		return nil, err
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("identity.key has wrong length; refusing to load")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return &Identity{PeerID: PeerIDFromPublic(pub), pub: pub, priv: priv}, nil
}

// LoadOrGenerate loads the identity in dir, creating and saving one if absent.
func LoadOrGenerate(dir string) (*Identity, bool, error) {
	if _, err := os.Stat(filepath.Join(dir, "identity.key")); err == nil {
		id, err := Load(dir)
		return id, false, err
	}
	id, err := Generate()
	if err != nil {
		return nil, false, err
	}
	if err := id.Save(dir); err != nil {
		return nil, false, fmt.Errorf("save identity: %w", err)
	}
	return id, true, nil
}
