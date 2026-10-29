# NLI V20 WebApp Operator and Parser Guide

This guide describes controls present in the V20 web application and controller code. The WebApp is an authenticated control plane, not a browser-only parser. Navigation is built from `web/app.js`; the controller provides `/api/v1` operations and RBAC. Use the controller-served WebView or a supported Vercel/Cloudflare proxy configured to the controller. A static file preview cannot sign in or manage state.

## 1. Sign in and understand the connection

1. Open the same-origin controller URL (normally `https://<controller-name>:12382/`) or the configured hosted proxy. For localhost/SSH mode use the packaged `Open-NLI-WebView.ps1` helper.
2. Wait for the login page's controller health check to enable the username/password fields. A static preview keeps credentials disabled by design.
3. Sign in as `admin` with the password chosen at first controller setup, or use the account created for you by an administrator. If the admin password was lost, the controller host supports `sudo nli-admin-reset`.
4. The page shows your username and role, connection/stream status, last refresh, notifications, Refresh, and Sign out. A session uses secure cookie authentication and CSRF protection. If live streaming fails repeatedly, the app reports polling fallback; Refresh requests a fresh workspace snapshot.

Do not use the Supabase project URL as the controller URL. Supabase is the controller's metadata/storage backend, not the WebApp API. Never paste API keys, node tokens, passwords, private logs, or enrollment bundles into samples or documentation.

## 2. Workspaces

The sidebar contains these workspaces (some content depends on controller data and your permissions):

| Group | Workspaces and purpose |
|---|---|
| Command | Live Dashboard (aggregated parsing/fleet metrics); Assurance Lab (sample runs, corpus, drafts and retained fixtures); Field Integrity & Corpus Proof; Fleet & Sync. |
| Assurance & Release | Assurance Lab; UPES · Parser Recovery; Approvals & Rollouts; Parser Fleet Sync; Signed Artifacts & Upgrades; Parser Creation Workspace; NUPIRE / EGPE. |
| Policy | Parser Catalog & Priority; Parser Fleet Sync; Migration Center; KV Delimiter Config; Static Positional Patterns; Field Capture & Projection; Regex, Templates & Studio; Regex Pattern Library; Known Key Catalog; Semantic Extraction Policy. |
| Parser Engines | Network Identity, URL, Email Address, and Encoded Subject capability pages. These inspect specialized engine behavior; RFC 2047 decoding is content decoding, not decryption. |
| Runtime | Live Metadata; Quarantine & Reprocessing; API Monitor; Template Drift; Performance & Backpressure; Rollouts; Signed Artifacts & Upgrades. |
| Governance | Users, RBAC & Audit; System Health & Storage; AI API Key Integration; Controller Agent Management; Client Agent Management. |

Operations and runtime views primarily show aggregated counters, hashes, service/health state, parser revision, and metadata. Normal production raw event bodies are not retained by the control plane. Use Assurance deliberately for sample evidence.

**API Monitor** makes authenticated live probes and displays route, purpose, HTTP result, latency, and last-check time. A permission response such as 401/403 proves reachability but does not grant access. The page records no request bodies, credentials, or log payloads.

## 3. Assurance Lab: repeatable sample test

1. Open **Assurance Lab** and use **Production parser assurance**. Paste a small representative raw line or multiline sample. Redact secrets and personal data first.
2. Choose **Parser family** from the reviewed family list. **Automatic family detection** evaluates every eligible approved ParserPack; the named IMAP, POP, Webtop/Webmail, MTA, PSS/PABD, MSS/CAPD, SMTP-IN and SMTP-OUT choices narrow family routing. Choose **Custom family** only for a new format, then enter its bounded family name. **Source** is optional context; it does not execute uploaded parser code.
3. Choose **Parser name**. **Automatic parser detection** runs the full approved ParserPack and runtime strategy pipeline. A named parser runs only that exact parser from the controller's live runtime registry, and an unavailable ID fails closed. The status cards show live runtime-parser count, managed pack states, Assurance draft count, managed sync-asset count, and aligned client count.
4. Leave **Exact desired JSON** as `{}` for parse-only inspection, or provide the exact expected JSON contract. Supply **Field mapping JSON** only when source keys need canonical names, for example `{"src_ip":"source_ip","ts":"timestamp"}`.
5. Select **Run assurance**. Inspect parsed output, field/type/count differences, source spans, ordered parser attempts, and stage results. A sample with `{}` tests handling and evidence; it does not assert semantic correctness. To claim a correct result, rerun with expected fields and values.
6. Use **Download result JSON** to save the report. Use **Clear manually** to clear the current in-page result. The app holds the current sample/result in page state while navigating within the session; clear it when finished.
7. For several formats, use **Multi-log corpus validation**. Choose default family/parser dropdowns and enter a JSON array whose entries may contain `raw`, `service`, `parser_id`, `source`, `desired`, and `field_mapping`. Defaults apply only where a case omits them, so one corpus can mix automatic and exact parser cases. Include positive and negative cases, continuations, optional fields, and malformed/truncated records relevant to your source.
8. To inspect a file, the upload panel accepts log/text, JSON/JSONL, CSV, and supported gzip/ZIP/TAR archives, subject to controller limits. The same family and parser selectors are sent to the controller for every extracted record. Hosted proxy uploads have a 4 MB UI limit; use the controller WebView for larger samples. Archives are expanded and validated server-side, not executed.
9. To retain a fixture, run the sample and use the save-fixture form when shown. Retained fixtures are encrypted by the controller before private Supabase Storage, including the selected parser identity and bounded result summary. The Fixtures section provides retained case operations including authenticated download and purge. Purge is permanent and removes the fixture and revisions. Transient analysis does not retain the original log.
10. Use **Guided Parser Repair Studio (GPRS)** for the retained-failure-to-candidate workflow described below. Its failure and lifecycle tables use the existing quarantine and Assurance APIs; a preview never approves or activates a candidate.
11. For an unknown but representative format, open **NAPI Synthesis**. Supply at least two discovery cases, a separate held-out case, and optional negative raw examples. NAPI generates only bounded declarative candidates, checks exact fields and false positives, applies RE2 size/capture/instruction limits, and stores only sample hashes and evidence. A successful result is a draft; validate, submit, obtain independent approval, and publish it through the existing parser lifecycle before rollout.
12. For repeatable certification, open **Test Plans** and include the required scenarios: `parser-regression`, `failure-recovery`, `replay`, `destination-integration`, `performance`, or `security`. Open **Runs**, select the exact plan revision, retained fixture, and one scenario, then start the bounded run. The durable local scheduler stores plan hash/revision, timestamps, terminal state, source hash, score, counts, route, latency, and scenario-specific checks; it never copies the raw fixture into the run record. Queued/running runs can be cancelled, and interrupted `running` records are re-queued after a controller restart. The destination scenario validates a local JSON boundary and does not contact configured production endpoints. Download the JSON as evidence. Distributed external-job orchestration is outside this release.

### Guided Parser Repair Studio (GPRS)

The GPRS tab sits inside **Assurance Lab**. It combines retained quarantine
records, Assurance previews, and the checksum-bound parser lifecycle already
provided by the controller; it is not an automatic promotion service.

1. Open GPRS and review **Failure inbox**. UPES health/counters come from the
   canonical NURSF snapshot; record rows come from the quarantine API. Exact
   source is available only for retained records. Empty tabs and unavailable
   counters are valid when the controller has no such evidence.
2. Select a retained failure and choose **Inspect & test**, or select a saved
   fixture. Enter the expected JSON instance and optional field mapping, then
   run the live preview. The JSON Schema box is reference text only; the current
   API checks an expected JSON instance and does not validate a schema.
3. Choose **Edit / create parser candidate** after preview. The Parser Creation
   Helper opens with the selected samples and cases. Create and validate a
   declarative draft; its checksum identifies the parser definition. Preview
   and draft creation do not change a live parser.
4. In **Candidate lifecycle**, submit a passing draft. A different reviewer
   must approve it. The controller rechecks the artifact checksum at submit,
   approval, and publish. **Withdraw** returns an in-review submission to draft.
5. Publish an approved candidate through the normal parser bundle workflow.
   Then obtain independent rollout approval and verify the originating client
   reports the desired applied bundle hash before requesting replay.
6. **Rollback** appears for an active published candidate. It makes that
   candidate approved and inactive and rebuilds the desired parser bundle; it
   does not immediately revert clients. Complete the ordinary rollout approval
   and client hash verification to deliver the removal. Replay remains gated
   by the server's record state, retry limit, target node, and applied hash.

The UPES page has aggregate and per-client counters in the canonical snapshot,
plus quarantine/NUPIRE-backed tabs. A counter-only heartbeat does not establish
that individual failure signatures or discovery summaries are available.

Its visible subtabs are **Live Failure Observatory**, **Recovery Decision
Trace**, **Parser Repair Workshop**, **Knowledge Base**, **Discovery Jobs**,
**Parser Health & Drift**, **Deployment & Rollback**, and **Runtime Health**.
These are views over available sync, quarantine, and NUPIRE data; an empty or
unavailable view is not evidence of an unseen worker. UPES observes/groups
bounded failures. GPRS candidate review is controller-side and managed parser
activation remains through the signed ParserPack workflow.

AI is the final recovery option after deterministic parsing and UPES/GPRS
diagnosis. The optional client-local Drain/AI discovery path stages a validated
declarative candidate under the client state directory; it does not register it
as active or assign it to a Drain cluster. Controller-side AI remediation also
creates review-only evidence. Both paths require validation, independent
approval, signed rollout, and client applied-hash acknowledgement before the
parser can become live.

An optional **Assurance parser workflow** can copy a live parser from its dropdown or start a custom definition, then accepts a name, version, declarative parser JSON and validation cases JSON. **Create and validate draft** creates a reviewable proposal. If a run provides a candidate parser, **Create candidate draft** can seed this workflow. Generated or AI-assisted definitions remain drafts and require the same deterministic validation and independent review as hand-authored parsers.

When **AI API Key Integration** is enabled on the controller, a failed deterministic Assurance run automatically asks the provider to diagnose the masked failure and suggest a bounded declarative parser. The original raw line and API key never go to client nodes, and the proposal store retains the source SHA-256 rather than the raw sample. The **AI remediation review** section shows the failure summary, coverage/errors, suggested method, proposed JSON and review state. A different reviewer must select **Approve as second reviewer**. After approval, rerun the same failing sample so its hash matches, then select **Create validated parser draft**. That action runs the ordinary deterministic validation; it does not publish or roll out anything. Continue with submit, independent parser approval, publish and Parser Fleet Sync. If the provider is offline or unconfigured, deterministic parsing and the manual workflow continue normally.

The guided order at the top of Assurance is the supported workflow: inspect the corpus contract, run a transient sample, retain only approved test evidence, create and validate a draft, obtain independent approval, publish, verify every target hash in Parser Fleet Sync, then replay selected quarantine records. A corpus is a set of input/expected-output cases used for regression testing; it is controller-side Assurance data and is not an RPM parser directory. Runtime ParserPacks are the reviewed files installed under `/etc/nli/parsers` and synchronized generations under `/var/lib/nli/parsers`.

The client has two separate parser state locations by design. Complete approved
ParserPacks in `/var/lib/nli/parsers` are loaded directly by `nli-agent` after
the sidecar performs an atomic generation swap and restarts the service. The
supporting KV, static-pattern, projection, template, regex, known-key, and
semantic assets are stored under `/var/lib/nli/parser-assets/<class>` with a
hash-verified `bundle.json`. These smaller assets are review and construction
inputs; they do not execute independently or bypass ParserPack validation.
Parser Fleet Sync reports the same desired/applied bundle hash and per-asset
revision for the controller and every client. The client rejects an unknown
class, invalid JSON, duplicate ID, altered asset hash, or altered bundle hash,
keeps the previous generation on failure, and reports the error to the
controller. This avoids network lookup in the per-record parsing path.

## 4. Create and operate a parser

There are two related UI surfaces. Use **Parser Catalog & Priority** for runtime ParserPacks (`rules` JSON); use Assurance's parser workflow, Test Plans, and revisioned proposals for assurance work. Policy workspaces (KV, static, projection, studio/templates, regex, known keys, semantic extraction) each save revisioned configuration assets. These are declarative definitions, not executable plugins.

### ParserPack path

1. In **Parser Catalog & Priority**, review the built-in runtime parser list and existing managed packs. Open **View full parser JSON** or download a definition to learn its shape.
2. In **Create parser draft**, either choose a live parser from **Start from live parser** to copy its declarative definition or leave Automatic and create your own. Enter a stable Pack ID, name, semantic version, priority, and Rules JSON. Lower numeric priority is tried first. Rules are declarative stages; UI guidance names regex, known-key, static-token and field projection stages. Non-RE2 regex and executable fields are rejected by the API.
3. Test first in Assurance against representative cases and exact desired JSON. For a persisted candidate, select **Create draft**. A newly created pack is a draft revision.
4. Use **Edit draft** only while a pack is draft or rejected. It loads the existing pack; saving creates a new revision. Inspect JSON and revision after saving.
5. A draft/rejected pack has **submit**. An in-review pack exposes **approve** and **reject**. An approved pack exposes **activate**. Approval should be by a different operator than the author where enforced by the controller lifecycle. Use a reviewer account for the review step.
6. Activation makes the approved pack part of runtime parser state; it does not imply that a custom parser-asset rollout control was invoked. For Web-created revisioned policy assets, follow the publish and Fleet Sync path below.
7. **Delete** is available in the pack catalog with a confirmation. Treat deletion as removal of the managed pack and verify resulting active state with Assurance and the catalog. Do not assume it withdraws an already materialized fleet bundle.

### Revisioned policy asset path (regex, keys, templates, projections, etc.)

1. Open the corresponding Policy workspace. Each shows stored asset definitions and a form with asset ID, name and configuration JSON. The defaults shown in the form are examples of the actual config envelope. For an established definition choose **Edit**; only draft or rejected assets are editable. Save a new draft revision.
2. Use a sample in Assurance before review. General configuration assets are saved via their workspace; parser proposals can be created and validated in Assurance. Confirm expected output, no unexpected field loss, and behavior for malformed input.
3. In the asset list use **submit** for a draft. A reviewer moves it through **approve** (or **reject**); then **publish** materializes the approved revision and starts Parser Fleet Sync. Available actions depend on the asset's state and your permission.
4. In **Parser Fleet Sync**, inspect desired and controller-applied revision/hash, rollout progress/errors, automatic rollout policy, and each client’s heartbeat/applied hash/drift. Use **Verify** to refresh controller-reported state. **Propose retry** creates a rollout proposal; it still needs independent rollout approval. This page cannot publish, approve, or force assets onto clients.
5. Confirm every target client's applied revision/hash matches desired and is not drifted. Then rerun the Assurance sample and check runtime metrics/Unknown & Quarantine for regressions.
6. For an unpublished draft/rejected policy asset, **Delete** prompts for confirmation and deletes it. For an approved/published asset, the UI does not offer deletion. GPRS exposes **Rollback** for an active published parser candidate: it returns that candidate to approved/inactive and rebuilds the desired bundle through the existing rollout path. It does not immediately revert clients. Obtain rollout approval and verify each target's applied bundle hash before replay or declaring rollback complete.

### How to shape the asset JSON

- **Known keys:** define keys, aliases, and delimiters. The built-in example keys include timestamp/time, level/severity, message, host/hostname, service, pid/process, user/username, source/destination IP, status, duration, and request ID. Scope keys to known formats; broad aliases can misclassify unrelated logs.
- **Regex:** the UI config envelope uses engine `re2`, a pattern and a mapping of named capture fields to canonical names. Prefer anchored patterns and bounded captures; include negative and multiline cases in Assurance. The form's sample is an example, not a promise about every regex field.
- **Templates:** use the Studio's RE2 pattern, named field mapping, and resource limits (`max_input_bytes`, timeout) visible in the default definition. Avoid patterns with unbounded expensive behavior; runtime is bounded.
- **Static positional patterns:** describe ordered tokens, optional/required types and optional rest-of-line capture. Use for stable headers and test shortened/missing-token cases.
- **KV delimiter policy:** configure pair separators, assignment separators, quote handling, duplicate policy and max fields. Test quoted delimiters, duplicates and truncated tails.
- **Projection:** explicitly select output fields and types. The sample default is allowlist mode. Check source-field parity and expected field counts before publishing.
- **Semantic extraction:** configure supported entity types and input/header bounds. Specialized engine pages show built-in examples for network identities, URLs, email and encoded subjects.
- For a full runtime pack example, inspect an installed reviewed pack such as `configs/parsers/nli-reviewed-smtp-in.parserpack.json`; do not paste a whole pack blindly into an asset's simpler config form.

## 5. Rollouts, notifications and verification

The **Rollouts** workspace lists proposals and supports approval, cancellation, and status inspection according to role. Creating a rollout from a signed artifact, upgrade, or parser sync retry creates an approval-gated proposal. Approving starts it; cancelling stops an eligible rollout. `GET /api/v1/rollouts` includes `pending_approval_count`; after approval or cancellation, the proposal remains in history but is excluded from this count. The Parser Fleet Sync inventory is the authoritative UI for per-parser desired/applied revision and hash. A completed rollout is not sufficient until client hashes are aligned and no errors/drift remain.

The notification bell shows in-session progress, completion, and error messages from UI actions (including uploads, assurance runs and rollout actions); **Clear** clears this in-page list. The Notification Manager separately shows the durable audit-derived feed and outbound delivery attempts. SMTP delivery uses implicit TLS on port 465; PagerDuty delivery requires a successful Events API v2 acknowledgement. Configure only environment-variable names in the UI and place values in the controller environment. `ready` reports a complete local configuration, while `delivered` requires provider acceptance. Command Center updates its retained historical counts in place every five seconds without refreshing the whole page. It supports preset windows from one hour through ten years, all retained history, and exact custom start/end timestamps. Use Refresh only when you want to reload the full current workspace state.

### Input paths and endpoints

1. Open **Input Log Paths**. Use the path field and parser-family dropdown, or select **Load all standard paths** for the complete RPM layout. Save and edit the profile as a draft, submit it, obtain independent profile approval, then publish it. Request rollout for one selected client to verify the exact node target; selected multi-client and all-eligible modes remain available for their existing workflows. Leave **replace outputs** unchecked to preserve that node's current outputs. Enable it only when you intentionally want the standard hourly family files (`<family>_normalised_YYYYMMDD_HH00.jsonl` and `<family>_quarantine_YYYYMMDD_HH00.jsonl`).
2. Open **Endpoint Configuration** to create local-file, Loki, ClickHouse, or controller output profiles. Local normalised output is optional; exact-raw quarantine is mandatory. Remote routes accept only fully parsed `normal` and `warning` events.
3. Submit the profile, have a second operator approve it, and publish it. In **Roll out endpoint profile**, select the active revision and exact node IDs. The proposal applies only an outputs patch; input, enrollment, telemetry, parser, and enrichment settings remain unchanged.
4. Approve the rollout as an authorized second operator. Confirm the client command result, service health, desired/applied hash, endpoint health, and delivery/error counters. A failed candidate validation or service restart restores the prior client configuration.

### Quarantine and reprocessing

1. The client always writes unmatched or structurally invalid source events to `/var/log/nli/quarantine.jsonl` as exact raw queue records with stable IDs, source path/line, hash, parser revision, attempt count, and error codes. Successful customer output continues to the configured file, Loki, or ClickHouse destinations.
2. Bounded metadata is synchronized to the controller. Exact raw upload is off by default and requires both the client installer opt-in and controller encrypted-retention opt-in. Controller quarantine storage is local, encrypted at rest, bounded, and excluded from Supabase.
3. Open **Quarantine & Reprocessing** to inspect metadata, parser revision, reason codes, and retained-evidence availability. **View evidence & diagnose** calls the audited analysis endpoint and shows the production parser result in Assurance without changing parser or queue state. Exact source is returned only when it was explicitly retained and the operator has `logs:read`; otherwise the item remains metadata-only. Single-record download and bulk raw/JSONL export are also audited.
4. **Exclude** removes an item from the active waiting queue while retaining its metadata and any retained evidence. The operator must provide a reason; **Return to queue** reverses the disposition. Both actions require `logs:write` and are recorded in quarantine history and the controller audit log. **Delete retained evidence** permanently purges only the encrypted raw sample, keeps metadata/history, requires `logs:write`, and is separately audited.
5. Use the evidence in Assurance to edit or generate a declarative parser, run validation, and save a draft. Submit a passing draft for review. A different operator with `parsers:review` may approve or reject it; the creator cannot review their own change. Publish is available only after approval and requires `rollout:trigger`.
6. In **Parser Fleet Sync**, verify desired/applied hashes and propose rollout to selected or all eligible clients. Complete the separate rollout approval, then verify client acknowledgements and hashes before replay. Replay only the exact record IDs on their originating client; the controller rejects excluded, resolved, in-flight, dead-letter, or otherwise ineligible records. Client outcomes remain pending until per-record parser confirmation arrives.
7. Records remain queued when parsing or customer-output delivery fails. Replay uses bounded 1, 5, 15, then 60 minute backoff and stops after five total attempts; the fifth unresolved result becomes `dead_letter` for manual attention. A controller/WebApp telemetry outage does not undo a successful customer delivery; the result marks telemetry separately.

The complete normal and recovery execution order, including UPES and GPRS plus the full meanings of NHLE, NURSF, NAPI, NAL, NUPIRE, and EGPE, is documented in [Ver20 subsystem names and execution order](V20_SUBSYSTEM_ORCHESTRATION.md). The key boundary is that NHLE processes ordinary records, while UPES observes bounded failure metadata and GPRS guides explicit evidence and review work; neither bypasses the existing signed rollout.

## 6. Users, roles and audit

In **Users, RBAC & Audit**, an authorized admin can create a user with username, email, role, and initial password, review role permissions/audit records, and deactivate an account. The UI's delete operation deactivates rather than erases the account. Access is enforced at the controller API as well as hidden/disabled in the UI; a visible page is not proof that a write is authorized.

Controller roles are `viewer`, `parser_author`, `reviewer`, `ops`, and `admin`. In practical terms: viewers inspect; parser authors create/manage parser drafts and run assurance; reviewers review parser proposals and can approve rollouts; ops manage fleet and trigger/cancel operational rollouts; admins have broad user, parser, fleet, rollout, audit and system access. Exact permissions are returned by `/api/v1/rbac/roles`; assign least privilege. Parser change and rollout approval are separate gates; arrange a second person for independent review.

Configure the AI provider only in **AI API Key Integration** using an administrator account. OpenAI and Anthropic use their official API base URL when the endpoint is blank; a custom endpoint must implement the OpenAI chat-completions contract. Test the connection before using automatic remediation. The API key is encrypted with the controller Assurance key, is write-only in the UI, is never returned after saving, and is never included in Supabase metadata, Vercel browser variables, client bootstrap files, parser bundles, or client rollouts.

## 7. Supabase artifacts and data boundaries

The controller uses Supabase for protected metadata. Parser patterns, known-key maps, templates, regex, projections, revisions and review state are metadata, not public files. Private file storage is for encrypted retained Assurance fixtures and approved rollout RPM/config artifacts. The UI downloads files through authenticated controller endpoints. Normal production logs/events are not stored in Supabase.

The browser must never receive Supabase server secrets, Storage secret keys, node tokens, TLS private keys, or controller bootstrap credentials. Vercel should proxy authenticated API traffic to the real HTTPS controller; configure secret values only where the server-side deployment contract requires them. Keep a local deployment `.env` out of Git and set Vercel variables in the correct environment separately. A public/publishable Supabase key alone does not make the browser a control-plane API.

## 8. Controller and client RPM operations

V20 packages target x86_64 Rocky Linux 9/10 as described by the repository support/release docs. The controller RPM (`nli-controller-agent`) provides the authenticated API, WebView, enrollment, fleet sync, rollout, telemetry and controller-side storage integration. The client RPM (`nli-client`) provides the parsing agent and restricted fleet-control sidecar. Install the controller on a management host and clients on log-producing hosts; install both on one host only if it also processes local logs.

- Controller health/API and WebView default to port `12382` in private TLS mode. Public Nginx/ACME, Cloudflare Tunnel, and operator-managed proxy modes keep `12382` on loopback and publish only HTTPS. The parser-agent metrics endpoint is `127.0.0.1:12380/metrics`; it is local metrics, not the browser API.
- Run controller setup with `sudo nli-controller-setup`; it configures the admin, TLS/listen mode, and backend settings and creates a one-use client bootstrap file under `/root`. Transfer it securely, then on a client run `sudo nli-client-setup /path/to/nli-client-bootstrap.env`. For another client, rotate enrollment on the controller with `sudo nli-controller-setup --rotate-enrollment` and use the new one-use file.
- In **Controller Agent Management** inspect controller health/version/storage and active service families. The UI intentionally does not restart services or execute shell commands. Use the OS service manager on the controller for service lifecycle and `journalctl -u nli-controller-agent` for host logs.
- In **Client Agent Management** inspect enrolled nodes, heartbeats, services, configured inputs/outputs, parser/config revisions, resources and command results. **Fleet & Sync** and **Parser Fleet Sync** show desired/applied state. For service/package installation or configuration changes, use the documented RPM/host workflow; do not mistake a browser status field for a host-level install action.
- The client polls for controller-managed state (the packaged example is 15 seconds) and reports applied hashes in heartbeat metadata. Investigate stale heartbeat, unreachable TLS/controller, rejected asset, rollout error, and revision/hash drift before retrying. A retry is still approval gated.
- Client configuration supports `default` (parse every assembled event), `filtered` (only exact event/service/hostname matches enter parsing; other events are counted as filtered and never quarantined), and `hybrid` (matches use the selected family while nonmatches use automatic detection). Multiple values within one field are OR; event, service, and hostname fields are AND. Existing production files start at end by default. Automatic setup creates empty input paths and validates parsers with temporary fixtures; it does not inject sample events. The agent opens all configured inputs and services them in bounded round-robin batches, so one 10 GB file cannot monopolize the loop.
- For release-specific RPM filenames, checksums, versions and upgrade steps, use the release archive manifest and `docs/INSTALLATION_AND_CONFIGURATION.md`; never mix loose RPMs from different release bundles. RPM rollback does not automatically reverse Supabase schema/data changes.

## 9. Practical end-to-end exercise

Use a test controller and a redacted sample before production:

1. Sign in as a parser author. In Assurance, test a representative source line with exact desired JSON and mapping.
2. In Parser Catalog, create a uniquely named low-risk draft with a bounded declarative rule. Re-run the corpus including a non-match and malformed line.
3. Submit the draft. Sign in as a separate reviewer, inspect definition/revision and validation evidence, then approve. Activate if using the ParserPack runtime lifecycle.
4. For a revisioned policy asset, submit, approve and publish it; wait for a rollout proposal when requested and have an authorized rollout approver approve it.
5. In Parser Fleet Sync, Verify, inspect each client’s applied hash and drift/error, then rerun the same Assurance contract and inspect Unknown & Quarantine and Dashboard metrics.
6. If evidence is wrong, keep the current deployment stable, create a corrected/new revision from the known-good definition, repeat the independent review, publish, and verify hashes. Delete only drafts/rejected assets through their available Delete control. Do not treat deleting metadata as rollback of a published client bundle.
7. Deactivate the temporary test account from Users, RBAC & Audit if it was created for the exercise; purge an encrypted retained test fixture when no longer needed.

The provided corpus proves coverage only for its pinned samples and declared parser families. No finite parser library can promise 100% semantic accuracy for every future log format. Treat unmatched/ambiguous records as unknowns, add representative evidence, and promote a parser only after its expected-field contract passes.
