# Public Gateway
Operator runs `meshcraft-gateway` on any reachable host (VPS is fine).
Flow: host agent registers route {world_id, host_agent_addr, fencing_token}
-> gateway validates token is the highest seen for that world ->
vanilla client connects to gateway:25565 -> gateway dials the host's
Paper endpoint and proxies bytes both ways. Minecraft's own protocol
is untouched, so unmodified Java clients work. Limits: per-IP new-conn
rate (default 10/min), global concurrent cap (default 200), idle
timeout 5m. Bandwidth: roughly one Minecraft session per player
(~50-200 KB/s typical); a 100-player public world wants a real VPS,
not a home uplink — the gateway host carries all public traffic.
