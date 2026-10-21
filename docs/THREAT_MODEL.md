# Threat Model (summary)
Assets: owner private key, world data, host availability, peer devices.
Adversaries: revoked peer, malicious worker, malicious Lua extension,
internet attacker at the gateway, curious coordinator operator.
Controls: Ed25519 identities; signed config/registrations; hashed,
expiring, single-use invites; least-privilege worker tasks (no
arbitrary code, paths confined to the world/snapshot dirs); checksum
verification of every transferred artifact; gateway rate limits +
route registration only with a valid fencing token; Lua capability API
with manifest permissions, no os/io/package access; audit log of admin
actions (agent log, structured). Out of scope for MVP: malicious owner
(owners are trusted for their own worlds), physical device compromise.
