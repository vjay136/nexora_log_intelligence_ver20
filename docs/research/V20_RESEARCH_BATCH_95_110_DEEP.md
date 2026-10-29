# Ver20 research batch 95–110 (excluding #96)

## Scope and exact count

**5 distinct upstreams were reviewed at source, test, and license level:** `getsentry/sentry`, `apache/pulsar`, `apache/kafka`, `segmentio/kafka-go`, and `elastic/detection-rules`. Entries #97, #101–103, #105–110 are not counted: I could not retrieve all required source/test/license evidence in this pass. #96 `getsentry/sentry-go` was explicitly excluded because it was already counted elsewhere. No product files were changed.

Activity evidence is limited to GitHub repository pages and fetched default-branch files. The pages/tool did not expose a reliable last-commit or release timestamp for all five, so precise activity dates remain unverified. Raw fetch/crawl recency is not treated as a commit date. This is a partial sample, not a completed review of positions 95–110.

## Method and evidence limits

Count requires relevant implementation source, a test file, and license evidence from the same canonical repository. Findings below are narrow source observations; no benchmark claims, full security audits, or dependency-license conclusions are made. Test files were inspected but not executed. Transferable ideas are independent design patterns; code and test copying are not proposed.

## Findings

### 95. getsentry/sentry — event ingestion and processing

- Canonical: https://github.com/getsentry/sentry. GitHub page showed public upstream; exact last commit/release date not established.
- Inspected: `src/sentry/event_manager.py`, `tests/sentry/event_manager/test_event_manager.py`, `LICENSE.md` (the repository has `LICENSE.md`, not `LICENSE`).
- Architecture: event manager is a broad application service, backed by PostgreSQL/Django transactions, cache, and message broker APIs. Test module exercises event ingestion and processing with Kafka payloads and local in-memory broker fixtures. This is application error/event management, not a generic parser engine.
- Malformed/evolution and failure handling: test suite is extensive and includes event processing behavior, but this pass did not trace a specific malformed-payload branch deeply enough to claim exact semantics. Source has typed mappings, validation/DB exception imports and transaction boundaries. Inference: an ingestion boundary must convert failures to observable outcomes without conflating them with valid empty events.
- Transfer to NLI: record parse/processing failures as first-class outcomes, and test queue/local-broker boundaries separately from storage transactions. Keep ingestion backpressure and downstream persistence behavior observable.
- Security/license/dependencies: event content is application data and external integrations exist; no full data-scrubbing or authentication audit was done. License is Functional Source License 1.1, Apache 2.0 Future License (`FSL-1.1-Apache-2.0`) from `LICENSE.md`; do not assume conventional Apache-2.0 permissions. Dependency licenses not reviewed.
- Confidence: medium for architectural surfaces; low for untraced malformed-input specifics.

### 98. apache/pulsar — broker connection and protocol boundary

- Canonical: https://github.com/apache/pulsar. Public Apache upstream; no dependable activity date established.
- Inspected: `pulsar-broker/src/main/java/org/apache/pulsar/broker/service/ServerCnx.java`, `pulsar-broker/src/test/java/org/apache/pulsar/broker/service/ServerCnxTest.java`, root `LICENSE`.
- Architecture: `ServerCnx` is a large broker connection/protocol handler. This review inspected the class and the corresponding connection test file, but did not trace a complete message path; avoid claiming details beyond that boundary.
- Failure/evolution: test file retrieval exposed a compacted 64-line response with license and class body unavailable, so behavior-level test assertions could not be established. No malformed-message policy or schema evolution behavior is claimed.
- Transfer to NLI: investigate the broker protocol boundary for bounded reads, backpressure, and connection lifecycle before borrowing any pattern. This entry is intentionally low-confidence and is not evidence of a specific recovery strategy.
- Security/license/dependencies: protocol parsers are security-sensitive; no frame-size, authentication, or decompression limits were inspected. Root license is Apache License 2.0 and source/test headers match. Transitive dependency licenses not audited.
- Confidence: low. Counted narrowly because source, test path, and license were opened; follow-up inspection required for substantive reliability conclusions.

### 99. apache/kafka — replication and ordered durable stream

- Canonical: https://github.com/apache/kafka. Public Apache upstream; latest activity date not established.
- Inspected: `core/src/main/scala/kafka/server/ReplicaManager.scala`, `core/src/test/scala/unit/kafka/server/ReplicaManagerTest.scala`, root `LICENSE`.
- Architecture: replica manager coordinates broker-side partition/replica work. The inspected test module is a dedicated ReplicaManager test suite. This pass did not walk specific code paths deeply enough to report exact retry ordering or malformed event behavior.
- Transfer to NLI: record source offsets/checkpoints and replay position independently from parsed output; test restart/replay and out-of-order failure paths with explicit expected state. These are NLI recommendations inferred from the replicated-stream role, not claims that the inspected files implement a particular NLI-like parser mechanism.
- Security/license/dependencies: no wire parser, ACL, or resource limits were audited. Root license and file headers identify Apache-2.0; transitive dependency licenses not checked.
- Confidence: low-medium; source/test/license evidence exists, but detailed failure handling was not traced.

### 100. segmentio/kafka-go — Go reader, offsets, cancellation

- Canonical: https://github.com/segmentio/kafka-go. GitHub page reachable; exact last commit/release date not established.
- Inspected: `reader.go`, `reader_test.go`, root `LICENSE`.
- Architecture: reader is context-aware, uses synchronization/atomic state, tracks offsets and exposes lag/read APIs. Source defines bounded default commit retries. Tests cover canceled contexts, reading message sequences, special first/last offsets, seeking, timestamp offsets, lag, recreated topics, and out-of-range reads that remain cancellable by context deadline.
- Failure/evolution: context cancellation and deadline are explicit error paths. Offset-based replay and lag are observable. Message content schema evolution is outside this library and not assessed.
- Transfer to NLI: give source readers cancellable operations and explicit offsets/checkpoints; expose lag and retry counts; make out-of-range behavior bounded by context deadlines instead of hanging indefinitely.
- Security/license/dependencies: protocol message decoding/resource limits were not audited. MIT root license. Dependency licenses not reviewed.
- Confidence: high for tested reader/cancellation semantics; medium for unexamined protocol internals.

### 104. elastic/detection-rules — schema-validated detection-as-code

- Canonical: https://github.com/elastic/detection-rules. GitHub page reports 4,215 commits and active project structure; page did not provide a reliable latest-commit date in retrieved text. README identifies a rules development, maintenance, testing, validation and release repository.
- Inspected: `detection_rules/rule.py`, `tests/test_all_rules.py`, `LICENSE.txt`, plus upstream README/CONTRIBUTING content for test workflow. Tests are built around rule syntax, schemas, metadata and query validators.
- Architecture: rule objects load structured TOML-like rule contents, validate through marshmallow, validate query languages (`EQL`, `KQL`), and check package/schema/version integration. Project CI/test model validates the entire rule corpus, not only individual examples; tests include syntax, metadata and compatibility checks.
- Malformed/evolution: schema/query validation and package compatibility are explicit. Rules carry versions, indices, severity, false-positive/investigation metadata, and validation recognizes compatible stack/integration schemas. This is static rule authoring, not free-form log decoding.
- Transfer to NLI: parser packs should be data with a schema and version, validated against an executable compatibility matrix and corpus-wide CI before promotion. Include metadata quality and query/grammar validation in release gates.
- Security/license/dependencies: source imports `eql`, `kql`, `marshmallow`, and other external libraries; dependency licenses were not audited. Rule/code repository is Elastic License 2.0 (`LICENSE.txt`), which restricts some hosted/managed-service uses; do not assume permissive reuse. Exact project README also states all repository content uses ELv2.
- Confidence: high for validation/test/licensing workflow; medium for broader runtime behavior.

## Uncounted positions and dispositions

- #96 `getsentry/sentry-go`: excluded per task because already counted.
- #97 OpenSearch: implementation `server/src/main/java/org/opensearch/ingest/IngestService.java` and Apache-2.0 license were opened, but the corresponding test fetch was throttled (429); not counted.
- #101 Zeek: `testing/btest/README` and `COPYING` were inspected, but the selected implementation path was invalid; no source/test/license-complete review, not counted.
- #102 Suricata: GPL-2.0 license was fetched, but source and test files were unavailable; not counted.
- #103 MITRE CTI: `USAGE.md` and `LICENSE.txt` were opened; the selected large STIX JSON payload exceeded fetch limit. This repository primarily supplies data/schema content, and no implementation+test pair was established; not counted.
- #105 SigmaHQ: `tests/test_rules.py` and repository `LICENSE` were opened. The candidate rule path did not resolve, and the root license says rule corpus uses Detection Rule License 1.1 while specification is public domain. No implementation/source+test+license-complete rule review; not counted.
- #106 osquery: root license indicates `Apache-2.0 OR GPL-2.0-only`; selected implementation and test paths unavailable; not counted.
- #107 Velociraptor: root AGPL-3.0 license was fetched; selected artifact and test files unavailable; not counted.
- #108 MITRE Caldera: root Apache-2.0 license fetched; selected source and test paths unavailable; not counted.
- #109 OWASP Cheat Sheet Series: inspected Logging Cheat Sheet and CC-BY-SA-4.0 `LICENSE.md`; it is guidance, not implementation/test code, therefore excluded from source-level project count.
- #110 google/fuzzing: inspected libFuzzer tutorial and Apache-2.0 LICENSE. Tutorial frames coverage-guided fuzzing and AddressSanitizer, but the selected paired test source was unavailable; not counted.

## NLI implications

This batch supports studying context-bounded cancellation, observable offsets and lag, replay/checkpoint separation, full rule-corpus schema validation, and explicit failure categories at ingestion/storage boundaries. It does not validate importing any of these projects. In particular, Sentry FSL and Elastic License 2.0 require specific legal review; Apache/MIT repository licenses do not resolve dependency, data, or generated-artifact rights. The batch is partial and should remain labeled as such in the combined research ledger.
