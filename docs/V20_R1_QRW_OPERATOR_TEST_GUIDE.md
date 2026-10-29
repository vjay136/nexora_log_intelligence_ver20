# Release 1 Quarantine Resolution Wizard operator and validation guide

This guide covers the end-to-end recovery workflow in the Quarantine
Resolution Wizard (QRW). It assumes Release 1 controller and client packages
are installed and healthy. Use synthetic, non-secret records in acceptance
environments. The current production parser remains authoritative until an
independently approved candidate is published.

## Run the browser validation

From the repository root, start the focused QRW scenarios with:

```powershell
npm --prefix web run test:ui -- --grep "QRW|Quarantine Resolution Wizard"
```

The suite uses a canonical controller mock for retained evidence, parser
revision actions, parser synchronization state, replay, and history. It checks
the nine-stage navigation and guidance, candidate review and publication,
multi-client rollout state, exact origin-client hash acknowledgement, replay,
verification, and rollback. The broader UI suite also visits every primary
navigation destination and every visible inner tab, checking for render errors
and failed controller requests.

The Release 1 record should report the exact source revision and results from
the required repository Go, vet, race, infrastructure, package, and Rocky
lifecycle gates. Browser mocks prove UI behavior only; deployment acceptance
must use real enrolled clients and the target storage and destinations.

## Operator workflow

1. Open **Quarantine Resolution Wizard** and select a retained failure whose
   raw evidence is available. Confirm its originating node and failure
   signature. If there is no retained raw body, stop parser recovery for that
   record; metadata is not a substitute for evidence.
2. Choose **Inspect** and compare the exact source line with its parser
   attempts. Use **Suggest from raw log** or define fields manually. For every
   field, review the name, type, presence/null behavior, extraction source,
   transform, timestamp/timezone handling, defaults, validation rule, preview,
   and source evidence. Run **Test all fields** before advancing.
3. Resolve each ambiguous mapping explicitly. For the release fixture below,
   keep `Note;systemlogs_for_data` unresolved until the operator confirms both
   the semicolon delimiter and which side maps to each output field. The wizard
   must block candidate generation while either interpretation is unconfirmed.
4. Run the deterministic preview against the retained record and any selected
   saved fixtures. Confirm the expected JSON and source-backed field evidence.
   A preview failure remains visible and does not change the active parser.
5. Create and save a review-gated candidate. Retest its cases, then submit it
   for review. A second authorized operator approves the candidate. Verify the
   audit trail and approved checksum before publishing.
6. Publish the approved candidate to create the desired parser bundle. In the
   **Fleet rollout proposals** panel, choose **Propose to origin client** for a
   staged rollout or **Propose to all eligible clients** for a fleet-wide
   rollout. Review the proposal's target mode and bundle hash, then have a
   different authorized operator approve it. Proposal creation does not
   deliver the bundle; approval starts the rollout. Check the complete client
   list, desired hash, applied hash, drift, and rollout outcome. Treat a
   rollout as incomplete while any required client is missing or reports a
   different hash. Fleet-wide parser publication and per-record replay are
   separate operations.
7. Replay only after the originating client reports an applied hash exactly
   equal to the candidate's desired hash and is not drifted. Use **Verify** to
   refresh reported client state when needed. The wizard's **All eligible**
   replay scope means all eligible records on the diagnosed origin client; it
   does not send replay across the fleet. The controller rechecks the target,
   status, retry limit, and acknowledgement when accepting the request.
8. Refresh verification and confirm the controller reports the record as
   resolved, the replay history records success, and normalized/quarantine
   accounting remains consistent. Confirm destination delivery separately
   from successful parsing. If output or accounting regresses, use the
   published candidate's **Rollback** action and have an independent operator
   approve its new bundle rollout proposal. **Offboard** intentionally retires
   a parser from the active bundle and also requires rollout approval.
9. If a client must be removed from service, use the site's approved client
   offboarding and enrollment-revocation procedure, then re-check the eligible
   fleet and rollout state. QRW's **Offboard** action retires a parser from
   the active bundle; it does not remove a client from fleet enrollment.

## Synthetic unstructured samples and expected states

Use this exact required sample to exercise the ambiguous delimiter gate:

```text
20250812 130035122+0100 aaa-gmailrgo009 mail 3609 0 140293768701696 Note;systemlogs_for_data
```

Before confirmation, the date, time/zone, hostname, service, PID, status, minor
PID, event type, and event name suggestions should retain evidence spans, but
the two final values should report unresolved/confirmation-required status.
Candidate generation remains blocked. After the reviewer confirms the
semicolon boundary and reruns field validation, the desired object should
include `event_type: "Note"` and `event_name: "systemlogs_for_data"`; no value
should be inferred from a field name alone.

This ordinary unstructured sample checks a delimiter-free mapping and
repeatable preview:

```text
2026-10-28T09:42:18Z mx-04 mailer[8421]: queued message id=QX-7312 recipient=ops@example.test status=deferred
```

An operator may map timestamp, host, process, PID, message ID, recipient, and
status where the preview shows exact source evidence. Expected lifecycle states
are: unresolved mapping → tested preview → draft → in review → approved →
published bundle → pending scoped rollout proposal → independently approved
rollout → client acknowledgement → replay pending → resolved and verified. A
preview-only or test-only parser must never appear as published or
acknowledged.

For a two-client acceptance fleet, deliberately leave the origin client on an
older applied hash while the other client has the desired hash. Expected state:
the fleet view reports partial acknowledgement, the next-action panel tells
the operator to wait or verify/retry rollout, and origin-record replay remains
disabled. After the origin reports the exact desired hash, replay becomes
eligible for that origin only. Verify rollback separately after a successful
replay; do not use rollback as a substitute for correcting evidence or
approval.

## Release and upgrade evidence

Record the source commit, package hashes, controller/client health, browser and
Go test output, reviewer/approver identities, published bundle checksum,
per-client acknowledgement hashes, replay result, verification result, and
rollback result in the release evidence. Follow the repository release process
in `docs/release/README.md` and the package upgrade/recovery procedure in
`docs/release/V20_MIGRATION_AND_ROLLBACK.md`. Do not report production
qualification from mocked browser scenarios alone.
