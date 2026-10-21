# ADR-0006: Snapshot consistency strategy
Status: accepted (2026-10-20)
Decision: A backup is only taken in one of two states: (a) server
stopped, or (b) server coordinated via console commands
`save-off` -> `save-all flush` -> copy -> `save-on` (the Paper plugin
will expose the same hooks). A raw copy of a running, saving server is
refused by the adapter API (`ErrServerRunning`). Every snapshot is a
tar.gz + manifest.json with per-file SHA-256, world ID, Minecraft
version, fencing token and creator identity; restore verifies the full
manifest before extracting, into a staging dir, then swaps.
Consequences: backups are provably the bytes that were hashed; corrupt
or partial snapshots are detected, never silently restored.
