// Package config defines and validates the agent's persisted configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WorldConfig describes one Minecraft world managed by this agent.
type WorldConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Edition       string `json:"edition"` // "java" only for now; "bedrock" rejected until Phase 8
	Dir           string `json:"dir"`
	JavaPath      string `json:"java_path"` // empty = find "java" on PATH
	JarPath       string `json:"jar_path"`  // relative to Dir, e.g. "paper.jar"
	MCVersion     string `json:"mc_version"`
	Port          int    `json:"port"`
	MaxRAMMB      int    `json:"max_ram_mb"`
	MaxPlayers    int    `json:"max_players"`
	OnlineMode    bool   `json:"online_mode"`
	PublicGateway bool   `json:"public_gateway"` // opt-in public exposure
	BackupKeep    int    `json:"backup_keep"`    // snapshot retention per world
	BackupDir     string `json:"backup_dir"`     // empty = <data>/backups/<id>
	OwnerPeerID   string `json:"owner_peer_id"`
}

// AgentConfig is the root persisted configuration.
type AgentConfig struct {
	NodeName       string        `json:"node_name"`
	DataDir        string        `json:"data_dir"`
	APIAddr        string        `json:"api_addr"` // local API listen address, loopback by default
	CoordinatorURL string        `json:"coordinator_url"`
	ExtensionsDir  string        `json:"extensions_dir"`
	Worlds         []WorldConfig `json:"worlds"`
}

// Default returns a sane starting configuration rooted at dataDir.
func Default(dataDir string) *AgentConfig {
	return &AgentConfig{
		NodeName: "my-pc",
		DataDir:  dataDir,
		APIAddr:  "127.0.0.1:4747",
		Worlds:   []WorldConfig{},
	}
}

// Validate checks the whole configuration; every rule returns an actionable error.
func (c *AgentConfig) Validate() error {
	if c.NodeName == "" {
		return errors.New("node_name must not be empty")
	}
	if c.DataDir == "" {
		return errors.New("data_dir must not be empty")
	}
	if c.APIAddr == "" {
		return errors.New("api_addr must not be empty")
	}
	seen := map[string]bool{}
	ports := map[int]string{}
	for i := range c.Worlds {
		w := &c.Worlds[i]
		if w.ID == "" {
			return fmt.Errorf("world #%d: id must not be empty", i)
		}
		if seen[w.ID] {
			return fmt.Errorf("duplicate world id %q", w.ID)
		}
		seen[w.ID] = true
		if w.Edition != "java" {
			return fmt.Errorf("world %q: edition %q is not supported yet (only \"java\")", w.ID, w.Edition)
		}
		if w.Dir == "" {
			return fmt.Errorf("world %q: dir must not be empty", w.ID)
		}
		if w.Port < 1 || w.Port > 65535 {
			return fmt.Errorf("world %q: port %d out of range", w.ID, w.Port)
		}
		if other, dup := ports[w.Port]; dup {
			return fmt.Errorf("worlds %q and %q share port %d", other, w.ID, w.Port)
		}
		ports[w.Port] = w.ID
		if w.MaxRAMMB < 256 {
			return fmt.Errorf("world %q: max_ram_mb %d is below the 256MB minimum for Paper", w.ID, w.MaxRAMMB)
		}
		if w.BackupKeep < 1 {
			w.BackupKeep = 5
		}
		if w.JarPath == "" {
			w.JarPath = "paper.jar"
		}
	}
	return nil
}

// World returns the world config with the given id.
func (c *AgentConfig) World(id string) (*WorldConfig, bool) {
	for i := range c.Worlds {
		if c.Worlds[i].ID == id {
			return &c.Worlds[i], true
		}
	}
	return nil, false
}

// Path is the canonical config file location inside dataDir.
func Path(dataDir string) string { return filepath.Join(dataDir, "config.json") }

// Load reads and validates the config at path.
func Load(path string) (*AgentConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c AgentConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save validates and atomically writes the config (temp file + rename).
func (c *AgentConfig) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
