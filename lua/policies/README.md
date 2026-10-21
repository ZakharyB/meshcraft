# Lua policies
Scheduling *policy* (which peer looks best) may be expressed in Lua via
an extension; scheduling *decisions* are always validated by the Go
scheduler (internal/scheduler): budgets, quotas, task allowlist and
peer existence are enforced in Go. See docs/LUA_API.md.
