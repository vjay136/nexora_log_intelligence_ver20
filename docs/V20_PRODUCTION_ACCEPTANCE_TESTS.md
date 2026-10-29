# NLI v20 production acceptance tests

Use this checklist after installing the matching controller and client RPMs and deploying the matching webapp. Record the release tag, RPM NEVRAs, controller hostname, client node IDs, browser URL, and test time (UTC) before starting. Use a dedicated test log path and test endpoint; do not point these tests at production customer data.

## Evidence and safety rules

- Screenshots should include the page title, selected inner tab, UTC time range, and the displayed revision/freshness indicator. Capture Command Center, Monitoring, NHLE Live, NAPI Live, NAL Live, Assurance Lab, Input Log Paths, Endpoint Configuration, Unknown & Quarantine, System Health & Storage, Users/RBAC/Audit, Notifications, and Status of All Communication.
- For each screenshot, save the corresponding API response from browser developer tools (Network → request → Response) where applicable. Do not include passwords, bearer tokens, enrollment tokens, or destination credentials in screenshots or evidence files.
- A value is live only when the UI reports a recent authoritative observation. `unavailable`, `not instrumented`, `idle`, or `not configured` is an honest state and must not be replaced with a fabricated zero or a demo value.
- Keep cumulative totals separate from counts for the current test window. The test below compares file lines with the delta between before and after snapshots.

## 1. Health and synchronization path

1. Sign in as an administrator. Open **Status of All Communication**.
2. Confirm `client_to_controller` lists each enrolled node with its last-seen timestamp and a live/stale state. A node that has not checked in must not be marked live.
3. Confirm `controller_internal` is live only when this status request succeeds. Confirm `controller_to_webapp` changes from unavailable to live after the browser heartbeat has been received; a stale browser heartbeat must become stale after the documented freshness window.
4. In browser developer tools, inspect `GET /api/v1/communications/status` and `POST /api/v1/communications/browser-heartbeat`. Record the response time and the `canonical.revision`. Verify the webapp refreshes the view in the background without reloading the whole page.
5. Open **Unified Sync Fabric**, **NHLE Live**, **NAPI Live**, and **NAL Live**. Verify their revision and freshness values refer to the same current controller snapshot. If a subsystem has no source evidence, it must explicitly say unavailable/idle rather than inventing data.
6. Stop the browser heartbeat (for example, pause the page's heartbeat timer in developer tools) and verify the browser hop eventually becomes stale. Resume it and verify recovery without a full page reload.

## 2. Single-source log count and duplicate-output regression

1. Stop the client service and confirm it is stopped before preparing test input:

   ```bash
   sudo systemctl stop nli-agent
   ```

2. Use a new, dedicated input file configured for one parser family. Put exactly 49 distinct, valid, single-line test records in it. Verify the source count:

   ```bash
   wc -l /var/log/nli/input/nli-acceptance.log
   ```

   Expected: `49`.

3. Record the current input/parsed/quarantined/error totals in Command Center and the current destination file sizes. Start the client:

   ```bash
   sudo systemctl start nli-agent
   sudo journalctl -u nli-agent -f
   ```

4. Wait until the client reports the file's 49 lines are consumed. Compare the **delta** in canonical input totals with 49. Parsed + quarantined + errors must equal input for the same window; parser failures must not silently disappear.
5. Stop the client again. Inspect the normalized output as JSON Lines (one JSON object per output line):

   ```bash
   sudo systemctl stop nli-agent
   wc -l /var/log/nli/output/nli-acceptance.jsonl
   jq -c . /var/log/nli/output/nli-acceptance.jsonl >/dev/null
   ```

   Expected for a clean 49-line corpus: 49 newly written normalized records and valid JSON on every line. If the configured route is quarantine, those records must instead be represented in quarantine output. Existing output records from earlier tests are not part of the delta.

6. Keep the service stopped for 60 seconds. Verify output line count and file sizes remain unchanged. Restart the service and verify the already consumed records are not emitted again. Capture the before/after counts and service journal.
7. Repeat with a deliberately malformed record. Expected: it is counted exactly once in the configured quarantine/error route and does not appear as a normalized success.
8. Remove only the dedicated test file and its output after preserving evidence. Do not use a wildcard delete against shared production paths.

## 3. Multi-file and multi-family ingestion

1. Configure two or more dedicated input files with distinct source IDs and parser families. Put a known number of records in each and record per-file counts.
2. Start the client and verify each configured source appears in NHLE/Monitoring with independent source identity. Verify aggregate input equals the sum of source deltas and each output's source metadata points to the correct input.
3. Append one new line to each file concurrently. Confirm each is processed once, without waiting for a manual browser refresh. Check queue depth and backpressure are source-specific where the contract supports it.
4. Rotate one test file and append to its replacement. Verify the configured rotation behavior does not replay old records or skip new ones.

## 4. Output endpoint delivery

1. Configure a non-production Loki or ClickHouse endpoint with a test tenant/database and restricted credentials. Verify the endpoint's TLS and authentication settings before enabling it.
2. Send a small known test batch. Compare NLI's accepted/delivered/failed counters with the destination's query result. Do not count local normalized-file writes as remote acknowledgements.
3. Temporarily make the test destination unreachable. Verify retries/backlog and errors appear with a meaningful cause; once restored, confirm each record is delivered once and the backlog drains.

## 5. Notifications and routing

1. Open **Notifications** and inspect the in-app feed. Each item must carry an audit sequence, timestamp, category, severity, action, resource, outcome, and source. Verify displayed events correspond to the controller's **Users, RBAC & Audit** audit records.
2. Acknowledge an item and reload the page. It should remain acknowledged for that user; sign in as another permitted user to verify acknowledgement state is user-specific.
3. As an administrator, set a routing policy using `PUT /api/v1/notifications/config`. Configure channel `enabled` flags and only environment-variable *names* for credentials. SMTP requires a host on port 465, sender and recipient-list variables, and optionally a username/password pair. Confirm the API never returns credential values. Verify invalid channel names, event types, or non-environment-name fields return HTTP 400.
4. Check channel state. In-app is derived from the audit chain. SMTP email uses implicit TLS and is marked delivered only after SMTP accepts message DATA; PagerDuty uses Events API v2 and is marked delivered only after its `success` acknowledgement. `ready` means credentials exist, not that an event was delivered.
5. Generate a harmless audited event with email/PagerDuty configured against a test mailbox and test PagerDuty integration. Confirm the event category, delivery-attempt history, retry outcomes, and provider acknowledgement. Every attempt is persisted separately and delivery failures do not create further audit events. Admin fallback and username routing apply to the in-app feed; external recipients come from the SMTP recipient-list environment variable and PagerDuty integration routing key.

## 6. Assurance, quarantine, audit, and user time

1. In Assurance Lab, select every inner tab in turn. The selected tab must be visually distinct and its content must match the selection. Run one valid corpus and one failing corpus, then verify persisted run IDs and timestamps in the activity/history views.
2. Send one malformed log to quarantine. Verify the quarantine record is visible, carries a stable source/record identity and reason code, and is not shown as parsed successfully. Replay only after correcting its parser/configuration; verify replay outcome and audit event.
3. Create a test account and confirm `created_at`, creator, role, and status are shown in Users/RBAC & Audit. Log in with that account and verify `last_login` updates in UTC-consistent time. Deactivate only the test account after capturing evidence.

## 7. Reliability and recovery

1. Keep a live dashboard open while client heartbeats and input records arrive. Confirm updates happen in the background without whole-page flicker or navigation reset.
2. Briefly interrupt browser connectivity, then restore it. The UI should reconnect using the shared recovery path, retain last-known values as stale during interruption, and resume from a fresh canonical snapshot without an unhandled `AbortError` notification.
3. Restart the client service and controller in the test environment. Verify client status transitions through stale/offline and back to live using actual heartbeat time; verify audit chain and metadata health remain valid.
4. For each error shown, capture its source, time, affected node/source/stage, diagnostic code, and recovery action. Verify the same cause is visible in Status of All Communication or Monitoring where the backend contract exposes it.

## 8. Input-path rollout and dashboard history

1. Create or edit an input-path profile as a draft, submit it, approve it as a different reviewer, and publish it. Verify the rollout endpoint rejects the profile before publication.
2. Propose the active profile for exactly one enrolled client and confirm only that node is targeted. Also verify selected multi-client and all-eligible target modes remain available. Confirm the proposal enters `pending_approval` and creates live commands only after independent rollout approval.
3. Read `GET /api/v1/rollouts` before and after approval. Confirm `pending_approval_count` decreases and the acted-on proposal stays in history. Confirm proposal and approval are present in the audit log.
4. Compare `canonical_history` one-minute count deltas with the client cumulative counters for the same interval. Confirm canonical buckets persist across controller restart. Treat parse/AI metadata series as bounded in-memory measurements and host metrics as current samples; do not use them as durable event history.

## Release sign-off record

| Check | Result | Evidence filename | Notes |
|---|---|---|---|
| Release/RPM versions match |  |  |  |
| Communication path and heartbeat |  |  |  |
| 49-record count and valid JSONL |  |  |  |
| No duplicate output after idle/restart |  |  |  |
| Multi-file/multi-family processing |  |  |  |
| Endpoint acknowledgement/retry |  |  |  |
| Notification routing and acknowledgement |  |  |  |
| Assurance/quarantine/replay |  |  |  |
| Browser reconnect without full refresh |  |  |  |
| Operator sign-off and UTC timestamp |  |  |  |
