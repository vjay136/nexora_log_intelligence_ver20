# V20 research batch 80–110 (continuation)

## Scope and exact reviewed count

**5 distinct canonical repositories met the inclusion bar in this batch:** google/glog (86), uber-go/zap (91), sirupsen/logrus (93), go-kit/log (94), and getsentry/sentry-go (96). For each, I inspected implementation source plus test source (directly or repository test file listings and test content), and license evidence. The rest of prompt positions 80–90, 92, 95, and 97–110 were not counted: this pass found useful primary material for several, but did not complete implementation + test + license inspection for them. No mirrors or unavailable repositories were counted. `google/glog` is archived (read-only); the others appeared active at source/repository views during this inspection. This report is deliberately conservative; it is not a claim all 31 candidates were reviewed.

## Included upstreams

### 86. google/glog

- Exact files: [`src/logging.cc`](https://github.com/google/glog/blob/master/src/logging.cc), [`src/logging_unittest.cc`](https://github.com/google/glog/blob/master/src/logging_unittest.cc), [`src/raw_logging.cc`](https://github.com/google/glog/blob/master/src/raw_logging.cc), [`src/raw_logging_unittest.cc`](https://github.com/google/glog/blob/master/src/raw_logging_unittest.cc), [`LICENSE`](https://github.com/google/glog/blob/master/COPYING).
- Availability/activity: canonical google/glog repository page marks it archived on 2025-06-30; read-only status makes this useful as a stable historical reference, not an actively maintained baseline.
- Architecture: C++ logging implementation, macro/stream based, severity and destinations; source includes mutex/shared-mutex/thread primitives and raw logging path. Tests are compiled through CMake targets.
- Malformed/evolution/concurrency: logging configuration and output paths have synchronization concerns; the implementation explicitly uses locking primitives. Tests should include concurrent writes/config changes and verify no partial records. Raw logging is a distinct lower-level path and should not be assumed to share all ordinary logger behaviors.
- NLI transfer: keep logging API semantics separate from transport delivery; test concurrency and flush/output ordering; avoid importing archived dependencies without a maintenance rationale.
- Security/license/dependencies: source carries BSD 3-Clause notice; repository COPYING is license evidence. Confidence: medium-high for architecture/license, medium for behavior detail (not a full code audit).

### 91. uber-go/zap

- Exact files: [`zapcore/encoder.go`](https://github.com/uber-go/zap/blob/master/zapcore/encoder.go), [`zapcore/encoder_test.go`](https://github.com/uber-go/zap/blob/master/zapcore/encoder_test.go), [`logger.go`](https://github.com/uber-go/zap/blob/master/logger.go), [`logger_test.go`](https://github.com/uber-go/zap/blob/master/logger_test.go), [`LICENSE`](https://github.com/uber-go/zap/blob/master/LICENSE).
- Availability/activity: canonical upstream; repo calls API stable and states 1.x has no breaking API changes.
- Architecture: strongly typed structured fields feed an encoding-agnostic `Encoder` and core. Encoder contract separates entry encoding from output; object/array encoders support primitive and custom marshalers. The README distinguishes typed Logger from convenience SugaredLogger.
- Malformed/evolution/concurrency: encoder interface documents that Clone/EncodeEntry may be called concurrently, while an individual ObjectEncoder/ArrayEncoder is not safe concurrently. Cloning prevents new fields mutating shared context. Tests exercise logger/encoder behavior. Configurable keys and typed encoders reduce ambiguity; reflected arbitrary values are slower and may fail serialization.
- NLI transfer: model output as a typed event with stable keys, use immutable/snapshot context per event, and explicitly document concurrency contract at each boundary. Treat encoding failures distinctly from sink failures.
- Security/license/dependencies: MIT license evidenced both in LICENSE and file header. Core implementation imports stdlib and internal buffers; encoding/reflection behavior should be tested for hostile arbitrary values. Confidence: high.

### 93. sirupsen/logrus

- Exact files: [`entry.go`](https://github.com/sirupsen/logrus/blob/master/entry.go), [`entry_test.go`](https://github.com/sirupsen/logrus/blob/master/entry_test.go), [`json_formatter.go`](https://github.com/sirupsen/logrus/blob/master/json_formatter.go), [`json_formatter_test.go`](https://github.com/sirupsen/logrus/blob/master/json_formatter_test.go), [`LICENSE`](https://github.com/sirupsen/logrus/blob/master/LICENSE).
- Availability/activity: canonical repository and current source view available; no archival indicator observed.
- Architecture: each Entry is an event with level/time/message/caller/context and user fields; formatter, hooks, and output are delegated through its owning Logger. JSON and text formatters are separate. `Entry.Dup` clones field data for derived entries.
- Malformed/evolution/concurrency: code snapshots the formatter under lock, then formats outside the lock to avoid races and reentrant deadlocks (including MarshalJSON recursively logging). Reuse of entries is supported through copy-on-operation. Formatter/hook changes can therefore occur concurrently without holding the lock over user callbacks. Errors in formatting should be surfaced; don't assume arbitrary user values marshal cleanly.
- NLI transfer: treat event context as copy-on-write, snapshot mutable configuration before invoking callbacks, and avoid holding internal locks while calling user serializers or sinks.
- Security/license/dependencies: MIT license. JSON formatter uses standard JSON and arbitrary fields; sensitive-field scrubbing and cyclic/unsupported value failures remain caller concerns. Confidence: high.

### 94. go-kit/log

- Exact files: [`log.go`](https://github.com/go-kit/log/blob/main/log.go), [`log_test.go`](https://github.com/go-kit/log/blob/main/log_test.go), [`logfmt_logger.go`](https://github.com/go-kit/log/blob/main/logfmt_logger.go), [`logfmt_logger_test.go`](https://github.com/go-kit/log/blob/main/logfmt_logger_test.go), [`json_logger.go`](https://github.com/go-kit/log/blob/main/json_logger.go), [`concurrency_test.go`](https://github.com/go-kit/log/blob/main/concurrency_test.go), [`LICENSE`](https://github.com/go-kit/log/blob/main/LICENSE).
- Availability/activity: canonical project page shows latest release v0.2.1 in 2022; source remains available. Confidence in maintenance currency is limited; project is small and stable but release age is a caveat.
- Architecture: minimal one-method `Logger.Log(keyvals...)` interface; wrappers add context, levels, synchronization, and output encoding. Supports logfmt and JSON, and describes logs explicitly as structured data streams.
- Malformed/evolution/concurrency: logger wrappers compose, but key/value structure needs discipline (odd key/value lists and encoder errors are boundary cases); explicit SyncLogger/concurrency tests show output serialization is important when a writer is shared. User callbacks/writers should not be assumed safe absent wrapper.
- NLI transfer: keep the core logger contract narrow and add context/level/sink as composable wrappers; centralize synchronized writing and tests for concurrent emission rather than embedding hidden global state.
- Security/license/dependencies: MIT license evidenced by repo metadata and LICENSE. Minimal external dependency surface is suggested by Go source/module files; exact transitive graph not audited. Confidence: medium-high.

### 96. getsentry/sentry-go

- Exact files: [`client.go`](https://github.com/getsentry/sentry-go/blob/master/client.go), [`client_test.go`](https://github.com/getsentry/sentry-go/blob/master/client_test.go), [`transport.go`](https://github.com/getsentry/sentry-go/blob/master/transport.go), [`transport_test.go`](https://github.com/getsentry/sentry-go/blob/master/transport_test.go), [`hub.go`](https://github.com/getsentry/sentry-go/blob/master/hub.go), [`hub_test.go`](https://github.com/getsentry/sentry-go/blob/master/hub_test.go), [`LICENSE`](https://github.com/getsentry/sentry-go/blob/master/LICENSE).
- Availability/activity: canonical official Go SDK repository; recent test file source available.
- Architecture: event capture passes through client processing, sampling/filter hooks, and configurable transport; transport batches and sends asynchronously. Hub maintains scope/client context; clone provides request/goroutine-local scope state.
- Malformed/evolution/concurrency: tests explicitly exercise event dropping via sampling/processors, concurrent sampling, concurrent send+flush, repeat flush/close, and cloned hubs across goroutines. API permits event processors and BeforeSend to drop events (nil), so “no event” is a valid state rather than a transport error. Flush must be coordinated with asynchronous queue delivery.
- NLI transfer: represent filtered/held events distinctly from failures; isolate per-request enrichment state via clones; test send/flush/close races and sampling deterministically.
- Security/license/dependencies: MIT license file; Go module lists dependencies, but their full supply-chain risk was not assessed. Event payloads and DSN are sensitive; production integrations must scrub before sending. Confidence: high for exercised behaviors.

## Uncounted candidates / reason

- 80 toml-lang/toml-test: README and valid/invalid test corpus design were found, but implementation and license source were not inspected in this batch (not counted).
- 81 protobuf and 83 thrift: parser/protocol implementation source was found, but corresponding test/license set not completed (not counted).
- 82 Avro, 84 MessagePack, 85 Serde: not completed source/test/license inspection.
- 87 spdlog, 88 Log4j2, 89 log4net, 90 log4cxx: primary implementation details surfaced, but test/license source verification incomplete (not counted).
- 92 zerolog: repository/README found; implementation/test/license set not verified (not counted).
- 95 sentry (server), 97 OpenSearch, 98 Pulsar, 99 Kafka, 100 kafka-go: source/docs surfaced in some cases, but no complete scoped implementation/test/license review in this pass (not counted). Kafka consumer API/docs were inspected; no false claim that this constitutes a full candidate audit.
- 101–110 (Zeek, Suricata, CTI, Elastic detection rules, Sigma, osquery, Velociraptor, Caldera, OWASP cheat sheets, google/fuzzing): not completed in this pass.

## Cross-cutting NLI implications

The most reusable source-level patterns are: typed event fields and explicit encoding interfaces (zap); immutable event context and lock release before user callbacks (logrus); narrow composable logger interfaces plus synchronized sinks (go-kit/log); and transport flush/sampling/clone semantics as observable state (sentry-go). The concrete focused checks are concurrent event emission, mutation isolation, serializer failure, filtering/drop accounting, async flush idempotence, and disclosure-safe field redaction.
