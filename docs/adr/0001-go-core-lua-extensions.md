# ADR-0001: Go core, Lua extensions, Java only as integration target
Status: accepted (2026-10-20)
Context: Spec §2 mandates exactly two primary languages.
Decision: All critical infrastructure in Go (stdlib-first). Lua via
gopher-lua (pure Go, no cgo, Lua 5.1 semantics) embedded in the agent.
Java appears only in `integrations/paper-plugin`, a separate artifact.
Consequences: single static binaries; Lua sandboxing is our
responsibility (see ADR-0005); no plugin can bypass Go validation.
