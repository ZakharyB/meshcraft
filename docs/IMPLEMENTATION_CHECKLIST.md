# Implementation Checklist

## Phase 0 — Architecture & validation [x]
- [x] Inspect existing repo (Node prototype -> legacy/node-prototype/)
- [x] Architecture plan + ADR-0001..0007
- [x] Identity/authorization model (internal/identity, authorization, membership)
- [x] Go-Lua API specified (docs/LUA_API.md)
- [x] Runtime adapter interface defined (internal/minecraft)
- [x] Gateway flow documented (docs/GATEWAY.md)
- [x] Test plan (docs/TEST_PLAN.md)

## Phase 1 — Local Java hosting MVP [x] (this deliverable)
- [x] Go CLI + headless agent (local HTTP API, token-protected)
- [x] Paper adapter: locate/install-check, server.properties, start/stop/restart, readiness, health, log capture, graceful shutdown
- [x] Config persistence (JSON, validated, atomic write)
- [x] Safe local backups: checksummed snapshots, rotation, verified restore
- [x] Structured logging (slog) + /status diagnostics
- [x] Tests for main workflows incl. fake-Paper process lifecycle

## Phase 2 — Peer networking [partial]
- [x] Device identities (Ed25519), enrollment + signed registration
- [x] Coordinator: discovery, heartbeat, peer listing
- [x] Invitations (hashed, expiring, single-use) + revocation
- [ ] Production tunnel behind internal/transport (tsnet) — interface + LAN impl. present
- [ ] Relay fallback implementation

## Phase 3 — Public gateway [partial]
- [x] Gateway TCP proxy, routing table, rate limits, connection caps, tests
- [ ] End-to-end test with a real vanilla client + real Paper (needs Java runtime + Paper jar in CI — harness documented)

## Phase 4 — Lua extension system [x] (core)
- [x] Runtime, manifests, permissions, events, timeouts/cancellation, examples
- [ ] Separate-process sandbox for untrusted extensions

## Phase 5 — Distributed background work [partial]
- [x] Scheduler (Go policy + Lua policy hook), task model, quotas, retries, audit
- [x] Worker handlers: snapshot replicate, backup verify, file integrity
- [ ] Cross-peer task transport wiring (scheduler is exercised in-process + via agent API today)

## Phase 6 — Desktop app [ ]  Phase 7 — Failover [partial: leases/fencing done, migration orchestration pending]  Phase 8 — Bedrock [ ]
