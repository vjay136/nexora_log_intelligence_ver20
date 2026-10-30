# NLI Ver20 Release 3 — Test Results

Release identity: `20.0.0-3`  
Test date: 2026-10-30

## Corrective scope

- Actionable navigation badges exclude terminal `excluded`, `evidence_deleted`, and resolved quarantine records while retaining audit metadata.
- Step 2 accepts operator-defined field names and creates evidence-backed editable mappings.
- Wizard step cards derive current completion from the active diagnosis, mappings, candidate, deterministic preview, approval, rollout, acknowledgement, replay, and resolution state.
- Deterministic preview uses the currently generated desired JSON and field mapping rather than stale empty cases.
- Legacy parser records receive an explicit catalog/runtime identity mapping; incompatible records remain blocked with a repair transition.
- Parser validation reports deterministic case totals, indexes, and failure reasons. Parser authors are not offered a misleading self-approval path.

## Executed evidence

| Gate | Result | Evidence |
|---|---|---|
| Complete Go suite | PASS | `go test ./... -count=1` in the pinned Go image; command packages without direct test files remain covered by integration and lifecycle gates |
| Go static analysis | PASS | `go vet ./...` in the pinned Go image |
| Complete Chromium UI suite | PASS | 66/66 scenarios across every primary workspace, visible inner tabs, responsive layout, live synchronization, QRW, catalog compatibility, and terminal badge clearing |
| JavaScript syntax and patch whitespace | PASS | `node --check web/app.js`; `git diff --check` |

The complete browser, Go, and hosted-WebApp source gates passed. Release-static, reproducibility, RPM, and Rocky lifecycle results are recorded by the final local release build and tag workflows. A source-level pass is not a deployment-specific throughput or production acceptance claim.
