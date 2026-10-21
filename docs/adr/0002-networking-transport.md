# ADR-0002: Transport behind an interface; tsnet/WireGuard-class tunnel for production NAT traversal
Status: accepted (2026-10-20)
Context: Spec §3 forbids hand-rolled crypto and requires direct-first
with encrypted relay fallback, no port forwarding.
Decision: `internal/transport` defines Dial/Listen over authenticated
peer identities. Phase 1–2 implementation is TLS/HTTP between agents
for testability on LAN. Production implementation will embed tsnet
(Tailscale) — evaluated ahead of Pion ICE and raw WireGuard because it
bundles NAT traversal + DERP-style relay + key management in one
audited stack, and a self-hostable control alternative (Headscale)
exists. Pion remains the fallback if tsnet licensing/embedding
constraints bite.
Consequences: hosting/scheduler/Lua code never depends on the tunnel
choice. Public-address play never depends on the tunnel: the gateway
(ADR-0004) covers vanilla clients.
