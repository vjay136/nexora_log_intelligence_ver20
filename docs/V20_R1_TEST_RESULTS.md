# Nexora Log Intelligence 20.0.0-1 test results

This report records source-tree validation for Release 1. Deployment acceptance
against the target fleet, storage, network, Loki, ClickHouse, identity, and
security controls remains environment-specific. The release workflow records
the immutable source revision and artifact hashes after the tag is created.

## Guided quarantine lifecycle

The controller and browser suites cover this ordered workflow:

1. Select and inspect an exact retained quarantine record.
2. Define source-backed JSON fields and resolve ambiguous delimiters.
3. Run the deterministic preview and save a bounded candidate.
4. Retest, submit, and record independent parser approval.
5. Publish the exact canonical bundle and create an approval-gated rollout.
6. Select origin, selected-client, or all-eligible delivery scope.
7. Reject creator self-approval and stale bundle snapshots.
8. Compare desired, active, and acknowledged client bundle hashes.
9. Replay only records from one acknowledged origin client, verify controller
   history, and exercise rollback and intentional parser offboarding.

The exact `Note;systemlogs_for_data` fixture remains blocked until its
semicolon mapping is explicitly confirmed. A partial fleet acknowledgement
does not enable replay when the originating client is still drifted.

## Local source gates

| Gate | Result | Evidence |
|---|---|---|
| Full Go suite | PASS | `go test ./...` with the repository's Go 1.25.4 toolchain |
| Go vet | PASS | `go vet ./...` with the repository's Go 1.25.4 toolchain |
| Focused controller lifecycle | PASS | independent rollout approval, target scopes, stale snapshot rejection, exact acknowledgement, replay, rollback, and offboarding |
| Browser UI | PASS | 62/62 Playwright Chromium scenarios, including every primary workspace, visible inner tab, and QRW stages 6–9 |
| Live controller browser E2E | PENDING TAG CI | Requires the isolated Linux controller and release credentials; mocked UI coverage is not reported as live-controller acceptance |
| Hosted WebApp contracts | PASS | 24/24 Node tests |
| Vite production build | PASS | Core production bundle generated successfully |
| JavaScript syntax | PASS | `web/app.js` and `web/ui.spec.js` |
| Release documentation contract | PASS | R1 identity, packaged architecture guide, wizard guide, and migration note |

## Publication gates

The immutable Release 1 commit must still pass the repository workflow,
reproducible RPM/evidence build, signature and checksum verification, race
suite, Rocky 9 and Rocky 10.2 install/upgrade/rollback lifecycle, and tag
workflow before the GitHub release is described as published. The local release
directory must be downloaded from that successful tag workflow and verified
against the published checksums.
