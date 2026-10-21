# Test harness
`fakejava.sh` emulates a Paper server process for tests: prints the
real Paper readiness line, writes world files, honours `save-all
flush` and `stop` on stdin. Used by the adapter + e2e tests so CI
needs no Java and no Paper download.
Real-Paper check (manual): install Java 21, download a Paper jar from
papermc.io into a world dir as `paper.jar`, set the world's
`java_path`/`jar_path`, then `meshcraft world start` and join with a
vanilla client at the gateway address.
