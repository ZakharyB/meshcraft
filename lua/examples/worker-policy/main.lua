-- Worker Policy Helper: keeps a count of host-capable peers.
host_capable = 0
events.on("worker_available", function(ev)
  local list, err = peers.list_available()
  if not list then
    logger.warn("peers.list_available failed: " .. tostring(err))
    return
  end
  local n = 0
  for i = 1, #list do
    if list[i].can_host then n = n + 1 end
  end
  host_capable = n
  logger.info("host-capable peers: " .. n)
end)
logger.info("worker-policy ready")
