# Test Plan
Unit (Go): config validation, identity sign/verify + persistence
permissions, authorization/invite lifecycle, membership revocation,
lease fencing monotonicity + fail-closed, snapshot create/verify/
corrupt-detect/restore/rotation, scheduler policy + retry + cancel,
worker quota enforcement, Lua sandbox (timeout, permission, syntax,
handler failure isolation), gateway routing + rate limit.
Integration: fake-Paper process (tools/test-harness/fakejava.sh)
through the real adapter: start -> ready -> status -> backup ->
stop -> restore. E2E (tests/): coordinator + two agents enroll,
heartbeat, one hosts the fake server, gateway proxies a TCP stream.
Manual/CI-optional: real Paper jar + real Java client through the
gateway (requires Java 21 + network access to PaperMC; harness in
tools/test-harness/README.md). No feature is claimed from a TCP
connect alone.
