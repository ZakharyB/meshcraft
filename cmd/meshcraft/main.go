// meshcraft: operator CLI for the MeshCraft peer agent.
//
// Offline commands (init, keys, world add, backup verify) work without
// a running agent; world control commands talk to the local agent API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ZakharyB/meshcraft/internal/config"
	"github.com/ZakharyB/meshcraft/internal/identity"
	"github.com/ZakharyB/meshcraft/internal/snapshots"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	data := flag.NewFlagSet("global", flag.ContinueOnError)
	dataDir := data.String("data", defaultDataDir(), "agent data directory")
	// Parse global flags that appear before the subcommand.
	args := os.Args[1:]
	for len(args) > 0 && args[0] == "--data" {
		_ = data.Parse(args)
		args = data.Args()
	}
	_ = dataDir
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	dd := *dataDir
	var err error
	switch args[0] {
	case "init":
		err = cmdInit(dd, args[1:])
	case "keys":
		err = cmdKeys(dd)
	case "world":
		err = cmdWorld(dd, args[1:])
	case "backup":
		err = cmdBackup(dd, args[1:])
	case "status":
		err = apiCall(dd, "GET", "/status", nil)
	case "extensions":
		err = apiCall(dd, "GET", "/status", nil)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`meshcraft [--data DIR] <command>

  init [--name NAME] [--coordinator URL]   create config + device identity
  keys                                     show this device's peer id + public key
  world add --id ID --dir DIR [--port N] [--version V] [--ram MB]
  world list                               list configured worlds
  world start|stop|restart --id ID         control the running agent's world
  world logs --id ID                       tail server logs via the agent
  backup create --id ID                    snapshot a world via the agent
  backup list --id ID                      list snapshots
  backup verify --id ID --snapshot SID     verify a snapshot offline (no agent needed)
  backup restore --id ID --snapshot SID    restore via the agent (world must be stopped)
  status                                   agent status (worlds, tasks, extensions)`)
}

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".meshcraft")
}

func cmdInit(dd string, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	name := fs.String("name", "my-pc", "node name")
	coord := fs.String("coordinator", "", "coordinator URL (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfgPath := config.Path(dd)
	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("config already exists at %s (edit it directly or choose another --data dir)", cfgPath)
	}
	cfg := config.Default(dd)
	cfg.NodeName = *name
	cfg.CoordinatorURL = *coord
	if err := cfg.Save(cfgPath); err != nil {
		return err
	}
	id, created, err := identity.LoadOrGenerate(dd)
	if err != nil {
		return err
	}
	fmt.Printf("initialized %s\npeer_id: %s (new identity: %v)\nconfig: %s\n", dd, id.PeerID, created, cfgPath)
	return nil
}

func cmdKeys(dd string) error {
	id, err := identity.Load(dd)
	if err != nil {
		return fmt.Errorf("no identity in %s — run `meshcraft init` first", dd)
	}
	fmt.Printf("peer_id:    %s\npublic_key: %s\n", id.PeerID, id.PublicKeyHex())
	return nil
}

func loadCfg(dd string) (*config.AgentConfig, error) {
	cfg, err := config.Load(config.Path(dd))
	if err != nil {
		return nil, fmt.Errorf("load config: %w (run `meshcraft init` first)", err)
	}
	return cfg, nil
}

func cmdWorld(dd string, args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("world subcommand required")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("world add", flag.ContinueOnError)
		id := fs.String("id", "", "world id")
		dir := fs.String("dir", "", "world directory (holds paper.jar + world data)")
		port := fs.Int("port", 25565, "server port")
		version := fs.String("version", "1.21", "minecraft version")
		ram := fs.Int("ram", 2048, "max RAM MB")
		javaPath := fs.String("java", "", "java binary path (default: PATH lookup)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" || *dir == "" {
			return fmt.Errorf("--id and --dir are required")
		}
		cfg, err := loadCfg(dd)
		if err != nil {
			return err
		}
		idn, err := identity.Load(dd)
		if err != nil {
			return err
		}
		cfg.Worlds = append(cfg.Worlds, config.WorldConfig{
			ID: *id, Name: *id, Edition: "java", Dir: *dir, JavaPath: *javaPath,
			JarPath: "paper.jar", MCVersion: *version, Port: *port,
			MaxRAMMB: *ram, MaxPlayers: 10, OnlineMode: true, BackupKeep: 5,
			OwnerPeerID: idn.PeerID,
		})
		if err := cfg.Save(config.Path(dd)); err != nil {
			return err
		}
		fmt.Printf("world %q added (dir %s, port %d)\n", *id, *dir, *port)
		return nil
	case "list":
		cfg, err := loadCfg(dd)
		if err != nil {
			return err
		}
		for _, w := range cfg.Worlds {
			fmt.Printf("%s\t%s\tport %d\tdir %s\n", w.ID, w.MCVersion, w.Port, w.Dir)
		}
		return nil
	case "start", "stop":
		fs := flag.NewFlagSet("world "+args[0], flag.ContinueOnError)
		id := fs.String("id", "", "world id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return apiCall(dd, "POST", "/world/"+args[0], map[string]string{"id": *id})
	case "restart":
		fs := flag.NewFlagSet("world restart", flag.ContinueOnError)
		id := fs.String("id", "", "world id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := apiCall(dd, "POST", "/world/stop", map[string]string{"id": *id}); err != nil {
			return err
		}
		return apiCall(dd, "POST", "/world/start", map[string]string{"id": *id})
	case "logs":
		fs := flag.NewFlagSet("world logs", flag.ContinueOnError)
		id := fs.String("id", "", "world id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return apiCall(dd, "GET", "/world/logs?id="+*id, nil)
	}
	return fmt.Errorf("unknown world command %q", args[0])
}

func cmdBackup(dd string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("backup subcommand required")
	}
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	id := fs.String("id", "", "world id")
	snap := fs.String("snapshot", "", "snapshot id")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "create":
		return apiCall(dd, "POST", "/world/backup", map[string]string{"id": *id})
	case "list":
		return apiCall(dd, "GET", "/backups?id="+*id, nil)
	case "restore":
		return apiCall(dd, "POST", "/world/restore", map[string]string{"id": *id, "snapshot": *snap})
	case "verify":
		cfg, err := loadCfg(dd)
		if err != nil {
			return err
		}
		_ = cfg
		store := snapshots.NewStore(filepath.Join(dd, "backups"))
		if *snap == "" {
			return fmt.Errorf("--snapshot is required")
		}
		if err := store.Verify(*id, *snap); err != nil {
			return fmt.Errorf("snapshot %s FAILED verification: %w", *snap, err)
		}
		fmt.Printf("snapshot %s verified OK\n", *snap)
		return nil
	}
	return fmt.Errorf("unknown backup command %q", args[0])
}

// apiCall talks to the local agent using the token in the data dir.
func apiCall(dd, method, path string, body any) error {
	cfg, err := loadCfg(dd)
	if err != nil {
		return err
	}
	token, err := os.ReadFile(filepath.Join(dd, "api.token"))
	if err != nil {
		return fmt.Errorf("cannot read agent API token — is the agent running? (%w)", err)
	}
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+cfg.APIAddr+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-MeshCraft-Token", string(token))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent unreachable at %s: %w", cfg.APIAddr, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") == nil {
		out = pretty.Bytes()
	}
	fmt.Println(string(out))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("agent returned %s", resp.Status)
	}
	return nil
}
