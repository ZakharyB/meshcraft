# Protocol definitions (v1)
Wire formats are JSON over HTTP for the control plane in v1:
- Coordinator: POST /register (signed Registration, see
  internal/coordinator), GET /peers, POST /lease/acquire,
  POST /lease/release.
- Agent local API: see internal/agent Handler() routes; requires the
  per-device token header `X-MeshCraft-Token`.
- Gateway route registration is in-process/CLI in v1 (ADR-0004).
Protocol changes bump this version and the Lua API version
independently; both are recorded in the respective docs.
