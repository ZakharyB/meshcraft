# Go <-> Lua API (version 1)

All functions live in globals created by Go. Any state-changing call is
authorized in Go against the calling extension's manifest permissions.
Errors are returned as `nil, "message"` (Lua idiom); permission errors
use the prefix `permission denied:`.

| Function | Permission | Returns | Notes |
|---|---|---|---|
| `world.get_status()` | `world:read` | table `{id, running, players_online=-1 if unknown, endpoint}` | read-only |
| `world.get_metrics()` | `world:read` | table `{cpu_percent=-1, mem_mb=-1, uptime_s}` | -1 = not measurable yet, never fabricated |
| `peers.list_available()` | `peers:read` | array of `{id, name, can_host, free_ram_mb}` | from coordinator/membership view |
| `peers.get_metrics(peer_id)` | `peers:read` | table or `nil, err` | unknown peer -> error |
| `tasks.submit(def)` | `tasks:submit` | task id string or `nil, err` | def validated in Go: known type, budget caps, world must exist |
| `tasks.cancel(task_id)` | `tasks:submit` | bool | only own extension's tasks |
| `storage.request_backup(world_id)` | `storage:backup` | snapshot id or `nil, err` | Go performs the backup; Lua cannot pick arbitrary paths |
| `events.on(name, fn)` | `events:subscribe` | — | names validated against the known event list |
| `logger.info/warn/error(msg)` | (always) | — | tagged with extension id in the agent log |

Events (payload is a table): `peer_connected`, `peer_disconnected`,
`worker_available`, `worker_unavailable`, `task_started`,
`task_completed`, `task_failed`, `backup_requested`,
`backup_completed`, `host_health_changed`, `world_started`,
`world_stopped`. Ordering: per-event-name FIFO from a bounded queue
(128); overflow drops with a logged warning, never blocks the core.
Handlers run with a deadline (default 2s); 3 consecutive failures
disable the extension until reload.
