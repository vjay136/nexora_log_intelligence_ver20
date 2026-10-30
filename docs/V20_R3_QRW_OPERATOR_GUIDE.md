# NLI Ver20 Release 3 — Quarantine Resolution Operator Guide

Release identity: `20.0.0-3`.

## The single recovery path

Use **Assurance & Release → Quarantine Resolution Wizard** for a retained parsing failure. The step cards are live status indicators, not a checklist that the operator must remember manually.

1. **Select** — choose one actionable retained failure and select **Inspect**.
2. **Inspect** — review the authenticated raw evidence. Enter the desired JSON field names in raw-token order, separated by commas or new lines, then select **Create mappings from my field names**. Values still come from the evidence; names never invent values.
3. **Schema** — verify field names, data types, required/optional policy, and null handling.
4. **Map** — verify token, key/value, delimiter, or regex extraction. Resolve every invalid or unconfirmed mapping.
5. **Generate** — prepare the deterministic candidate and save its validation cases as a review-gated draft.
6. **Test** — run the production Assurance preview. The exact desired JSON and mappings are sent with the selected retained record or fixture. AI is offered only after a deterministic single-record failure.
7. **Review & publish** — submit the candidate. A different authorized reviewer approves it; the author cannot approve their own revision. Publish only a candidate with passing deterministic evidence.
8. **Deploy & replay** — propose the exact bundle to the origin client, obtain independent rollout approval, wait for the exact applied bundle hash, then request targeted replay.
9. **Verify result** — refresh until the controller reports the retained record as resolved. Roll back or offboard the parser if the governed evidence requires it.

## Terminal record actions and badges

**Ignore / exclude** removes a record from active recovery work while retaining its metadata and permitted evidence for audit and future investigation. **Delete raw** permanently removes retained raw evidence while retaining non-raw metadata and audit history. Both actions clear the Quarantine Wizard, UPES, and Unknown & Quarantine actionable badges after the controller records the terminal state. **Return to queue** makes an excluded record actionable again.

## Legacy parser reconciliation

The controller reconciles a legacy catalog record only when it can produce an explicit current runtime identifier and a valid declarative runtime definition. Compatible records remain available under the current runtime identifier. Incompatible or malformed records stay visible as **blocked** and expose **Repair as draft**. They are not silently activated. New or changed parser revisions still require deterministic validation and independent review.

## Parser reference catalog

The release embeds an offline, revisioned reference catalog derived from the supplied corpus: 728 accounted records across eight families, 278 exercised known keys, reviewed ParserPacks, bounded known-key extraction, static layouts, regex/templates, projections, semantic policies, and an explicit generic fallback. This is a deterministic reference library, not a promise that arbitrary future field semantics can be inferred. Unknown or ambiguous input remains quarantined for evidence-backed review.
