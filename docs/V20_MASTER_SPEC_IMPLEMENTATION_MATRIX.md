# Ver20 master specification implementation matrix

Updated: 2026-10-23

| Requirement | Implementation | State |
|---|---|---|
| Preserve real Ver18 runtime | Existing agent, engine, outputs, quarantine, NURSF, auth, rollout and package paths retained | Implemented |
| Universal structure discovery | `internal/nupire/discovery.go` | Implemented with bounded evidence-backed hypotheses |
| Deterministic safe compiler | `internal/nupire/compiler.go` | Implemented; declarative JSON only |
| Evidence-gated lifecycle | `internal/nupire/lifecycle.go` | Implemented with atomic persistence and audit |
| Held-out evaluation | Lifecycle evidence policy and NAPI bridge | Implemented; training and held-out IDs must be disjoint |
| Shadow/canary/promotion/rollback | Lifecycle transitions and `/api/v1/nupire` actions | Implemented; production rollout remains separately gated |
| NAPI integration | `internal/controlplane/nupire_napi_bridge.go` | Implemented without copying raw evidence |
| Controller API | `internal/controlplane/nupire_api.go` | Implemented with existing authentication/RBAC/CSRF/audit |
| Operator workspace | NUPIRE/EGPE page in `web/app.js` | Implemented with real persisted state and explicit empty/unavailable states |
| Unsupported/unstructured input | partial/abstain plus existing exact-raw quarantine/replay | Implemented safety path; universal semantics are not claimed |
| Runtime isolation | Candidate work remains off the client hot path | Implemented architecture boundary |
| 100 repository legal/research review | `V20_RESEARCH_AND_LICENSE_LEDGER.md` and batch evidence | Complete: 100 distinct source-level reviews; incomplete and duplicate candidates excluded |
| Full RPM/Rocky release validation | CI/release workflow | Pending Linux/container execution |
| Reproducible signed release | Existing release pipeline with Ver20 identity | Pending all acceptance gates |

See [V20_NUPIRE_ARCHITECTURE.md](V20_NUPIRE_ARCHITECTURE.md) for the data and
trust boundaries and [V20_ACCEPTANCE_CHECKLIST.md](V20_ACCEPTANCE_CHECKLIST.md)
for the release decision.
