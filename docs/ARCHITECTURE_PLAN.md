# MeshCraft — Architecture Plan (Phase 0 output)

## Principle
One authoritative Minecraft server per world. Peers contribute background
work, storage, connectivity and relay capacity — never a split tick.
Go owns every security-critical decision. Lua customizes policy and
automation inside a validated, permission-checked API. The core runs
with zero Lua extensions installed.

## Components (this repo)
| Component | Binary / package | Responsibility |
|---|---|---|
| Peer agent | `cmd/peer-agent`, `internal/agent` | Headless daemon: world lifecycle, backups, local API, Lua host, scheduler client |
| CLI | `cmd/meshcraft` | Operator front-end to the agent's local API + offline commands (keys, config, backup verify) |
| Coordinator | `cmd/coordinator`, `internal/coordinator` | Discovery, membership signaling, host-lease arbitration. Stores NO world data |
| Gateway | `cmd/gateway`, `internal/gateway` | Optional public TCP endpoint routing vanilla Java clients to the active host |
| Paper adapter | `internal/minecraft` | Runtime adapter interface + Paper implementation (install check, properties, lifecycle, health, logs) |
| Snapshots | `internal/snapshots` | Versioned, checksummed backups; rotation; verified restore |
| Scheduler/Workers | `internal/scheduler`, `internal/workers` | Task model, built-in Go policy, quota-enforcing executor, auditable status |
| Lua runtime | `internal/luaruntime` | gopher-lua host: manifests, permissions, sandboxed API, events, timeouts |
| Identity/AuthZ | `internal/identity`, `internal/authorization`, `internal/membership` | Ed25519 device identity, roles, invites, revocation |
| Hosting leases | `internal/hosting` | Lease + monotonic fencing token; fail-closed on unvalidatable authority |

## Networking decision (see ADR-0002)
Phase 1–2 transport is authenticated HTTP/TCP between agents with the
coordinator for discovery — deliberately simple and testable. The
production NAT-traversal layer is an embedded WireGuard-class tunnel
(tsnet evaluated first) slotted behind `internal/transport`'s
interface; no hand-rolled crypto anywhere. Direct-first, relay-fallback
semantics are preserved at the interface level so swapping the tunnel
implementation does not touch hosting, scheduler or Lua code.

## Data flow (hosting a world)
1. Owner creates world → config + membership persisted locally (owner key signs config bundle).
2. A peer requests hosting → coordinator grants a lease with fencing token N+1.
3. Host restores latest verified snapshot, starts Paper via the adapter.
4. Players connect via private peer network, or via the public gateway (opt-in).
5. On stop/crash: server is coordinated to a safe state (`save-all flush` + graceful stop, or stopped-world snapshot), snapshot is checksummed, replicated to worker peers.
6. Failover (Phase 7): a new host may take over only with a higher fencing token and a verified snapshot; a partitioned old host's token is stale, so it fails closed.

## Trust boundaries
- Coordinator: trusted for liveness/discovery only; cannot forge ownership (owner signatures) or read world data.
- Worker peer: receives only the data a task requires; results are checksum-verified by Go.
- Lua script: untrusted; capability API only, permissions declared in manifest and enforced in Go.
- Gateway: untrusted network edge; rate-limited, routes only registered world endpoints, never sees control-plane credentials.

## Acceptance criteria for the Phase 1 slice (this deliverable)
- `go build ./...` and `go vet ./...` clean; `go test ./...` green.
- CLI can init identity/config, configure a world, start/stop a (fake or real) Paper process, take + verify + restore a backup — all locally, documented in README.
- A Lua example extension runs against the real API; a looping script times out; a script without permission is denied — all covered by tests.
- Gateway proxies a real TCP stream to a backend server process in tests.
- Two agent processes enroll with a coordinator and exchange heartbeats in the e2e test.
