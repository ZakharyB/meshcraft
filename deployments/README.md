# Deployments
- systemd user unit for the agent (Linux): copy to
  ~/.config/systemd/user/, `systemctl --user enable --now meshcraft-agent`.
- Coordinator/gateway are plain binaries; run them under any supervisor
  on a reachable host. No Kubernetes, no managed cloud required.
- Windows: run meshcraft-agent.exe from a scheduled task or service
  wrapper (e.g. NSSM); the agent itself is headless and identical.
