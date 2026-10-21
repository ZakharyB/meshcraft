# ADR-0008: Relay fallback is end-to-end encrypted, relay is a dumb pipe
Status: accepted (2026-10-20)
Decision: internal/relay. Both agents dial out to the relay and pair
via a session code = truncated SHA-256 of a shared secret. They
exchange ephemeral X25519 public keys through the relay and derive an
AES-256-GCM key (SHA-256 KDF bound to both public keys + protocol
label, direction-separated nonce prefixes, per-frame counters).
After pairing, the relay copies ciphertext frames only.
Consequences: a relay operator (or a VPS host) sees metadata
(pairing time, sizes) but no world data and no Minecraft traffic
content. Session codes are capabilities: treat the shared secret
like an invite. The relay does not replace direct connectivity;
it is the fallback when NAT defeats it.
