// meshcraft-coordinator: discovery + lease arbitration service.
// Stores no world data; safe to run on a small VPS or any peer.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/ZakharyB/meshcraft/internal/coordinator"
	"github.com/ZakharyB/meshcraft/internal/observability"
)

func main() {
	addr := flag.String("addr", ":4748", "listen address")
	flag.Parse()
	log := observability.New("coordinator")
	srv := coordinator.NewServer()
	fmt.Printf("MeshCraft coordinator listening on %s\n", *addr)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
