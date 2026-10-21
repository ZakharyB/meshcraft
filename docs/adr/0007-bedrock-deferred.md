# ADR-0007: Bedrock is an adapter, not a flag
Status: accepted (2026-10-20)
Decision: The `minecraft.RuntimeAdapter` interface is edition-agnostic
(Install/Configure/Start/Stop/Health/Endpoint/Backup hooks). Bedrock
differs in transport (UDP/RakNet), port, auth and licensing; it gets
its own adapter implementation in Phase 8 and is reported as
"planned" everywhere until end-to-end client tests pass. No shared
"edition" boolean leaks into networking or gateway code; the gateway
will need a UDP path for Bedrock and does not pretend otherwise.
