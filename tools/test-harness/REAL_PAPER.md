# Running the real-Paper test

Verified working 2026-10-20 with Paper 1.21.1 (build 133) and
Temurin OpenJDK 21.0.12.

```bash
# 1. Java 21 (Temurin tarball or your package manager)
java -version   # must be 21+

# 2. Paper jar (Fill API; the old v2 API is sunset)
#    https://fill.papermc.io/v3/projects/paper/versions/1.21.1/builds
#    pick the latest STABLE build's server:default download URL.
#    Verify the sha256 checksum published alongside it.

# 3. Mojang server jar — Paperclip downloads this on first boot.
#    If your machine's Java cannot reach Mojang (some proxies break
#    Java's TLS tunnelling), pre-seed it instead:
#    - version URL: https://piston-meta.mojang.com/mc/game/version_manifest_v2.json
#      -> versions[1.21.1].url -> downloads.server.{url,sha1}
curl -L -o mojang_1.21.1.jar <server.url>
sha1sum mojang_1.21.1.jar   # must equal downloads.server.sha1
#    The test copies it into <worlddir>/cache/mojang_1.21.1.jar,
#    which is exactly where Paperclip looks.

# 4. Run (TMPDIR on a real disk: Paper + worlds need ~1GB temp)
MESHCRAFT_JAVA=$(which java) \
MESHCRAFT_PAPER_JAR=/path/paper-1.21.1.jar \
MESHCRAFT_MOJANG_JAR=/path/mojang_1.21.1.jar \
TMPDIR=/big/disk \
go test ./tests/ -run TestRealPaperEndToEnd -v -timeout 900s
```

The test boots real Paper through the adapter, performs the real
Minecraft protocol status ping + a login probe directly and through
the public gateway, takes a quiesced backup of the running server
and verifies it. A quick manual probe tool is in tools/mc-ping:
`mc-ping <host:port>` does the same status ping + login probe
against any running server.
