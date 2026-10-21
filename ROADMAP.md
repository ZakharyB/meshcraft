# Roadmap
The authoritative roadmap is the spec's phase plan; status lives in
docs/IMPLEMENTATION_CHECKLIST.md. Summary:
- Phase 0 architecture/ADRs — done
- Phase 1 local Java hosting MVP — done (Go CLI + agent + Paper + backups)
- Phase 2 peer networking — core done (identity, coordinator, invites,
  leases); tsnet tunnel + relay pending
- Phase 3 public gateway — proxy done + tested; real-client E2E pending Java in CI
- Phase 4 Lua extensions — done (runtime, manifests, permissions, events, examples)
- Phase 5 background work — scheduler/workers done; cross-peer transport pending
- Phase 6 desktop app — pending (agent is headless-first by design)
- Phase 7 failover — leases/fencing done; migration orchestration pending
- Phase 8 Bedrock — pending, adapter-shaped (docs/BEDROCK.md)
The original Node prototype's roadmap is preserved in legacy/node-prototype/.
