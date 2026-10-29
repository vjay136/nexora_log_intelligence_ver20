# Changelog

## [20.0.0-1] - 2026-10-29

- Preserve parser asset bytes through independent rollout approval so the approved bundle hash remains verifiable and deployable.
- Make the Quarantine Resolution Wizard a focused nine-stage workflow with evidence-backed mapping, deterministic tests, independent approval, exact client acknowledgement, replay, verification, rollback, and offboarding.
- Group quarantine failures only by controller-verified signatures; isolate records when shared cause is unverified.
- Publish structured per-source journal progress and surface active file, processed, parsed, quarantined, error, byte, and phase counters in live WebView workspaces.
- Keep synchronization streams alive with rolling SSE deadlines, jittered bounded reconnects, shared HTTP transports, TLS 1.2 minimums, and transactional client trust updates suitable for large fleets.
- Promote Ver19 release 8 to the explicit upgrade and rollback baseline for the initial Ver20 release.

## [19.0.0-8] - 2026-10-28

- Turn Quarantine Resolution Wizard stages 6–9 into one guided Test, Review, Rollout, Replay, and Verify control flow with a state-derived next action.
- Duplicate the canonical Assurance, parser lifecycle, rollout approval, Fleet Sync, replay, verification, rollback, and offboarding controls inside the wizard.
- Add approval-gated parser-bundle rollout scopes for the origin client, selected clients, or every eligible online client.
- Require an exact desired hash, active hash, and applied acknowledgement before replay; stale bundle proposals cannot be approved.
- Keep rollback and offboarding reviewable and distribute the resulting removal bundle through the same independent approval path.
- Add browser and controller lifecycle regressions plus an operator test guide for unstructured-log recovery.

## [19.0.0-7] - 2026-10-26

- Publish authoritative per-source validation attempt, pass, and failure counters and rates from the client through the canonical synchronization snapshot and OpenMetrics endpoint.
- Replace the Monitoring pipeline's static VALIDATE placeholder with measured live values and parser-path timing.
- Fix non-empty NUPIRE/EGPE candidate rendering by preserving table rows as a collection.
- Consolidate quarantine evidence, deterministic preview, declarative parser editing, validation, independent review, publication, client acknowledgement, replay, and rollback into the Guided Parser Repair Studio.
- Add a dedicated nine-stage Quarantine Resolution Wizard with per-field add, rename, duplicate, reorder, delete, type, presence, extraction, transform, default, validation, evidence, confidence, and status controls.
- Validate the exact `Note;systemlogs_for_data` fixture without fabricating the semicolon-delimited value; explicit delimiter and field-boundary confirmations are required before candidate generation.
- Add browser regressions for every primary workspace and visible inner tab, the non-empty NUPIRE registry, measured validation telemetry, and the guided quarantine recovery workflow.

## [19.0.0-6] - 2026-10-26

- Add bounded UPES failure signatures, discovery queue metrics, and canonical snapshot visibility.
- Add the Guided Parser Repair Studio (GPRS) checksum-bound candidate review lifecycle, with explicit withdrawal and rollback states.
- Enforce deterministic parsing and UPES/GPRS recovery before AI; successful records never enter AI discovery, and validated AI output is staged for managed approval rather than activated locally.
- Add five endpoint-configuration subtabs with explicit local-validation boundaries and live/rollback controls.
- Document the actual UPES/GPRS boundaries and deployment acceptance gates.
- Package the architecture guide and SVG with the release archive and both RPMs.

## [19.0.0-5] - 2026-10-25

- Make submitted, independently approved, and published input-path profile stages explicit before rollout proposal.
- Add exact single-client targeting and a pending approval count for parser rollouts.
- Persist bounded canonical one-minute telemetry buckets and distinguish durable counters from in-memory metadata series.

## [19.0.0-4] - 2026-10-25

- Remove the unnecessary generic multiline continuation from line-oriented auto-family inputs while retaining explicit stack-trace assembly and restart-safe exactly-once output.
- Add persisted aggregate interval history for Overall Timeline dashboards, plus editable, validated, approval-gated single or multiple input-path rollouts.
- Keep dashboard controls stable while live telemetry updates in the background.

## [19.0.0-2] - 2026-10-24

- Wired controller, client-host, destination acknowledgement, replay, release-readiness, NHLE, NAPI and NAL evidence into the shared canonical live snapshot and their Monitoring views.
- Corrected System Health resource mapping, healthy storage diagnostics, missing-versus-zero rendering, NaN guards, and responsive diagnostic-table layout.
- Added bounded Linux host CPU, memory and load sampling to authenticated client heartbeats, plus controller uptime, memory, goroutine and file-descriptor measurements.
- Added the complete 39-workspace operator and manager demonstration guide with live, idle, missing and error-state interpretation.

## [19.0.0-1] - 2026-10-21

- Establish the Ver19 release identity across Go imports, web assets, RPM metadata, installers, bundles, and CI tags.
- Preserve Ver18 as the explicit upgrade source and retain its parser catalog and architecture evidence.
- Add a bundled migration and rollback note describing state preservation and recovery limits.
- Add a Ver19 operator and manager demo guide covering every current navigation workspace, significant inner tabs, real-time synchronization behavior, synthetic sample data, and screenshot capture instructions.
## [18.0.0-1] - 2026-10-21

- Separate controller transport freshness from per-source activity so idle log
  files cannot mark the whole WebApp synchronization path stale.
- Add one shared, abortable one-second snapshot recovery loop for interrupted
  or proxy-buffered SSE connections; all live workspaces continue to consume
  the same canonical state without page-wide refresh or per-panel fan-out.
- Fix canonical subsystem key mapping so NHLE, NAPI, NAL, and NURSF show their authoritative live, ready, or idle state.
- Publish one-second in-memory SSE heartbeats without issuing per-subscriber metadata queries; retain ordered source deltas and stale-source classification.
- Preserve valid zero-source full snapshots, expose client snapshot collection failures, and keep removed sources out of canonical state.
- Feed Command Center and Monitoring throughput from canonical nested source rates and queues.
- Publish controller health/version in the canonical snapshot instead of
  hardcoding browser values, and keep failed NAPI/NAL metadata collections
  unavailable instead of presenting false zero counts.
- Derive live parser p50/p95/p99 and source-worker counts from current
  authenticated source samples while keeping unsupported measurements
  explicitly unavailable.
- Coalesce meaningful visual updates in the background while preserving scroll, focus, forms, and active inner tabs; heartbeat-only updates no longer rebuild the page.
- Add controller, client, and browser regression coverage for live subsystem values, throughput, reconnects, freshness, recovery, sign-out cancellation, and no-shake refresh.
- Add the Nexora Unified Real-Time Synchronization Fabric (NURSF) as the canonical one-second-target telemetry path.
- Add live NHLE, NAPI, NAL, and Grafana-grade Monitoring workspaces backed by one shared snapshot and incremental SSE store.
- Add measured freshness, sequence/reconnect handling, bounded diagnostics, root-cause evidence, and responsive premium branding.

## [16.0.0-2] - 2026-10-19

- Add durable Assurance execution state and recovery.
- Add bounded parser synthesis and validation evidence.
- Add executable failure, replay, destination, performance, and security Assurance scenarios.

## [16.0.0-1] - 2026-10-18

- Adopted the supplied official Nexora Log Intelligence eagle logo and login
  banner across the authenticated console and Vite WebApp, plus a transparent
  horizontal product lockup sized for login, sidebar, and topbar use.
- Replaced one-goroutine-per-input scheduling with a bounded fixed source
  worker pool and fair source batches. V16 accepts 1–256 input files, keeps
  per-source ordering and durable checkpoints, and reports per-source errors
  and output backpressure without exposing customer event bodies.
- Added a 101-source append/restart acceptance test, multiline restart
  coverage, and a reusable 101-source benchmark.
- Reorganized the console into Command Center, Assurance, Input, Output,
  Parsing Engine, Parser Management, Runtime Engines, Client Management,
  Rollouts & Approvals, and Controller Management groups.
- Expanded Parser Creation Helper and AI Resolution into focused tabs with
  normalized input, parsed JSON, regex, templates, KV logic, field mappings,
  editable draft lifecycle controls, and independent approval gates.
- Added live client selection, select-all targeting, multi-profile proposals,
  and per-client rollout history for revisioned input-path configurations.
- Added exact paste normalization evidence for CRLF, LF, and bare-CR input and
  removed only end-of-line spaces and tabs before Assurance analysis.
- Added audited quarantine re-analysis from verified retained raw input without
  silently changing parser or record state.
- Added exact time/count dashboard axes with keyboard-accessible point details
  backed only by controller telemetry.
- Added a Vite production build and a clean web-only export for the dedicated
  Vercel repository while preserving the same-origin authenticated API proxy
  and all official brand assets.
- Preserved reviewed parser catalogs, known-key/static/template/delimiter and
  projection assets, approval-gated rollout, atomic client activation, durable
  quarantine, and Loki/ClickHouse/file output contracts.

## [14.0.0-1] - 2026-10-17

- Added a noninteractive `--automatic` controller path with stable root-owned
  credentials, IMDSv2-aware AWS identity discovery, complete certificate SANs,
  listener/TLS/firewall diagnostics, and safe credential recovery on upgrade.
- Added client `--all-log-files` provisioning for every reviewed parser family,
  including family-specific inputs and hourly normalized/quarantine outputs.
- Reorganized the WebView by operator task and added an original eagle-shield
  identity with a reduced-motion-safe animated sign-in experience.
- Rebuilt the operational dashboard around one bounded aggregate request with
  live ingest, parse, quarantine, error, latency, route, pipeline-stage,
  backpressure, client-resource, and parser-family measurements.
- Added a complete Parser Creation Workspace for trimmed single/multiline
  samples, editable deterministic artifacts, validation, drafts, independent
  approval, publication, rollout, and audit history.
- Added encrypted, write-only OpenAI, Anthropic, and compatible-provider
  configuration. AI proposals run only after an explicit operator action and
  remain drafts until deterministic validation and independent approval.
- Preserved AWS/private-network, corporate certificate, Nginx/ACME, existing
  proxy, Cloudflare Tunnel, Vercel, and Cloudflare Pages deployment paths with
  same-origin authentication and streaming.

## [12.0.0-1] - 2026-10-13

- Made offline controller-local private CA deployment the restricted-network default and added validated existing-server-certificate discovery with safe fallback.
- Added exact service, event, Webtop method, and hostname input filtering with editable default and customer rules.
- Added privacy-preserving email/network buckets and optional exact-path MaxMind MMDB enrichment.
- Split the console into Input Parser Engine and Output Parser Engine workspaces.
- Added bounded ClickHouse field-column mapping while retaining the complete typed event JSON.
- Preserved normal systemd status metadata while removing only external GitHub documentation links.

## [10.0.0-1] - 2026-10-13

- Added guided public controller exposure through Nginx with ACME, Cloudflare Tunnel, or an existing HTTPS reverse proxy while keeping the controller on loopback.
- Added the API Monitor workspace and reorganized Assurance guidance around test, retain, draft, review, publish, fleet verification, and replay.
- Added exact event/service/hostname input selection with `default`, `filtered`, and `hybrid` modes, reviewed presets, automatic parser/path provisioning, and tail-from-end for existing production logs.
- Added an exact-raw durable client quarantine queue, local encrypted controller retention, authenticated downloads, targeted replay, per-record results, and replay history.
- Fixed client node identity permissions so both managed services can read `install.id` after fresh install or upgrade.
- Added parser catalog documentation and automatic self-tests for every bundled reviewed parser family.

## [10.0.0-4] - 2026-10-10

- Added one versioned release archive for clean-directory transfer to Rocky Linux nodes, with an external archive checksum and a complete internal release manifest.
- Added `RELEASE_ID` and made both installers require its exact version, release, architecture, and source revision before installation.
- Made installers verify every checksum-listed bundle file and require the RPM release to match the bundle release exactly, preventing mixed V10 releases.
- Fixed the release evidence unit fixture so GitHub Actions includes the required sample-field accounting artifact.
- Fixed a controller-readiness race that could clear a password entered while the initial anonymous session probe completed.
- Rewired managed clients to send authenticated, private-CA parser telemetry to the controller while keeping parsed customer fields in selected local, Loki, or ClickHouse outputs.
- Unified bounded telemetry metadata across Live Metadata, overview, and time-series APIs and included the current interval immediately.

## [10.0.0-3] - 2026-10-10

- Fixed the guided controller and client installers so valid RPM release values with Rocky Linux distribution suffixes, including `2.el9` and `3.el9`, pass validation.
- Added `--verify-only` to both installers and made the release artifact gate execute those exact operator validation paths before publication.
- Kept V10 package identity, exact `10.0.0` version, x86_64 architecture, and SHA-256 bundle checks fail closed.

## [10.0.0-2] - 2026-10-09

- Added deterministic field-accounting evidence for all 728 supplied records: 723 valid records preserve exact `13,101 = 13,101` raw-field-to-generated-JSON parity, while five fixed malformed or truncated specimens are explicitly hash-pinned to the expected quarantine set.
- Verified all 278 declared known keys and added byte-level provenance for all 16 supplied Alloy/Vector/VRL parser sources, including the documented minimal repair applied to the SMTP-OUT source.
- Added authenticated Field Integrity & Corpus Proof, Unknown & Quarantine, Template Drift, and Performance & Backpressure workspaces, bringing the live WebView to 29 tabs.
- Added the corpus proof API and embedded release evidence, including RPM/web release validation of `SAMPLE_FIELD_ACCOUNTING.json`.
- Decoded bounded RFC 2047 Q/B content when semantic extraction reads a previously parsed field, with focused IPv4, IPv6, domain, URL, email, encoded-subject, and telemetry tests.
- Kept unknown and structurally invalid input fail closed; V10 does not fabricate fields or claim universal semantic accuracy for future unseen formats.

## [10.0.0-1] - 2026-10-09

- Added a corpus-derived, tiered V10 parser catalog with editable/downloadable known-key, delimiter, static-layout, projection, regex, template, and semantic-extraction assets.
- Added bounded IPv4, IPv6, domain, URL, email-address, and RFC 2047 encoded-Subject extraction as separately named parser capabilities.
- Added safe Assurance file/archive upload, encrypted opt-in retention, persistent AI proposal memory, independent approval, and complete trace/parity reporting.
- Added 25 live WebView workspaces, selectable time-series dashboards, detailed fleet/controller/client management, migration inspection, and parser asset studios.
- Added simultaneous controller, JSONL, Loki, and ClickHouse client outputs with independent batching, retries, and failure receipts.
- Restricted Supabase file persistence to retained Assurance fixtures and approved rollout RPM/config artifacts; normal production logs remain outside Supabase.
- Added V10 controller/client RPM packaging, V8 upgrade coverage, guided install/uninstall scripts, an 8K architecture diagram, and content-hash cached verification evidence.

## [7.0.0-1] - 2026-10-07

- Added the V7 Assurance Lab with scoring, coverage, parser candidate validation, regression history, and approval-gated publication.
- Added encrypted server-side configuration for optional OpenAI-compatible parser proposals.
- Added live user metadata mirroring to Supabase with credential exclusion and retryable partial-sync handling.
- Added one-run Rocky Linux controller/client enrollment, firewalld configuration, sample-log processing, and verified heartbeat startup.
- Added V7 product branding, notification center, repaired live-data workspaces, authenticated downloads, and Vercel/Cloudflare proxies.
- Added V7 RPM, WebView, webapp, provenance, SBOM, checksum, and reproducible release packaging.

## [6.0.0-2] - 2026-10-07

### Added
- Single-command controller and client installers that validate release RPMs, guide enrollment, and verify active services.
- Interactive client input/output configuration for one or more real log paths and controller, file, Loki, or ClickHouse delivery.
- Reconfiguration mode for changing client paths or destinations without reinstalling or re-enrolling.
- Separate safe uninstallers with preserved-data defaults and explicit purge confirmation.
- Three-node VMware/MobaXterm deployment runbook and Vercel live-sync guidance.

### Fixed
- Release staging normalizes Linux scripts, configuration, and systemd units to LF so Windows-hosted builds execute correctly on Rocky Linux.

## [6.0.0-1] - 2026-10-07

### Added
- Supabase PostgREST storage protected by RLS and an installation-specific controller secret.
- Production ParserPack execution and trace evidence in Assurance Lab, with encrypted manual-retention fixtures and authenticated raw-log downloads.
- Vercel-ready same-origin web application and bounded controller proxy, plus responsive tests for all 14 workspaces.
- Authenticated Loki, ClickHouse, controller, file, and stdout destinations with retry, custom-CA, multi-tenant, and delivery-accounting support.
- Reproducible client/controller RPM pipeline, web artifacts, SBOM, provenance, checksums, and Rocky lifecycle validation.

### Fixed
- Input cursors now advance only after complete multiline records reach acknowledged destinations.
- Supabase publishable keys are sent using their supported API-key semantics without invalid bearer authentication.
- Assurance inputs and results survive background reconciliation until an operator clears them.
- Fleet, parser catalog, and migration workspaces render actionable empty states instead of blank panels.
- Single-use enrollment replay, proxy authentication forwarding, TLS onboarding, and disabled-retention health behavior remain fail closed.

## [0.4.0] - 2026-10-02

### Added
- **13-stage layered parse pipeline**: multiline → format detect → timestamp extract → variable mask → Drain template mine → deterministic parse → AI discovery → field extract → type infer → schema validate → confidence → output route
- **Multiline assembler** (`internal/multiline`): Fluent Bit-inspired state machine with Java/Python/Go stack trace rules, flush_timeout (4s default), 2MB buffer limit with truncation flag
- **Format detector** (`internal/detector`): JSON, syslog RFC3164, syslog RFC5424, logfmt, Apache CLF, nginx, CEF, SMTP native, kv-pairs, plain (short-circuit detection)
- **Timestamp extractor** (`internal/timestamp`): 8 format variants (ISO 8601 variants, Apache CLF, syslog RFC3164, unix epoch, epoch ms, SMTP native), all normalized to RFC3339Nano UTC
- **Variable masker** (`internal/masker`): IPv4, IPv6, email, UUID, URL, hex IDs, integers, paths, secret fields; applied before template mining and on output fields; configurable per-input
- **Drain3-style template miner** (`internal/drain`): Fixed-depth prefix tree (ICWS 2017 algorithm), configurable sim_th/depth/max_children/max_clusters with LRU eviction, JSON persistence, thread-safe with RWMutex
- **Grok-style pattern library** (`internal/grok`): 20+ named patterns (USERNAME, IPV4, HOSTNAME, URI, EMAILADDRESS, UUID, TIMESTAMP_ISO8601, SYSLOGBASE, COMMONAPACHELOG, etc.), recursive substitution with cycle detection, pure Go, no GPL
- **Parser registry** (`internal/registry`): Declarative ParserDef JSON DSL (never executable code), loadable from directory, regression tests
- **16 built-in parsers**: `generic.json`, `generic.kv`, `generic.logfmt`, `syslog.rfc3164`, `syslog.rfc5424`, `web.apache_clf`, `web.nginx_access`, `web.nginx_error`, `smtp.dmarc.aggregate` (v4), `smtp.postfix`, `smtp.imap`, `security.sshd`, `security.sudo`, `kernel.audit`, `docker.json`, `cef.generic`
- **AI parser discovery** (`internal/ai`): Triggered only when cluster reaches min_cluster_size (default 10), batches up to 20 clusters per call, AI output constrained to ParserDef JSON only, regression test + quarantine on failure; supports ollama/openai/anthropic/none
- **Loki push output** (`internal/output/loki.go`): Batch HTTP push to `/loki/api/v1/push`, no external SDK
- **ClickHouse output** stub (`internal/output/output.go`): Uses `clickhouse-go/v2` (Apache-2.0)
- **Output router** (`internal/output`): Multiple destinations per run, per-route filtering (normal/warning/quarantine/unknown)
- **nli-manager** (`cmd/nli-manager`): HTTP REST API for multi-client management, client heartbeat registry (90s timeout), web UI served from `/usr/share/nli/web`
- **nli-bench** (`cmd/nli-bench`): Benchmark with 14 metrics, `run` and `compare` commands, BENCHMARK.md output
- **Replay mode** (`nli replay`): Re-parses quarantined logs against updated parser registry
- **Config v2 schema**: New fields: `outputs[]`, `drain{}`, `ai{}`, `replay{}`; backward-compatible v1 auto-migration

### Changed
- `engine.Version` = `"0.4.0"`
- Config schema bumped to version 2 (v1 configs auto-migrated with warning)
- `engine.Parse()` now accepts `*Deps` to inject pipeline components
- RPM bumped to `Version: 0.4.0`
- 4 binaries now packaged: `nli`, `nli-agent`, `nli-manager`, `nli-bench`
- 2 systemd units: `nli-agent.service`, `nli-manager.service`

### Fixed
- **Root cause of parsing failures**: All non-DMARC structured logs were falling to `_unknown` because `engine.go` had only 3 parsers. v4 adds format detection covering 10 formats and 16 built-in parsers.

## [0.1.0] - 2026-10-01

- First standalone deterministic test agent and CLI package
- SMTP DMARC aggregate parser, JSON parser, key=value parser
- Systemd service, cursor/checkpoint file tailer, metrics endpoint
# 12.0.0-1 — 2026-10-13

- Make offline controller-local private CA deployment the restricted-network default and add validated existing-server-certificate discovery with safe fallback.
- Add exact service, event, Webtop method, and hostname input filtering with editable default and customer rules.
- Add privacy-preserving email/network buckets and optional exact-path MaxMind MMDB enrichment.
- Split the console into Input Parser Engine and Output Parser Engine workspaces.
- Add bounded ClickHouse field-column mapping while retaining the complete typed event JSON.
- Preserve normal systemd status metadata while removing only external GitHub documentation links.
