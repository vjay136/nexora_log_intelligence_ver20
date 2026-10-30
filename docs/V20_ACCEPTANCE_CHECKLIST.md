# NLI Ver20 acceptance checklist

Updated: 2026-10-30

Current package identity: `20.0.0-3`. Release 3 adds terminal-state badge reconciliation, operator-named Step 2 field mappings, live nine-stage status, stale-case prevention, and explicit legacy catalog/runtime identity repair. This note does not mark deployment-specific production acceptance.

| Gate | State | Evidence or remaining action |
|---|---|---|
| Operator and manager demo guide covers current navigation and sync behavior | PASS | `docs/V20_OPERATOR_DEMO_GUIDE.md`; `tests/v20-operator-guide.test.js` checks `NAV` IDs/titles |
| Parser catalog compatibility and 530 recovery | PASS | Focused store/controller suite covers legacy conversion, malformed-record isolation, bounded read retry, no write retry, approval gating, and publication rollback. |
| GPRS checksum, withdrawal, independent approval, publish, rollback, and offboarding lifecycle | PASS | Focused controller lifecycle and browser QRW tests pass. Signed rollout/client acknowledgement remains governed by independent approval and exact-hash gates. See `docs/V20_R1_QRW_OPERATOR_TEST_GUIDE.md`. |
| Quarantine Resolution Wizard field mapping and guided projection | PASS | Exact required fixture, unresolved delimiter gate, per-field evidence, deterministic candidate generation, controller persistence, nine stages, Back/Next controls, and invalid/resource-exceeding candidate rejection are covered. See `docs/V20_QUARANTINE_RESOLUTION_WIZARD.md`. |
| Release 2 upgrade baseline preserved | PASS | Release 3 lifecycle CI requires the genuine published Ver20 Release 2 client and controller RPMs. |
| NUPIRE discovery/compiler/lifecycle | PASS | Unit, persistence, API, integration, and browser tests |
| Declarative artifact and resource limits | PASS | Compiler validation and stable error-code tests |
| Held-out evidence separation | PASS | Lifecycle and NAPI bridge tests |
| Approval/canary/promotion/drift/rollback | PASS | State-machine and controller API tests |
| Existing hot path and signed rollout preserved | PASS | Complete Go suite and architecture boundary review |
| Full Go suite and vet | PASS | `go test ./...` and `go vet ./...` passed with the repository's Go 1.25.4 toolchain |
| Browser suite | PASS | 66/66 Chromium scenarios passed across every primary workspace and visible subtab, including QRW operator field naming, terminal badge clearing, catalog compatibility/repair, sidebar order, and canonical badges. |
| Hosted deployment contracts | PASS | 24/24 Node contracts; standalone and Vite production builds passed. |
| Fuzz target | PASS | 110,438 executions / 3 seconds |
| Shell and installer contracts | PASS | Syntax, client, AWS/controller edge, provenance, SELinux fixture |
| Race detector | PASS | Complete Linux `go test -race ./...` passed in the pinned Go container. |
| 100 distinct upstream source reviews | PASS | 100 verified across the consolidated ledger and evidence batches; mirrors, aliases, duplicates, and incomplete inspections excluded |
| Deterministic RPM build and verification | PENDING | Runs from the frozen Release 3 commit in local release build and tag CI |
| Rocky 9/10.2 fresh install, upgrade, rollback | PENDING | Runs from the frozen Release 3 artifacts against genuine Release 2 RPMs in tag CI |
| Two-node TLS/enrollment lifecycle | PENDING | Linux release integration gate |
| Reproducible artifact comparison | PENDING | Two-pass comparison runs from the frozen Release 3 commit |
| Same-host legacy/Ver20 parser microbenchmark | PASS | Median hot-path timings level; see `evidence/V20_V18_LOCAL_PARSER_BENCHMARK.md` |
| Linux production throughput qualification | PENDING | Pinned workload required before production performance claim |

Release decision: **source release approved for tagging by the operator** after
the complete Go, browser, vet, Vite, and static packaging gates pass. Linux RPM
reproducibility, Rocky install/upgrade, race, multi-node TLS, and production
throughput qualification remain blocked or pending and must pass before the
release is described as production-qualified.
