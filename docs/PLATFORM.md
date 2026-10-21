# Platform Notes
Targets: Windows + Linux first, macOS by design. The agent is headless
(a Go daemon + local HTTP API); no desktop app is required to host.
Differences handled behind interfaces: process supervision (process
groups on Unix, Job Objects planned on Windows), file permissions
(identity key written 0600; on Windows ACL-equivalent is the user
profile dir), service registration (systemd unit + Windows service
wrapper documented in deployments/), Java availability (adapter
locates `java` on PATH or JAVA_HOME; Paper needs Java 21 for MC 1.21+).
Device capability is measured, not assumed: hosts need ~2GB+ free RAM
for a small Paper world; light peers can still store backups, relay,
and run verification tasks.
