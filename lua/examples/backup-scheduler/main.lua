-- Backup Scheduler: on world_stopped, ask Go to take a snapshot.
-- The extension never touches the filesystem itself; storage.request_backup
-- is validated and executed by the Go core.
events.on("world_stopped", function(ev)
  local snap, err = storage.request_backup(ev.world_id)
  if snap then
    logger.info("backup scheduled for " .. ev.world_id .. " -> " .. snap)
  else
    logger.error("backup request failed: " .. tostring(err))
  end
end)

events.on("backup_completed", function(ev)
  logger.info("backup completed: " .. tostring(ev.snapshot_id))
end)

logger.info("backup-scheduler ready")
