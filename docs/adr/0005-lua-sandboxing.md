# ADR-0005: Lua sandbox policy
Status: accepted (2026-10-20)
Decision: gopher-lua state created WITHOUT os/io/package/debug libs.
Scripts get only: base (minus dofile/load/loadstring), string, table,
math, coroutine — plus the MeshCraft capability API, each function
checking the extension's manifest permissions in Go. Every script call
runs under a context deadline (default 2s handlers, configurable);
gopher-lua checks context between instructions, so infinite loops are
cancelled. Event dispatch is via a bounded queue; a slow/faulty handler
is disabled after repeated failures and never blocks the core.
Honest limit: gopher-lua has no hard memory cap; a script can allocate
until the deadline fires. Truly untrusted third-party extensions
should run under the Phase-4 separate-process model (versioned local
IPC), which this API is designed to allow without script changes.
