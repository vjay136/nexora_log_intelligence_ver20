# NLI 20.0.0-2 Quarantine Resolution Wizard

The Quarantine Resolution Wizard is the single operator workspace for turning a
retained parsing failure into a reviewed parser revision. It reuses the existing
Assurance Lab evidence, parser registry, approval, rollout, acknowledgement,
replay, and rollback APIs. It does not create a second parser store or a shortcut
around independent approval.

## Operator workflow

The persistent step bar records the current point in the nine-stage workflow:

1. **Select** a bounded failure signature and inspect its retained samples.
2. **Inspect** the raw record, structural signature, source context, and parser
   attempts.
3. **Schema** the desired JSON fields. Add, rename, duplicate, reorder, or remove
   fields and choose type and required/optional/default/null handling.
4. **Map** every field to a token, delimiter part, named regular-expression
   group, or constant. Transform, default, and validation controls remain
   explicit.
5. **Generate** a declarative candidate. Runtime-safe field identifiers use
   letters, digits, and underscores and start with a letter or underscore.
6. **Test** all retained fixtures through the same deterministic parser runtime.
   Each field displays source evidence, preview value, confidence, and status.
7. **Review** the candidate, desired JSON, diff, fixtures, and provenance,
   submit it, and record independent parser approval in the same workspace.
8. **Roll out and replay** by proposing the exact published bundle for the
   origin client or all eligible clients, obtaining independent rollout
   approval, waiting for exact applied acknowledgement, and then replaying only
   eligible records from one origin client.
9. **Verify** controller-reported record state and replay history, normalized
   output, accounting parity, destination delivery, and live telemetry.
   Rollback and intentional offboarding stay available through the same
   approval-gated bundle lifecycle.

Replay is enabled only after the originating client's exact applied bundle acknowledgement
matches the current desired bundle hash and applied status.

The wizard also exposes evidence download, ignore, exclude/include, purge,
candidate edit, submit, approval, publish, origin or all-eligible rollout
proposal, rollout approval/cancellation, replay, rollback, and offboarding
actions when the current role has the corresponding permission.

## Required unstructured fixture

Release 1 includes the following exact reviewer fixture:

```text
20250812 130035122+0100 aaa-gmailrgo009 mail 3609 0 140293768701696 Note;systemlogs_for_data
```

The deterministic suggestion maps date, time and zone, hostname, service, PID,
status, minor PID, event type, and event name. The semicolon boundary remains
unresolved until the reviewer confirms both the delimiter and the intended
field boundary. The candidate cannot be generated while a field is unresolved,
invalid, or awaiting confirmation. No missing value is invented.

## Integration and execution order

Production traffic remains on the fast path:

```text
source reader -> multiline framing -> parser selection -> normalization
              -> validation/accounting -> route/deliver
```

A record that cannot be parsed is copied to the protected quarantine evidence
path and counted once. Recovery is asynchronous:

```text
bounded signature -> UPES/GPRS/QRW review -> candidate tests -> NAL evidence
                  -> independent approval -> signed rollout -> client ACK
                  -> bounded replay -> verification
```

NAPI, NAL, NUPIRE/EGPE, UPES, GPRS, and optional AI do not block every source
record. Deterministic suggestions and local templates run before any configured
AI provider. AI output is advisory and must pass the same deterministic tests,
approval, rollout, and acknowledgement gates.

## Data and security boundaries

- Raw log bodies stay in the protected quarantine store and are omitted from
  canonical telemetry, notifications, and normal state.
- Browser drafts use session storage and are cleared at sign-out. Persisted
  candidates use the controller API and contain reviewed fixtures and evidence
  under the existing assurance retention policy.
- Field names are validated before they become RE2 named captures. Candidate
  regexes are compiled and checked against bounded parser resource limits.
- Purge, approval, publish, replay, and rollback remain permission checked and
  audit logged.
- Empty or unverified fields block candidate generation. Parsing failure stays
  visible as quarantine rather than being converted to a misleading success.

## Installation, upgrade, and rollback

Install or upgrade the controller and client RPMs from the same Release 1
bundle. After both services are healthy, confirm the client reports the exact
published parser bundle checksum before replaying retained evidence. The
upgrade does not auto-promote drafts and does not replay old evidence.

To roll back a parser revision, use the rollback action in the wizard or the
existing signed-artifact workflow, wait for the exact client acknowledgement,
then verify normalized and quarantine accounting. To roll back the software,
follow `V20_MIGRATION_AND_ROLLBACK.md` using the previously retained package and
configuration backup. Parser and software rollback operations are independently
audited.

## Acceptance checks

Release validation covers the exact fixture above, multiline and mixed-format
samples, rejected resource-exceeding regex candidates, unique browser controls,
all primary workspaces and visible inner tabs, controller API failures, and
uncaught browser errors. The release evidence names the tested source revision;
environment-specific Loki, ClickHouse, SMTP, PagerDuty, TLS, and fleet checks
must still be completed against the target deployment.
