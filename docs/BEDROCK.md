# Bedrock (planned, Phase 8)
Not supported yet. Design notes in ADR-0007. Bedrock uses UDP/RakNet
on 19132, a different server binary with its own EULA/distribution
terms, and different auth. It will ship as `internal/minecraft/bedrock.go`
behind the same RuntimeAdapter interface plus a UDP gateway path.
Java and Bedrock clients cannot join the same world without a
translation layer (e.g. Geyser), which would be an optional plugin,
never an assumption.
