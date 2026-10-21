#!/bin/sh
# Fake "java" for tests: emulates just enough of Paper's console
# behaviour. Invoked as: fakejava.sh -Xmx... -jar paper.jar nogui
# with the world dir as CWD. Creates world/ files, prints the Paper
# readiness line, then answers console commands on stdin:
#   save-off / save-on / save-all flush -> acknowledged in log lines
#   stop -> writes a final save marker and exits 0
echo "[Server thread/INFO]: Starting minecraft server version 1.21 (fake)"
mkdir -p world
echo "fake-level-data" > world/level.dat
echo "[Server thread/INFO]: Preparing level \"world\""
echo "[Server thread/INFO]: Done (0.042s)! For help, type \"help\""
while IFS= read -r line; do
  case "$line" in
    stop)
      echo "final-save" > world/session-end.txt
      echo "[Server thread/INFO]: Stopping server"
      exit 0
      ;;
    "save-all flush")
      echo "flushed" > world/last-save.txt
      echo "[Server thread/INFO]: All dimensions saved"
      ;;
    save-off) echo "[Server thread/INFO]: Saving is now off" ;;
    save-on)  echo "[Server thread/INFO]: Saving is now on" ;;
    *) echo "[Server thread/INFO]: Unknown command (fake)" ;;
  esac
done
