# NLI v20 release infrastructure

## Release 20.0.0-2

Release 2 keeps the controller and live synchronization path available while
it reconciles parser records created by earlier releases. Malformed historical
records are isolated and reported with actionable diagnostics rather than
terminating the complete parser catalog response.

The Quarantine Resolution Wizard now presents independent review, exact-bundle
rollout, origin-client acknowledgement, replay, and verification as one
ordered completion path. Each blocker includes the responsible role, required
action, and the canonical evidence that will unlock the next stage. Sidebar
sections place Parser Management immediately below Assurance & Release and use
canonical pending counters for actionable work only.

### Operator acceptance for release 2

1. Upgrade a Release 1 controller containing active, draft, rejected, and legacy parser records. Confirm the controller remains healthy, the catalog lists valid records, and isolated records include a recoverable diagnostic.
2. Complete Quarantine Resolution Wizard Steps 7–9 with two authorized users: submit and independently approve the parser, publish it, propose the exact bundle to the origin client, independently approve rollout, wait for the exact applied hash, replay, and verify the retained record outcome.
3. Confirm the creator cannot approve their own parser or rollout and replay remains blocked before the origin client acknowledges the exact bundle hash.
4. Confirm sidebar pending counters match canonical actionable records and clear after approval, cancellation, acknowledgement, replay, or resolution.

## Release 20.0.0-1

Release 1 completes quarantine recovery after candidate generation. The
Quarantine Resolution Wizard now duplicates the canonical Test, Review,
Publish, scoped Fleet Sync, rollout approval, exact acknowledgement, replay,
verification, rollback, and offboarding controls in one workspace. Its next
action is derived from persisted candidate, proposal, client, and replay state.

The dedicated Quarantine Resolution Wizard presents the same governed APIs as
a persistent nine-stage operator flow with per-field schema, mapping,
validation, evidence, confidence, and status controls. It blocks unresolved
fields and requires explicit confirmation of ambiguous delimiter boundaries.

Parser publication and removal create an exact bundle snapshot and an
independently approved rollout proposal. Origin, selected-client, and
all-eligible scopes are explicit. A stale proposal is rejected, and replay
remains blocked until the target client's desired hash, active hash, and
applied status all match the current bundle.

The release gate exercises representative structured and unstructured records,
exact normalized/quarantine accounting, all primary workspaces and visible
inner tabs, the real controller APIs, reproducible RPM creation, and Rocky 9
and Rocky 10.2 upgrade lifecycles. See `V20_R1_TEST_RESULTS.md` in the release
archive for the verified commands, sample classes, and results.
See `V20_QUARANTINE_RESOLUTION_WIZARD.md` for the operator workflow, exact
unstructured fixture, data boundaries, deployment checks, and rollback path.

### Operator acceptance for release 1

1. Upgrade both controller and clients to `20.0.0-1`; confirm the Monitoring → Pipeline VALIDATE row shows measured attempt/pass/failure rates.
2. Put representative JSON, key/value, syslog, delimiter, positional, multiline, and deliberately unstructured records into a clean input. Confirm `read = normalized + quarantined` and that restart does not duplicate output.
3. Open Quarantine Resolution Wizard for a retained quarantine record. Complete evidence, mapping, preview, candidate generation, independent parser review, publication, origin and all-eligible rollout proposals, independent rollout approval, exact client acknowledgement, replay, verification, rollback, and offboarding checks without leaving the guided workflow.
4. Confirm the proposal creator cannot approve their own rollout, a stale bundle proposal is rejected, and replay remains disabled until the origin reports the exact applied bundle.

## Ver19 baseline: 19.0.0-8

The Ver19 baseline added authoritative validation telemetry, non-empty NUPIRE/EGPE
rendering, and the nine-stage field-mapping and candidate-generation wizard.

## Ver19 capability history

Release 6 adds bounded UPES failure-signature aggregation and the Guided Parser
Repair Studio (GPRS) candidate checksum, withdrawal, independent-review,
publication, and rollback lifecycle. UPES retains only normalized structural digests and bounded
metadata in memory; it does not retain raw records or paths, and its discovery
queue is not a durable or distributed queue. The GPRS-labelled candidate
workflow does not bypass the established signed bundle rollout, independent
rollout approval, client verification, or applied-hash acknowledgement.

See [the UPES/GPRS architecture and acceptance guide](../V20_UPES_GPRS_ARCHITECTURE_AND_ACCEPTANCE.md)
and its [architecture diagram](../architecture/NLI_V20_UPES_GPRS_ARCHITECTURE.svg)
for detailed boundaries and reproducible checks. These notes do not claim
release acceptance or production qualification.

### Operator acceptance for release 6

1. Verify UPES same-shape aggregation, first-seen queue behavior, signature and queue bounds, concurrency, aggregate-only metrics, and in-memory restart semantics using the guide's synthetic checks.
2. Create, validate, submit, independently approve, publish, withdraw, and roll back synthetic Assurance parser candidates. Verify checksum mismatch rejection at approval and publication.
3. Confirm publication and rollback update the desired parser bundle only through the normal signed rollout and independent approval path, then verify client bundle-hash acknowledgement before declaring applied state.

## Ver19 retained history and input-path lifecycle

Release 5 makes the input-path approval stages explicit: a profile must be
submitted, independently approved, and published before it can be proposed
for rollout. Single-client, selected multi-client, and all-eligible target
modes remain supported. The rollout proposal still requires a different
authorized operator to approve it. The rollouts API
reports how many proposals are awaiting approval; approved and cancelled
proposals remain in history but no longer count as pending.

The dashboard history contract distinguishes retained canonical counters from
short-lived metadata series. `canonical_history` contains all-source read,
parsed, quarantined, and error deltas in persisted one-minute buckets retained
for up to ten years. It is derived from the latest cumulative client counters
and survives controller restart. Other `points` fields such as parse latency,
parse rate, and AI request counts come from bounded in-memory event metadata;
they are not durable customer event history. Per-client heartbeat host metrics
are current samples, and unavailable measurements stay unavailable. Metric
history does not retain raw logs, parsed payloads, or secrets. Range queries
are limited to at most 1000 buckets.

These notes describe implementation behavior; they do not claim release
acceptance, production capacity, or completed deployment qualification.

### Operator acceptance for release 5

1. Create and edit an input-path draft, submit it, approve it as a different reviewer, and publish it. Confirm rollout is rejected before publication.
2. Propose the published profile for one enrolled client and confirm the target list contains exactly that client. Also verify selected multi-client and all-eligible proposals retain their existing target modes. The proposal is pending until an independent rollout approver acts.
3. Check `GET /api/v1/rollouts` before and after approval. `pending_approval_count` must decrease while the approved proposal remains in history. Verify the audit log identifies the author and independent approver.
4. Compare dashboard canonical minute totals with client counter deltas over the same interval. Refresh or restart the controller and confirm aggregate buckets persist. Treat bounded metadata series and current heartbeat samples as separate, non-durable measurements.

## Ver19 ingestion and timeline foundation

Release 4 removes the avoidable final-row delay for line-oriented `auto` input while retaining explicit Java, Python, and Go stack-trace grouping and exactly-once restart checkpoints. It adds retained, bounded aggregate buckets for Overall Timeline History and lets operators add, edit, remove, validate, and independently approve rollouts containing one or many input paths. The UI keeps open controls mounted while canonical telemetry continues in the background.

The release tests validate counter safety and bucket representation at a simulated 10,000,000 records/second. This is not a production throughput claim; capacity still depends on deployment hardware, input format, parser cost, and destination acknowledgement.

### Operator acceptance for release 4

1. Put 49 independent single-line JSON events with unique `id` values in one `auto` input file. Confirm the normalized output contains exactly 49 records without waiting for a multiline timeout. Restart the client and confirm the count remains 49, with every `id` present once. Separately verify Java/Python/Go stack traces still form one event per trace.
2. In **Input Log Paths**, add a single absolute path and select its parser family. Add a second path, edit one, remove one, and run **Validate paths**. Save and publish the revision, request rollout to one enrolled client, obtain independent approval, and verify the client reports the applied profile revision. Repeat with multiple selected clients if available.
3. Open every relevant selector while live telemetry is arriving. Change the option and verify the selected value remains visible and the page does not navigate or replace the control before selection completes.
4. Open **Performance → Overall Timeline History**. Confirm persisted interval buckets show controller timestamps and exact received, parsed, quarantined, and error counts. Refresh the page and verify history remains; compare bucket totals with the canonical source counters for the same interval.

Capture screenshots of the 49/49 output, approved/applied input revision, selected dropdown during live updates, and timeline bucket details for the deployment record. Do not interpret absent destination acknowledgement as successful remote delivery.

## Ver19 exact-output and recovery foundation

This release fixes the reported duplicate-output and continuously increasing-count defect. The client now checkpoints every completed input prefix before a pending multiline record, makes durable local output authoritative before optional telemetry delivery, avoids replay after optional Loki, ClickHouse, or controller telemetry failures, and creates family output files only when that family receives a record.

Atomic parser generations now preserve the RPM runtime ownership boundary (`root:nli` parser packs and an `nli:nli` quarantine directory), including during first enrollment and later fleet synchronization. Installers validate bundled parsers with temporary fixtures and never append synthetic events to live input paths, so a fresh deployment reports only operator-provided records.

The release also adds an audited quarantine recovery workflow from evidence review through parser drafting, testing, independent approval, signed fleet rollout, client bundle-hash acknowledgement, targeted replay, and verification. Raw evidence can be viewed, exported, excluded, restored, or purged under role-based permissions without silently changing production parser behavior.

Operational additions include queued SMTP and PagerDuty notifications, persistent delivery history, a live Client → Controller → WebApp communication-status view, stable background synchronization, browser branding, corrected timestamps, and explicit subsystem names and execution order for NHLE, NURSF, NAPI, NAL, NUPIRE, and EGPE.

Use `V20_PRODUCTION_ACCEPTANCE_TESTS.md` for the exact 49-record acceptance test and `V20_OPERATOR_DEMO_GUIDE.md` for the complete tab-by-tab demonstration. `V20_SUBSYSTEM_ORCHESTRATION.md` explains which subsystems run on ordinary traffic and which run only during evidence-gated recovery.

This directory documents the supported V20 release path. It builds source-derived static Linux binaries in a digest-pinned Go image, creates genuine RPMs with `rpmbuild` in a digest-pinned Rocky image, performs a two-pass byte-for-byte reproducibility check, and emits checksums, SPDX SBOM, release manifest, binary provenance, and an in-toto/SLSA provenance statement.

## Safety and prerequisites

- Release input must be one clean, committed, frozen Git revision. Dirty or untracked source is refused.
- Outputs go to `NLI_BUILD_SCRATCH` or an explicit output directory; the repository `dist` directory is never used.
- `nli_clinet_ver20.rpm` is a byte-identical filename alias of `nli_client_ver20.rpm`. Both contain the same `nli-client` NEVRA and must not be treated as separate packages.
- Release 2 acceptance uses the genuine published Ver20 Release 1 client and controller RPMs, then proves upgrade, rollback, re-upgrade, restart, state preservation, and uninstall behavior on Rocky Linux 9 and Rocky Linux 10.2.
- The release remains unsigned unless an explicit signing key is supplied. Unsigned output must never be described as signed.
- Operators receive one `nli-v<version>-<release>-x86_64.tar.gz` archive plus its external `.sha256`. The archive contains `RELEASE_ID`, all RPM/web/operator artifacts, and the complete internal checksum manifest. It must be extracted into a fresh versioned directory.

## Commands

```bash
export NLI_BUILD_SCRATCH=/absolute/session-owned/scratch
make release-static
make release-package-test
make release-lifecycle-test
make release-preflight
make release-build OUTPUT_DIR="$NLI_BUILD_SCRATCH/nli-v20-release"
make release-rocky \
  OUTPUT_DIR="$NLI_BUILD_SCRATCH/nli-v20-release" \
  PREVIOUS_DIR=/absolute/path/to/genuine/previous-rpms
```

Windows PowerShell uses the same pinned two-pass build:

```powershell
$env:NLI_BUILD_SCRATCH = 'C:\absolute\session-owned\scratch'
.\packaging\build-release.ps1 -OutputDirectory "$env:NLI_BUILD_SCRATCH\nli-v20-release"
```

## Signing hooks

Unsigned reproducibility is checked before signing because signatures intentionally add nondeterministic key material and timestamps. Run Rocky acceptance against the verified unsigned bytes, then sign for promotion. RPM signing must be followed by evidence regeneration because it changes RPM hashes:

```bash
REQUIRE_SIGNATURES=1 RPM_GPG_NAME='NLI Release' \
  make release-sign-rpm OUTPUT_DIR="$NLI_BUILD_SCRATCH/nli-v20-release"
make release-evidence OUTPUT_DIR="$NLI_BUILD_SCRATCH/nli-v20-release"
REQUIRE_SIGNATURES=1 COSIGN_KEY=... \
  make release-sign OUTPUT_DIR="$NLI_BUILD_SCRATCH/nli-v20-release"
```

To use GPG detached signatures instead, set `GPG_KEY_ID`. Private keys are never read from the repository and are not mounted by the default build.

Verify promotion signatures with an explicit trust root:

```bash
COSIGN_PUBLIC_KEY=/absolute/path/to/cosign.pub \
  make release-sign-verify OUTPUT_DIR="$NLI_BUILD_SCRATCH/nli-v20-release"
```

## Rocky acceptance

`tests/rocky/run-matrix.sh` builds systemd-capable containers from digest-pinned Rocky Linux 9.6 and official Rocky Linux 10.2 images. It validates:
- RPM magic, digest, name/version/architecture, payload, ownership, and modes;
- byte-identical compatibility alias behavior;
- clean install, real previous-release upgrade, downgrade rollback, and re-upgrade;
- all three systemd services and `systemd-analyze verify`;
- exact 26-field SMTP CLI output;
- controller health, authenticated API, and embedded WebView;
- service restart persistence and state/config preservation on uninstall.

## Integration inputs still required

A final release run must wait until other writers stop and one integrated revision is frozen. That revision must provide:

1. compiling and passing Go source, vet, race, and fuzz gates;
2. runtime binaries reporting `20.0.0` with clean `nexora.local/nli-v20` VCS provenance;
3. exact SMTP CLI output matching `tests/release/smtp_expected.json`;
4. a complete WebView without stale `0.4.0`, `v1.0.0`, or `webview_ver1` identifiers;
5. no disabled tests; and
6. genuine previous-release client/controller RPMs for upgrade, rollback, and re-upgrade acceptance.
