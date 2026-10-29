# Ver20 UPES and GPRS architecture and acceptance

Release identity: `20.0.0-1`. This document describes the implementation
boundaries and the acceptance checks for the bounded parser-failure discovery
and governed parser-candidate lifecycle work. It is an architecture and test
guide; it does not declare the release build, deployment, or production
qualification complete.

![UPES and governed parser-candidate lifecycle](NLI_V20_UPES_GPRS_ARCHITECTURE.svg)

## UPES: bounded failure signatures

The client observes parser-pack validation failures and unmatched, ambiguous,
truncated, oversized, or otherwise quarantined records. It sends the
`FormatFingerprint` structural digest, family, failure class, and a bounded
reason string to the in-process UPES runtime. The fingerprint normalizes values
and hashes the resulting structure; it is not a digest of the exact raw record.
UPES does not accept or store a raw record or source path. Reasons are limited
to bounded diagnostic-code characters; free-form text is replaced with
`diagnostic_detail_redacted` before retention.

The runtime maintains at most 4,096 signature entries and at most 256 pending
discovery summaries by default. While a signature remains retained, repeated
observations increment its count without adding another queue item. The runtime
evicts the oldest retained signature when full; a later recurrence can then be
retained and queued again. It counts a discovery dropped when the pending queue is full. The
queue exposes a `Next` operation for a consumer; release 6 does not claim a
distributed queue, persistent discovery history, or an autonomous discovery
worker. The client metrics endpoint exposes aggregate counts for unique
signatures, observed failures, queued and dropped discoveries, and pending
discoveries. It does not expose individual signatures or raw samples.

UPES is observational. It does not select, edit, approve, publish, activate, or
roll back a parser. Its bounded in-memory state is lost when the agent restarts.
The exact quarantined source record remains governed by the separate local
quarantine and optional encrypted-retention controls.

For the operator view, the client reports UPES availability and counters in its
authenticated heartbeat. The controller includes aggregate and per-client
counters under `subsystems.upes` in the existing `GET /api/v1/sync/snapshot`
response. Aggregate values sum reporting clients only; `clients_total` and
`clients_reporting` expose completeness. The route uses the existing
telemetry-read permission. It returns only counters and reporting/freshness
metadata, not signatures or records. Clients without UPES measurements remain
unavailable in their row; partial, delayed, stale, and unavailable states are
not filled with invented zeroes. This is a canonical snapshot view, not a query
API for the pending discovery queue or its individual summaries.

AI remains available as the final fallback. Successful records never enter the
AI discovery queue. After a deterministic failure is observed by UPES, an
enabled client AI provider can propose a declarative candidate; schema and
regression checks must pass before it is written to the candidate staging
directory. It is not registered as an active parser or assigned to the Drain
cluster. Controller AI remediation is likewise exposed only after a failed
Assurance preview, and its proposal follows the same independent review,
publication, signed rollout, and client acknowledgement gates described below.

## GPRS: Guided Parser Repair Studio

The Guided Parser Repair Studio (GPRS) is an Assurance workspace over the
existing parser-candidate API. In this release its panel guides operators to
the UPES failure observatory and retained quarantine views; candidate creation
and review stay in the existing Assurance parser workflow. GPRS is not a second
parser runtime, a separate recovery service API, or a direct route from a UPES
signature to production. A candidate can be authored in Assurance or seeded
through an existing synthesis workflow, then validated and reviewed through
these gates:

1. Create and edit keep a declarative parser candidate in draft state and bind
   its JSON definition to a SHA-256 `artifact_checksum`.
2. Submit requires passing validation and records `submitted_checksum`.
3. A different operator must approve or reject the submitted candidate.
   Approval rechecks the definition against both its submitted and current
   checksum and records `approved_checksum`.
4. Publish rechecks the approved checksum, compiles the supported definition
   into the runtime pack, and starts the existing signed parser-bundle rollout
   workflow. Publication does not itself install the artifact on clients.
5. Cancel withdraws an `in_review` submission to draft and records the actor
   and time. It is not a general delete or rollback operation.
6. Rollback accepts only an active published candidate, marks it approved and
   inactive, and rebuilds the active bundle through the existing rollout
   mechanism. The client rollout still requires its normal approval, bundle
   verification, atomic activation, and applied-hash acknowledgement.

The checksum binds the declarative parser definition. It does not certify that
the parser is semantically correct, prove all future input formats, sign an
RPM, or replace held-out validation, independent review, rollout approval, or
client-side verification. The workflow remains separate from the per-record
ingestion path. Review state is controller metadata; production client state
changes only through the existing signed fleet delivery path.

## Acceptance procedure

Run these checks on a disposable controller/client environment with synthetic
records and matching source revision. Record the release identity, test time,
operator identities, candidate ID, checksum, bundle hash, and rollout result.

### UPES checks

1. Feed two records with the same structure and different values through the
   parser failure path. Confirm both observations increment the failure count
   while the normalized structural fingerprint remains the same and only one
   discovery summary is queued while that signature remains retained.
2. Use a small runtime in the package unit test to verify signature retention
   and pending-queue bounds, oldest-entry eviction, dropped-discovery counts,
   and concurrent updates. Confirm `Next` returns the summary without a raw
   record or path.
3. Scrape the client metrics endpoint and compare its UPES aggregate values
   with the runtime counters. Verify it contains no individual fingerprint,
   source path, or log body. Restart the agent and confirm the metrics describe
   fresh in-memory UPES state; do not expect durable history.

### GPRS checks

1. Create and validate a synthetic parser draft. Confirm the stored
   `artifact_checksum` matches the canonical declarative parser JSON.
2. Submit it and confirm `submitted_checksum` is recorded. Attempt self-approval
   and confirm it is rejected; approve as a second operator and confirm the
   approved checksum still matches the submitted checksum and current parser.
3. In a test fixture, alter the definition after submission and verify approval
   fails closed. Alter it after approval and verify publication fails closed.
   Re-edit and resubmit through the supported draft flow to recover.
4. Withdraw another submitted candidate. Confirm the cancel action records the
   actor/time, returns it to draft, and does not create or activate a runtime
   pack. Confirm a non-submitted candidate cannot use the withdrawal action.
5. Publish the approved candidate and confirm it is in the desired parser
   bundle and has an associated rollout. Confirm client applied state remains
   unchanged before the existing independent rollout approval and successful
   client bundle/hash acknowledgement.
6. Roll back that active published candidate. Confirm it becomes approved but
   inactive, is absent from the rebuilt desired bundle, and removal uses the
   existing rollout approval and client acknowledgement path. Confirm an
   inactive or unpublished candidate cannot be rolled back.
7. Compare audit events to the author, independent reviewer, publisher, and
   rollback actor. Confirm lifecycle state alone never implies client
   activation, replay completion, or release acceptance.
8. Open the GPRS panel and verify it links operators to the existing failure
   and quarantine evidence views. Confirm no text implies that UPES signatures
   automatically create candidates or that GPRS activates a parser directly.
9. Run a passing single-record preview and confirm the AI action is absent.
   Run a failing preview and confirm AI appears as the final fallback. Verify
   a valid AI result is staged only and does not change the active registry or
   assign the failed Drain cluster before managed approval and rollout.

The corresponding deterministic runtime and API unit tests should pass before
deployment acceptance. Linux RPM build, reproducibility, upgrade/rollback,
race, full-system, and production-throughput gates remain independent evidence
and must be recorded separately.
