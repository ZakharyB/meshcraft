# Limits — what MeshCraft deliberately does not do

## One world, one JVM
Minecraft's simulation (entities, chunks, redstone, pathfinding) is built
around shared in-memory state ticked 20×/second by a single server
process. Splitting that across home PCs means every tick waits on the
slowest peer and the internet, and any partition forks the world.
Regionised threading (Folia) parallelises *inside one machine*; it does
not change this. MeshCraft pools peers across sessions, worlds, and
jobs — never by slicing one world's tick.

## Hole punching is not magic
Cone NAT: direct tunnel almost always works. Symmetric NAT / double
CGNAT: direct fails and the relay carries traffic — playable, with
relay ping added. A mesh with zero reachable relays and two symmetric-
NAT peers cannot connect; the fix is one peer/VPS running the free
relay, which the roadmap ships as a one-command install.

## The world has one writer
Replication gives availability, not mergeability. Two peers playing
"the same world" separately at the same time creates two worlds.
The lease + quorum rule exists to make that impossible by construction.

## Phones are light nodes
A phone can be a peer, a relay, a backup vault and a player endpoint.
Hosting a modded Paper server for several players on one is a RAM and
thermal problem no software design removes.
