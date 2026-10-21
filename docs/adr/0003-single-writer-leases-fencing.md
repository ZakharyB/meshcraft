# ADR-0003: Single-writer authority via leases + fencing tokens
Status: accepted (2026-10-20)
Decision: The coordinator issues host leases carrying a monotonically
increasing fencing token per world. A host must present a current,
unexpired token to publish snapshots or claim the gateway route.
Tokens only increase; a stale token is rejected everywhere. If lease
validity cannot be confirmed, the host fails closed (stops writing).
Consequences: split-brain produces at most a stopped world, never two
divergent writers. Failover is Phase 7; the token machinery lands now
so later phases build on tested primitives.
