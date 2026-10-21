// meshcraft-agent: the headless peer agent daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ZakharyB/meshcraft/internal/agent"
	"github.com/ZakharyB/meshcraft/internal/config"
	"github.com/ZakharyB/meshcraft/internal/identity"
	"github.com/ZakharyB/meshcraft/internal/observability"
)

func main() {
	dataDir := flag.String("data", defaultDataDir(), "agent data directory")
	flag.Parse()
	log := observability.New("peer-agent")

	cfgPath := config.Path(*dataDir)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = config.Default(*dataDir)
			if err := cfg.Save(cfgPath); err != nil {
				log.Error("create default config", "err", err)
				os.Exit(1)
			}
			log.Info("created default config", "path", cfgPath)
		} else {
			log.Error("load config", "err", err)
			os.Exit(1)
		}
	}
	id, created, err := identity.LoadOrGenerate(*dataDir)
	if err != nil {
		log.Error("identity", "err", err)
		os.Exit(1)
	}
	if created {
		log.Info("generated new device identity", "peer_id", id.PeerID)
	}
	a, err := agent.New(cfg, id, log)
	if err != nil {
		log.Error("agent init", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.StartBackground(ctx)
	fmt.Printf("MeshCraft agent %q running. peer_id=%s api=http://%s\n", cfg.NodeName, id.PeerID, cfg.APIAddr)
	if err := a.Serve(ctx); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return home + string(os.PathSeparator) + ".meshcraft"
}
