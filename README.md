# MeshCraft

Decentralized Minecraft hosting: run a world on your own device, let
trusted peers help with backups/storage/relay, keep ownership with the
owner's key — no port forwarding for private play, an optional public
gateway for vanilla clients.

**Architecture principle:** one authoritative Minecraft server per
world. Peers contribute background work, storage and connectivity —
the simulation is never split across machines. Go is the trusted core;
Lua is a sandboxed, permission-checked extension layer; the core runs
fine with no extensions at all. Java Edition (Paper) is supported;
Bedrock is a planned adapter (see `docs/BEDROCK.md`), not a claim.

Design docs: `docs/ARCHITECTURE_PLAN.md`, ADRs in `docs/adr/`,
Lua API in `docs/LUA_API.md`, threat model in `docs/THREAT_MODEL.md`,
progress in `docs/IMPLEMENTATION_CHECKLIST.md`.

## Quick start (exact commands)

Prerequisites: Go 1.23+. For a real server: Java 21+ and a Paper jar
from [papermc.io](https://papermc.io) (MeshCraft does not bundle one).

```bash
# build everything
./scripts/build.sh

# 1) initialize this device (identity + config)
./dist/meshcraft init --name my-pc
./dist/meshcraft keys                # your peer_id + public key

# 2) register a world (dir must contain paper.jar)
./dist/meshcraft world add --id survival --dir ~/worlds/survival --port 25565 --version 1.21 --ram 2048

# 3) run the headless agent (separate terminal)
./dist/meshcraft-agent --data ~/.meshcraft

# 4) drive it
./dist/meshcraft status
./dist/meshcraft world start --id survival
./dist/meshcraft world logs  --id survival
./dist/meshcraft backup create --id survival     # safe while running (save-off/flush coordinated)
./dist/meshcraft backup list   --id survival
./dist/meshcraft backup verify --id survival --snapshot <SNAPSHOT_ID>   # offline, no agent needed
./dist/meshcraft world stop    --id survival
./dist/meshcraft backup restore --id survival --snapshot <SNAPSHOT_ID>  # world must be stopped
```

**Verified with real Paper:** the end-to-end test in
`tests/realpaper_test.go` boots a real Paper 1.21.1 server through
the agent and speaks the actual Minecraft protocol to it — status
ping and login probe, directly and through the gateway, plus a
verified backup of the running server. Setup (Java 21, Paper jar,
Mojang cache seed) is documented step-by-step in
`tools/test-harness/REAL_PAPER.md`. Probe any running server with
`./dist/mc-ping <host:port>`.

No Java handy? The test harness emulates Paper:
`tools/test-harness/fakejava.sh` is what CI uses — pass it as
`world add --java /path/to/fakejava.sh` with any file named
`paper.jar` in the world dir.

## Relay fallback (when direct/NAT fails)

```bash
./dist/meshcraft-relay --addr :4749   # on any reachable host
```

Agents pair through the relay with a session code derived from a
shared secret and encrypt end-to-end (X25519 + AES-256-GCM); the
relay forwards ciphertext it cannot read (ADR-0008, `internal/relay`).

## Failover

If a host dies, an authorized peer takes over: it must hold the host
role, the dead host's coordinator lease must have expired
(takeover under a valid lease is refused — no split-brain), then it
restores the newest verified snapshot and starts under a higher
fencing token. Covered end-to-end in `tests/failover_test.go`.

## Multi-peer (private network, LAN today)

```bash
# on any reachable machine (or a small VPS):
./dist/meshcraft-coordinator --addr :4748

# in each agent's config.json set "coordinator_url": "http://<host>:4748"
# agents announce signed registrations + heartbeats automatically.
```

Invitations are hashed, expiring, single-use, and can never grant
ownership (`internal/authorization`); revocation is instant and local
(`internal/membership`). Host authority uses leases with monotonic
fencing tokens (`internal/hosting`) — a stale host fails closed.

## Public gateway (optional, opt-in)

Lets unmodified Java clients join via an ordinary server address:

```bash
./dist/meshcraft-gateway --addr :25565 --world survival \
  --target <active-host-mesh-address>:25565 \
  --holder <holder-peer-id> --token <current-fencing-token>
```

Routes are accepted only for the current lease holder; per-IP rate
limits and a connection cap apply. See `docs/GATEWAY.md` for the
bandwidth reality check.

## Lua extensions

Drop a folder with `manifest.json` + entry script into
`<data>/extensions/` and restart (or reload) the agent. Examples in
`lua/examples/` — backup-scheduler (auto-backup on world stop),
event-logger, worker-policy, lifecycle-hooks — all run against the
documented API in `docs/LUA_API.md` and are covered by tests.
Scripts get no `os`/`io`/`package`; every capability is
permission-checked in Go; handlers run under a deadline and a script
that fails repeatedly is disabled, never fatal.

## Tests

```bash
./scripts/test.sh        # gofmt check + vet + go test ./...
go test -race ./...      # what CI runs
```

Integration coverage includes: a real (fake-Paper) server process
through start → ready → backup → stop → restore; two agents +
coordinator enrollment and discovery; gateway proxying with lease
verification; Lua sandbox escapes, permission denials, infinite-loop
timeout, and handler-failure disablement; snapshot corruption
detection. CI: `.github/workflows/ci.yml`.

## Layout

```
cmd/            meshcraft (CLI), peer-agent, coordinator, gateway
internal/       identity, authorization, membership, hosting (leases),
                minecraft (Paper adapter), snapshots, scheduler,
                workers, luaruntime, agent, coordinator, gateway,
                transport, resources, config, observability
integrations/   paper-plugin (the only Java: health heartbeat plugin)
lua/            examples/, policies/, services/
proto/          wire-format notes (v1)
tools/          test-harness (fake Paper for tests)
docs/           architecture plan, ADRs, API, threat model, checklist
legacy/         the original Node.js prototype, kept for reference
```

## Honest status

Phase 1 (local hosting MVP), the relay, failover takeover, and the
core of Phases 2/4/5 are real and tested — including a real Paper
1.21.1 server verified over the actual Minecraft protocol. Not yet
done, and not claimed: the production NAT-traversal tunnel (tsnet)
behind `internal/transport` (LAN transport today), automatic crash
detection for failover (takeover itself works), cross-peer task
transport over the wire, desktop app, Bedrock. A human joining with
the game client + Microsoft account is the one step only a real
client can perform (Mojang auth); everything up to that handshake
is protocol-verified.
The checklist in `docs/IMPLEMENTATION_CHECKLIST.md` is the source of
truth for what is and isn't finished.

License: MIT.
