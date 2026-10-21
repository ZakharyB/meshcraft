# ADR-0004: Public gateway is a separate, opt-in TCP proxy
Status: accepted (2026-10-20)
Decision: `cmd/gateway` is a standalone process an operator runs on a
reachable host. It maps public endpoint -> world -> active host
(registered only with a valid fencing token), enforces per-IP rate
limits and connection caps, and proxies raw TCP to the host's Paper
port. It holds no world data and no owner keys. Public exposure is
off unless the owner enables it per world.
Consequences: vanilla clients need nothing installed; the privacy
model of the private network is unchanged for those who skip it.
