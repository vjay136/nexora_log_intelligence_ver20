# NLI Ver20 Release 2 — Quarantine Resolution Operator Guide

This guide covers the single-workspace recovery path in **Assurance & Release → Quarantine Resolution Wizard**. The wizard uses controller-persisted evidence; a page refresh does not change the parser, rollout, client acknowledgement, or replay state.

## Roles and safety boundary

- The parser author selects evidence, maps fields, previews output, and submits a candidate.
- A different reviewer or administrator approves the parser candidate.
- An operator publishes the approved candidate and creates an exact-bundle rollout proposal.
- A different authorized reviewer approves that rollout.
- The originating client must report the exact desired bundle hash as applied with no drift before replay is enabled.
- AI remains advisory and is used only after deterministic UPES/GPRS controls. It never activates a parser directly.

The proposal creator cannot approve their own parser or rollout. Publication, rollback, and offboarding all use the same independently approved bundle path.

## Steps 1–6: evidence to tested candidate

1. **Select** one retained failure. Choose **Inspect**; the selected record and originating client remain attached to the workspace.
2. **Inspect** the exact retained raw line and failure reason. Do not paste unrelated customer data into the mapping form.
3. **Schema** defines desired JSON field names, types, required/optional state, null behavior, and validation.
4. **Map** binds each field to verified token, delimiter, named-regex group, or literal evidence. Ambiguous boundaries must be confirmed explicitly.
5. **Generate** creates a deterministic declarative parser candidate. Download the definition if a change record needs an attachment.
6. **Test** runs the retained fixture and regression cases. Continue only when the preview, field count, types, route, and evidence spans are correct.

## Step 7: review and publish

The **Next required action** panel names the exact action and role.

1. Select **Submit for review**.
2. Sign in as a different reviewer or administrator and select **Approve**.
3. An operator with rollout permission selects **Publish**.
4. Confirm **Published bundle available** and record the bundle hash. Publishing prepares a rollout proposal; it does not silently change clients.

If publication cannot materialize the bundle or persist its proposal, Release 2 automatically restores the previous catalog state and bundle. The parser must not remain falsely published.

## Step 8: roll out and replay

1. Select **Propose to origin client** for the safest first rollout, or **Propose to all eligible clients** only for an approved fleet-wide change.
2. A different rollout approver selects **Approve**.
3. Select **Verify origin acknowledgement**. The target client must report the exact desired hash as applied and `drifted=false`.
4. When acknowledgement is complete, select the retained record and choose **Request targeted replay**.

Replay stays disabled when the proposal is pending, stale, rejected, aimed at another bundle, missing the origin client, or not acknowledged by that client.

## Step 9: verify and recover

1. Refresh verification and confirm the origin record is `resolved` with a successful replay outcome.
2. Confirm the normalized record matches the desired schema and that read accounting still satisfies `read = normalized + quarantined`.
3. If results are wrong, choose **Rollback**. To remove the parser from future bundles, choose **Offboard**. Each removal creates another independently approved bundle proposal.

## Pending badges

Sidebar badges are canonical current-work counts, not notification history. They cover parser drafts/review/approved work, rollout approvals, active Assurance runs, configuration lifecycle work, and waiting/replay-failed quarantine records. Counts clear after the controller records the resolved state. An amber `!` means that specific lifecycle collection is temporarily unavailable; other counts remain usable.

## HTTP 530 recovery

HTTP 530 indicates that an upstream controller storage or proxy path did not answer successfully; it is not a valid parser state. Release 2 retries only idempotent health/read calls with a small bound. Revision-creating writes are never retried automatically because an ambiguous write response could duplicate work. The UI retains its last authoritative badge snapshot while reconnecting. If 530 persists, restore controller-to-storage reachability, then refresh; do not create duplicate proposals.

