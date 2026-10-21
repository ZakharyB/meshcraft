-- Lifecycle Hooks: demonstrates tasks.submit with a bounded budget.
events.on("world_started", function(ev)
  local st, err = world.get_status(ev.world_id)
  if not st then
    logger.warn("get_status failed: " .. tostring(err))
    return
  end
  logger.info("world " .. st.id .. " running at " .. st.endpoint)
end)
logger.info("lifecycle-hooks ready")
