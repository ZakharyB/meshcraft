// meshcraft-gateway: optional public endpoint for vanilla Java clients.
//
// A route is only installed when the operator supplies the world's
// current lease (holder peer id + fencing token, obtained from the
// coordinator/agent). Without a route the gateway accepts nothing.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ZakharyB/meshcraft/internal/gateway"
	"github.com/ZakharyB/meshcraft/internal/hosting"
	"github.com/ZakharyB/meshcraft/internal/observability"
)

func main() {
	addr := flag.String("addr", ":25565", "public listen address")
	world := flag.String("world", "", "world id to route")
	target := flag.String("target", "", "active host Paper endpoint, host:port (reachable from the gateway)")
	holder := flag.String("holder", "", "lease holder peer id")
	token := flag.Uint64("token", 0, "current fencing token for the lease")
	flag.Parse()
	log := observability.New("gateway")

	auth := hosting.NewAuthority(nil)
	gw := gateway.New(gateway.Config{ListenAddr: *addr}, auth)
	if *world != "" {
		if *target == "" || *holder == "" || *token == 0 {
			fmt.Fprintln(os.Stderr, "with --world you must also pass --target, --holder and --token")
			os.Exit(2)
		}
		lease := hosting.Lease{WorldID: *world, HolderPeerID: *holder, Token: *token, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour)}
		if !gw.ObserveLease(lease) {
			fmt.Fprintln(os.Stderr, "lease rejected as stale")
			os.Exit(1)
		}
		if err := gw.RegisterRoute(*world, *target, *holder, *token); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		log.Info("route registered", "world", *world, "target", *target)
	} else {
		log.Warn("no route configured; gateway will refuse all connections until a route is registered")
	}
	fmt.Printf("MeshCraft gateway listening on %s\n", *addr)
	if err := gw.ListenAndServe(); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
