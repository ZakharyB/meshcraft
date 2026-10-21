# MeshCraft

Turn any peer's device into the Minecraft server — no port forwarding,
no single always-on host, owner keeps control.

## The idea

A normal Minecraft server dies when the host's PC is off, and opening it
to friends means port forwarding, dynamic DNS, and handing out your IP.

MeshCraft replaces that with a **peer mesh**:

1. **Every peer runs an agent** on their own device (PC, laptop, mini-PC).
2. **Peers find each other without port forwarding** — a tiny rendezvous
   server introduces them, they hole-punch a direct encrypted tunnel,
   and fall back to a relay only when NAT makes direct impossible.
   (Same model as Tailscale: signaling + STUN + DERP-style relay.)
3. **Whoever wants to play sends "open server".** The online peers elect
   the best current node (free RAM, CPU, ping, owner preference) and that
   node starts the actual Paper server.
4. **Playing alone?** Your own node wins the election trivially — zero-
   latency local play. The open request is announced to the mesh, so a
   friend coming online later joins the *same already-running session*.
5. **Host not playing?** Doesn't matter. Any permitted peer can request
   an open, an online node is elected, and they play together without
   the owner. The world lives on the mesh, not on the owner's PC.
6. **Owner retains control.** The network and the server config are owned
   by one Ed25519 keypair held by the host/owner. Config, whitelist,
   plugins, resource limits and peer revocation are *signed bundles*.
   Peers can run the server, they cannot change the rules, promote
   themselves, or un-revoke themselves.

## What "using every peer's compute" honestly means

A single Minecraft world **cannot** split its tick simulation across
home PCs over the internet (one authoritative JVM, 50ms tick, shared
world state). Anyone promising that is selling you lag and world
corruption. MeshCraft pools compute in ways that actually work:

- **Election** — the strongest online node runs the world right now.
- **Sharded worlds (v2)** — a Velocity proxy spans nodes: survival on
  node A, creative / lobby / minigames on node B, players walk through
  a portal between them. That *is* multiple PCs powering one network.
- **Offload jobs** — backups, chunk pre-generation, BlueMap rendering,
  voice chat and Discord bridge run on *other* peers, not the game node.

See `ARCHITECTURE.md` for the full design and `docs/LIMITS.md` for what
is deliberately not attempted.

## Status

Prototype scaffold (v0.1). Plain Node.js, built-ins only — no npm
install, no build step, matching how this workspace builds local tools.

Working today:
- Agent dashboard + status API (CPU/RAM, lease, peers)
- Lease-based "request open" — one active game node at a time
- Peer heartbeat / announce between agents on a LAN or VPN
- Spawns a real `server.jar` if you drop one in `server/`
- Rendezvous server for network-code peer discovery (discovery only)

Stubbed / v1 (marked in code, not faked):
- UDP hole punching + encrypted tunnel (use WireGuard / libp2p here)
- Ed25519 signed config bundles (prototype uses a local owner token)
- World snapshot replication between peers

## Run it

```bash
# Terminal 1 — rendezvous (can run on any peer, or a cheap VPS)
node signaling/rendezvous.js          # :4748

# Terminal 2 — this peer's agent
cp agent/config.example.json agent/config.json   # edit name / peers
node agent/agent.js                   # dashboard on http://localhost:4747
```

Drop a Paper `server.jar` into `server/`, open the dashboard, hit
**Request Open**. With no jar present the agent grants the lease and
reports `mcRunning: false — no jar found` instead of pretending.

Run a second agent on another device (or a second port:
`AGENT_PORT=4749 node agent/agent.js`), add each other in `peers`,
and a request on one announces the open session on the other.

## Layout

```
agent/       per-peer daemon: election lease, MC process, dashboard
signaling/   rendezvous: introduces peers by network code, nothing else
server/      your Paper jar + world live here (world/ is git-ignored)
docs/        NAT traversal notes, limits, threat model
```

## License

MIT
