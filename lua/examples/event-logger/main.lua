-- Event Logger: the minimal useful extension.
events_seen = 0
local function log_event(name)
  return function(ev)
    events_seen = events_seen + 1
    logger.info("event " .. name .. " world=" .. tostring(ev.world_id))
  end
end
events.on("peer_connected", log_event("peer_connected"))
events.on("peer_disconnected", log_event("peer_disconnected"))
events.on("world_started", log_event("world_started"))
events.on("world_stopped", log_event("world_stopped"))
logger.info("event-logger ready")
