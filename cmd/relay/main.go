// meshcraft-relay: the encrypted relay fallback server.
// It pairs two agents by session code and pipes ciphertext between
// them; it cannot read the traffic (X25519 + AES-GCM end to end).
package main

import (
	"flag"
	"fmt"
	"net"
	"os"

	"github.com/ZakharyB/meshcraft/internal/observability"
	"github.com/ZakharyB/meshcraft/internal/relay"
)

func main() {
	addr := flag.String("addr", ":4749", "listen address")
	flag.Parse()
	log := observability.New("relay")
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Error("listen", "err", err)
		os.Exit(1)
	}
	fmt.Printf("MeshCraft relay listening on %s (pairs agents, reads nothing)\n", *addr)
	if err := relay.NewServer().Serve(ln); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
