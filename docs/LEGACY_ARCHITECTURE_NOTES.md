# MeshCraft — Architecture

## 1. Roles

| Role | Holds | Can do |
|---|---|---|
| **Owner** | Owner Ed25519 private key | Create network, sign config, add/revoke peers, set election policy & resource caps |
| **Peer node** | Node keypair, signed config copy | Request open, be elected game node, hold world replicas, run offload jobs |
| **Rendezvous** | Nothing sensitive | Introduce peers by network code, relay hole-punch signals, optional encrypted relay fallback |
| **Player** | Minecraft client only | Join via the mesh address `meshcraft.local:25565` / mesh IP of the current game node |

A peer is not an owner. Running the server process ≠ controlling the
server. All rule changes arrive as owner-signed bundles and are verified
before an agent applies them.

## 2. No port forwarding — how peers actually connect

Three layers, tried in order (the Tailscale playbook, reimplemented):

1. **LAN / mDNS** — same house? Connect directly, no internet needed.
2. **UDP hole punching** — both peers ask the rendezvous for each other's
   public endpoint (STUN-discovered), then punch simultaneously.
   Works on most home NATs (cone NAT). Agent keeps the pinhole alive.
3. **Relay fallback** — symmetric NAT / strict CGNAT: traffic flows
   through a relay (self-hostable, DERP-style), end-to-end encrypted,
   so the relay sees ciphertext and metadata only.

Tunnel choice for v1: **WireGuard** (kernel, audited, tiny) with keys =
node keypairs. The agent manages peers/config; users never touch it.
A pure-Node prototype tunnel is fine for LAN testing and nothing more —
do not ship a hand-rolled crypto transport.

## 3. Sessions, leases, and election

The invariant that keeps the world safe: **exactly one writer at a time.**

- A peer sends `OpenRequest { networkId, requester, timestamp }`.
- Online peers answer with `Capability { freeRam, cpuScore, pingToRequester,
  ownerPreferenceRank, canHost: bool }`.
- Winner = highest score among nodes allowed by owner policy.
  Alone online → you win, server starts on your node.
- Winner takes a **lease** (e.g. 60s, heartbeated while the server runs).
  Lease is gossiped; a second OpenRequest joins the live session instead
  of starting a competing world.
- Owner offline changes nothing: the lease needs a quorum of *online
  permitted peers*, not the owner's machine. Owner authority is a key,
  not a running process.
- Session end (last player leaves + idle timeout, owner-configured):
  server saves, snapshot is made, lease is released.

Split-brain rule: if the mesh partitions, the side *without* lease quorum
does not start a world. Two divergent worlds cannot be merged — preventing
the fork matters more than availability in that rare case.

## 4. World replication

- Snapshot = chunked, content-addressed (SHA-256) copy of `world/`,
  `server.properties`, plugins and their configs (the signed set).
- Only changed chunks move (rsync-style delta on chunk hashes).
- Every peer keeps up to the owner's configured replica count (default:
  all full nodes). That is why any peer can host tomorrow's session:
  they already have the world.
- Snapshots are versioned and signed by the node that hosted the session;
  peers verify against the lease record before accepting one — a peer
  cannot quietly rewrite history.

## 5. Pooled compute (the honest version)

| Technique | Status | Why |
|---|---|---|
| Best-node election per session | v0.1 design, lease in prototype | Real pooling across time |
| Velocity proxy + one world per node | v2 | Real pooling at the same time: lobby / survival / creative on different PCs, one network |
| Offload jobs (backup, pre-gen, map render, voice) | v2 | Frees the game node mid-session |
| Splitting one world's tick across PCs | **Not attempted** | See `docs/LIMITS.md` |

## 6. Owner control surface

Signed config bundle contains: server.properties values, whitelist/ops,
plugin list + hashes, Java args / RAM cap per node, election policy,
idle timeout, replica count, revoked node keys.
An agent refuses unsigned or stale (lower version) bundles.
Revocation propagates the same way; a revoked node loses mesh keys and
can no longer take a lease or receive new snapshots.

## 7. Device tiers

"Any device" with honest roles:
- **Full node** — ≥4 GB free RAM desktop/laptop/mini-PC: can host.
- **Light node** — phone / low-end: play, relay, hold snapshots, run
  offload jobs. Cannot be elected (advertises `canHost: false`).
The agent decides its tier from measured resources, owner can override.
