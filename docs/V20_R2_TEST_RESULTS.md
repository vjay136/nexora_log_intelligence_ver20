# NLI Ver20 Release 2 — Test Results

Release identity: `20.0.0-2`  
Date: 2026-10-29

## Source-tree evidence

- Complete Go suite and `go vet ./...`: **PASS**. Focused follow-up also passed after the blocked-parser repair transition. Coverage includes bounded HTTP 530 read recovery, no automatic retry for revision-creating writes, legacy parser conversion, malformed parser isolation, fleet bundle materialization, proposal gating, automatic rollback on proposal failure, and canonical navigation counts.
- Complete Chromium operator suite: **PASS, 64/64**. It covers every primary workspace and visible subtab, Quarantine Resolution Wizard Steps 1–9, exact-origin bundle acknowledgement, targeted replay, rollback/offboard controls, blocked-parser repair, canonical pending badges, and Parser Management placement immediately below Assurance & Release.
- Hosted WebApp contracts: **PASS, 24/24**, with both standalone and Vite production builds passing.
- Linux race detector across all Go packages: **PASS**.
- JavaScript syntax: **PASS** for `web/app.js` and `web/ui.spec.js`.

The deterministic artifact, RPM lifecycle, Rocky 9/10.2, race, TLS, and publication workflow results are recorded by the frozen tag workflow. This source-tree note does not replace deployment-specific throughput or destination acceptance evidence and does not claim universal production accuracy.

## Regression contracts

1. A valid parser created by an earlier release is normalized into the strict current runtime pack before publication.
2. A malformed legacy parser remains visible as `runtime_sync.status=blocked` with its reason, while catalog, overview, telemetry snapshot, and parser-sync APIs stay available.
3. Parser publication is approval-gated. Failure to create the exact fleet proposal restores the prior catalog state and materialized bundle.
4. HTTP 530 is retried only for bounded idempotent health/read operations; writes remain single-attempt.
5. Wizard replay remains blocked until independent rollout approval and the originating client’s exact applied bundle acknowledgement.
6. Pending badges derive from current controller lifecycle records and clear when those records resolve.

