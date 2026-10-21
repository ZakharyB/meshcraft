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
- [x] Relay fallback: paired, end-to-end encrypted (X25519 + AES-GCM), relay reads nothing (internal/relay)

## Phase 3 — Public gateway [mostly done]
- [x] Gateway TCP proxy, routing table, rate limits, connection caps, tests
- [x] Real Paper 1.21.1 end-to-end: adapter boot, Minecraft protocol status ping + login probe directly AND through the gateway, quiesced backup of the running server (tests/realpaper_test.go)
- [ ] A human joining with the real game client + a Microsoft account (the protocol path is verified; the encrypted login itself is performed by Mojang-authenticated clients only)

## Phase 4 — Lua extension system [x] (core)
- [x] Runtime, manifests, permissions, events, timeouts/cancellation, examples
- [ ] Separate-process sandbox for untrusted extensions

## Phase 5 — Distributed background work [partial]
- [x] Scheduler (Go policy + Lua policy hook), task model, quotas, retries, audit
- [x] Worker handlers: snapshot replicate, backup verify, file integrity
- [x] Snapshot replication between agent stores via the worker framework (used by the failover test)
- [ ] Cross-peer task transport over the wire (scheduler is exercised in-process + via agent API today)

## Phase 6 — Desktop app [ ]  Phase 7 — Failover [partial: leases/fencing done, migration orchestration pending]  Phase 8 — Bedrock [ ]
