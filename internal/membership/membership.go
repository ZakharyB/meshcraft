// Package membership tracks which peers belong to which world, and
// their roles. It is the local source of truth consulted by every
// authorization check; the coordinator only mirrors discovery data.
package membership

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/ZakharyB/meshcraft/internal/authorization"
)

// Member is one peer's membership in one world.
type Member struct {
	PeerID  string             `json:"peer_id"`
	Name    string             `json:"name"`
	Role    authorization.Role `json:"role"`
	Revoked bool               `json:"revoked"`
}

// Store is a concurrency-safe, JSON-persisted membership table.
type Store struct {
	mu     sync.RWMutex
	path   string
	Worlds map[string][]Member `json:"worlds"` // worldID -> members
}

// Open loads the store at path, or creates an empty one.
func Open(path string) (*Store, error) {
	s := &Store{path: path, Worlds: map[string][]Member{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parse membership: %w", err)
	}
	if s.Worlds == nil {
		s.Worlds = map[string][]Member{}
	}
	s.path = path
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Add inserts or updates a member. Owners cannot be added twice and a
// revoked peer re-added keeps no implicit rights beyond the new role.
func (s *Store) Add(worldID string, m Member) error {
	if m.PeerID == "" {
		return errors.New("peer_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.Worlds[worldID]
	for i := range list {
		if list[i].PeerID == m.PeerID {
			// never silently change the owner's role
			if list[i].Role == authorization.RoleOwner && m.Role != authorization.RoleOwner {
				return errors.New("cannot demote the world owner via Add")
			}
			list[i] = m
			s.Worlds[worldID] = list
			return s.saveLocked()
		}
	}
	s.Worlds[worldID] = append(list, m)
	return s.saveLocked()
}

// RoleOf returns the peer's effective role; revoked peers get RoleNone.
func (s *Store) RoleOf(worldID, peerID string) authorization.Role {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.Worlds[worldID] {
		if m.PeerID == peerID {
			if m.Revoked {
				return authorization.RoleNone
			}
			return m.Role
		}
	}
	return authorization.RoleNone
}

// Can is the one-call check used across the codebase.
func (s *Store) Can(worldID, peerID string, perm authorization.Permission) bool {
	return authorization.Can(s.RoleOf(worldID, peerID), perm)
}

// Revoke removes all access for a peer in a world (idempotent).
func (s *Store) Revoke(worldID, peerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.Worlds[worldID]
	for i := range list {
		if list[i].PeerID == peerID {
			if list[i].Role == authorization.RoleOwner {
				return errors.New("the world owner cannot be revoked")
			}
			list[i].Revoked = true
			return s.saveLocked()
		}
	}
	return fmt.Errorf("peer %s is not a member of world %s", peerID, worldID)
}

// Members returns a copy of the member list for a world.
func (s *Store) Members(worldID string) []Member {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Member, len(s.Worlds[worldID]))
	copy(out, s.Worlds[worldID])
	return out
}
