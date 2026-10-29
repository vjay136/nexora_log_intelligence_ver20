# NEXORA LOG INTELLIGENCE v20
# MASTER ENGINEERING PROMPT
## NUPIRE — Nexora Universal Parsing Intelligence & Recovery Engine
### Evidence-Gated Parser Evolution • Universal Unstructured Log Parsing • Live NLI Integration • Verifiable Accuracy • Safe Rollback

---

## 0. YOUR ROLE AND MISSION

Act as a principal architect for log parsing, distributed ingestion, compiler engineering, information extraction, security engineering, data quality, benchmarking, and production reliability.

You are working on **Nexora Log Intelligence (NLI)**, an existing application with a released v18 implementation.

Your task is to create a fresh, compatible NLI v20 release that introduces a deeply integrated, high-performance universal parsing subsystem named:

**NUPIRE — Nexora Universal Parsing Intelligence & Recovery Engine**

The core proposed innovation is:

**EGPE — Evidence-Gated Parser Evolution.**

EGPE is a policy-controlled architecture that discovers unfamiliar log structures, generates independent parser candidates, evaluates those candidates against evidence, chooses only sufficiently validated interpretations, monitors parser drift, and manages safe parser evolution with audit trails and rollback.

Do not merely create a new UI, documentation, a prototype, or a standalone parser library. Inspect the real NLI v18 repository, identify the actual implementation gaps, implement the required capabilities, and integrate them with the existing NLI runtime, APIs, configuration, telemetry, storage, security, webapp, tests, and release process.

The intended outcome is a usable NLI v20 product release—not a disconnected demonstration.

### Fundamental objective

Convert supported unstructured log records into structured, typed, normalized, explainable JSON records while preserving the original data and making uncertainty, failures, and recovery explicit.

Support both known and previously unseen log formats, including formats that mix timestamps, key-value pairs, delimiters, nested JSON, quoted strings, escaped delimiters, stack traces, multiline events, binary-derived text, inconsistent fields, and arbitrary vendor-specific messages.

No universal parser can infer every semantic meaning from arbitrary bytes without context. When information is ambiguous or absent, NUPIRE must represent that uncertainty rather than fabricate values.

---

## 1. NON-NEGOTIABLE RULES

1. Inspect the real NLI v18 repository before modifying it.
2. Preserve existing functionality, routes, APIs, configuration, security, authentication, TLS, node enrollment, systemd services, RPM packages, and release behavior.
3. Reuse existing parsing, monitoring, synchronization, replay, quarantine, and registry components where they work.
4. Do not create duplicate subsystems simply because a new name is available.
5. Do not delete or replace working code without a documented, tested reason.
6. Do not hardcode operational metrics, fake parsing results, fabricated confidence, mock production events, or simulated success.
7. Do not claim 100% accuracy for arbitrary unknown logs.
8. Do not silently discard records, overwrite original evidence, or convert parse failures into successful empty objects.
9. Do not execute arbitrary generated code, unsafe regular expressions, untrusted plugins, or uncontrolled model-generated programs in the production parsing path.
10. Do not send customer log contents to external AI services by default.
11. Do not copy third-party implementation code, parser packs, proprietary templates, benchmark datasets, or tests without appropriate rights and review.
12. Do not automatically promote a parser candidate to production solely because it parses several sample records.
13. Do not let parsing intelligence block ingestion while researching, training, compiling candidates, or running benchmarks.
14. Do not perform destructive database migrations or uncontrolled production replay.
15. Do not mark a feature complete until it has been integrated and its behavior has been verified.

If a capability already exists, verify it and improve it only where justified. If a capability is missing, implement it. If a capability is unsafe, repair it. If a dependency is unavailable, complete independent work and document the exact blocker.

---

# PART I — REPOSITORY RESEARCH AND LEGAL REVIEW

## 2. PHASE ZERO: AUDIT NLI V18

Inspect the entire repository and determine its actual architecture, language, modules, data flow, release structure, test coverage, APIs, configuration, deployment topology, and existing parsing behavior.

Inspect at minimum:

- Existing parser engines and parser registries.
- Source readers and multiline framing.
- File rotation, truncation, checkpoints, and recovery.
- Existing NLI source-specific processors.
- Input validation, output normalization, and schema definitions.
- JSONL output and quarantine handling.
- Parser confidence, error handling, and metrics.
- Existing NAPI/NPA or equivalent parser-intelligence components.
- NHLE or equivalent ingestion runtime.
- NURSF or equivalent synchronization infrastructure.
- NCIC and Monitoring dashboards.
- NAL or equivalent validation and assurance subsystem.
- API routes, frontend state, controller communication, and database schema.
- Agent/controller/client-control services.
- Configuration migration and compatibility logic.
- RPM/systemd packaging and service lifecycle.
- Existing benchmarks, fixtures, regression tests, and end-to-end tests.

Trace actual records from source to parser to output. Establish which parser handles each record and what happens when parsing fails.

Produce an initial capability matrix:

- IMPLEMENTED AND VERIFIED
- IMPLEMENTED BUT UNVERIFIED
- PARTIALLY IMPLEMENTED
- BROKEN
- DISCONNECTED
- DUPLICATED
- MISSING
- UNSAFE
- BLOCKED
- NOT APPLICABLE

Record evidence, affected files, dependencies, and test results for each conclusion.

Run existing tests and benchmarks before changes. Preserve a baseline so the v20 release can be compared against v18.

## 3. RESEARCH AT LEAST 100 GITHUB REPOSITORIES

Perform a structured, evidence-based review of at least 100 distinct GitHub repositories relevant to parsing, log ingestion, parser generation, structured-data decoding, log datasets, parser testing, or observability pipelines.

The inventory below is a candidate research corpus, not proof that each project is active, suitable, permissively licensed, or already audited. Verify actual repositories, canonical upstreams, source code, commit history, releases, license files, and relevant dependencies.

Do not count mirrors, forks of the same upstream, or multiple branches of one repository as independent projects just to reach 100.

For every repository actually reviewed, record:

- Canonical repository URL and upstream owner.
- Repository availability and last relevant activity.
- Exact source files and modules inspected.
- Primary purpose and architecture.
- Parsing algorithm or data-flow design.
- How it handles unknown formats.
- How it handles multiline records, nesting, malformed input, and schema drift, if applicable.
- Concurrency and streaming behavior, if applicable.
- Memory, CPU, latency, and throughput characteristics supported by evidence.
- Test datasets and benchmark methodology.
- Recovery, replay, versioning, and failure-handling mechanisms.
- Security concerns and unsafe execution surfaces.
- Exact license and dependency-license evidence.
- Ideas worth studying at a conceptual level.
- Missing capabilities or limitations actually demonstrated by source, tests, documentation, or benchmarks.
- Whether NLI can independently implement a comparable capability.
- Whether a dependency or code reuse is legally and technically acceptable.
- Research confidence and remaining uncertainties.

Do not invent benchmark results, licensing conclusions, source-code behavior, or undocumented limitations. Clearly separate observations, hypotheses, and proposed improvements.

### 3.1 Research corpus: log parsing algorithms and research

1. https://github.com/logpai/logparser
2. https://github.com/logpai/Drain3
3. https://github.com/logpai/loghub
4. https://github.com/logpai/loghub-2.0
5. https://github.com/logpai/loglizer
6. https://github.com/logpai/deep-loglizer
7. https://github.com/logpai/AutoLog
8. https://github.com/logpai/LILAC
9. https://github.com/BlueLionLogram/Logram
10. https://github.com/SalmaMessaoudi/MoLFI
11. https://github.com/SRT-Lab/ULP
12. https://github.com/gaiusyu/Brain
13. https://github.com/nulog/nulog
14. https://github.com/IBM/Drain3
15. https://github.com/IBM/LogAnalysis
16. https://github.com/IBM/LogAnalysis-2
17. https://github.com/elastic/logstash
18. https://github.com/elastic/beats
19. https://github.com/elastic/go-grok
20. https://github.com/elastic/elasticsearch

For entries that are unavailable, renamed, archived, mirrors, or duplicates, record the finding and locate the canonical upstream if one exists. Do not treat an invalid repository URL as evidence that a project has been reviewed.

### 3.2 Log ingestion, observability, and pipeline architectures

21. https://github.com/vectordotdev/vector
22. https://github.com/fluent/fluent-bit
23. https://github.com/fluent/fluentd
24. https://github.com/open-telemetry/opentelemetry-collector
25. https://github.com/open-telemetry/opentelemetry-collector-contrib
26. https://github.com/grafana/alloy
27. https://github.com/grafana/loki
28. https://github.com/grafana/tempo
29. https://github.com/prometheus/prometheus
30. https://github.com/prometheus/node_exporter
31. https://github.com/rsyslog/rsyslog
32. https://github.com/syslog-ng/syslog-ng
33. https://github.com/google/mtail
34. https://github.com/prometheus-community/pg_exporter
35. https://github.com/parseablehq/parseable
36. https://github.com/quickwit-oss/quickwit
37. https://github.com/elastic/elasticsearch
38. https://github.com/elastic/kibana
39. https://github.com/elastic/elastic-agent
40. https://github.com/elastic/ecs

Evaluate which reliability and observability practices can improve NLI without replacing its existing ingestion architecture.

### 3.3 Parsing libraries, grammars, and language frameworks

41. https://github.com/antlr/antlr4
42. https://github.com/tree-sitter/tree-sitter
43. https://github.com/tree-sitter-grammars/tree-sitter-json
44. https://github.com/tree-sitter-grammars/tree-sitter-yaml
45. https://github.com/tree-sitter-grammars/tree-sitter-toml
46. https://github.com/tree-sitter/tree-sitter-bash
47. https://github.com/tree-sitter/tree-sitter-python
48. https://github.com/tree-sitter/tree-sitter-rust
49. https://github.com/tree-sitter/tree-sitter-go
50. https://github.com/tree-sitter/tree-sitter-javascript
51. https://github.com/tree-sitter/tree-sitter-typescript
52. https://github.com/rust-bakery/nom
53. https://github.com/winnow-rs/winnow
54. https://github.com/zesterer/chumsky
55. https://github.com/pest-parser/pest
56. https://github.com/lark-parser/lark
57. https://github.com/pyparsing/pyparsing
58. https://github.com/erikrose/parsimonious
59. https://github.com/Chevrotain/chevrotain
60. https://github.com/kach/nearley
61. https://github.com/peggyjs/peggy
62. https://github.com/ohmjs/ohm
63. https://github.com/alecthomas/participle
64. https://github.com/mna/pigeon
65. https://github.com/akimd/bison
66. https://github.com/antlr/antlr4-runtime
67. https://github.com/foonathan/lexy
68. https://github.com/yhirose/cpp-peglib
69. https://github.com/jneen/parsimmon
70. https://github.com/Engelberg/instaparse

Review grammar design, parser recovery, error positions, nested structures, ambiguity handling, and safe execution. Do not assume a programming-language parser can directly solve arbitrary log parsing; identify the transferable concepts.

### 3.4 Structured data, serialization, and event formats

71. https://github.com/jqlang/jq
72. https://github.com/simdjson/simdjson
73. https://github.com/fastfloat/fast_float
74. https://github.com/Tencent/rapidjson
75. https://github.com/nlohmann/json
76. https://github.com/yaml/libyaml
77. https://github.com/yaml/pyyaml
78. https://github.com/toml-lang/toml
79. https://github.com/BurntSushi/toml
80. https://github.com/BurntSushi/toml-test
81. https://github.com/protocolbuffers/protobuf
82. https://github.com/apache/avro
83. https://github.com/apache/thrift
84. https://github.com/msgpack/msgpack
85. https://github.com/serde-rs/serde

Examine robust decoding, strict versus permissive modes, type conversion, nested data, malformed-input behavior, schema evolution, and resource limits.

### 3.5 Log generation, logging frameworks, and source behavior

86. https://github.com/google/glog
87. https://github.com/gabime/spdlog
88. https://github.com/apache/logging-log4j2
89. https://github.com/apache/logging-log4net
90. https://github.com/apache/logging-log4cxx
91. https://github.com/uber-go/zap
92. https://github.com/rs/zerolog
93. https://github.com/sirupsen/logrus
94. https://github.com/go-kit/log
95. https://github.com/getsentry/sentry
96. https://github.com/getsentry/sentry-go
97. https://github.com/opensearch-project/OpenSearch
98. https://github.com/apache/pulsar
99. https://github.com/apache/kafka
100. https://github.com/segmentio/kafka-go

Study realistic message variations, structured logging conventions, contextual fields, correlation identifiers, ordering, and transport boundaries.

### 3.6 Additional security, protocol, and test-tool candidates

101. https://github.com/zeek/zeek
102. https://github.com/OISF/suricata
103. https://github.com/mitre/cti
104. https://github.com/elastic/detection-rules
105. https://github.com/SigmaHQ/sigma
106. https://github.com/osquery/osquery
107. https://github.com/Velocidex/velociraptor
108. https://github.com/mitre/caldera
109. https://github.com/OWASP/CheatSheetSeries
110. https://github.com/google/fuzzing

These are supplementary research candidates. They are not all parser engines. Determine which provide useful log schemas, test methods, event semantics, or security practices; exclude irrelevant projects from the final technical comparison.

### 3.7 Research deliverables

Before claiming that the research phase is complete, produce:

1. A verified repository inventory with at least 100 distinct, relevant projects actually reviewed.
2. A repository-by-repository research matrix.
3. A feature comparison table.
4. A license and dependency-risk ledger.
5. A list of reproducible strengths and weaknesses.
6. A ranked gap analysis.
7. A proposed NLI-owned architecture.
8. A decision record explaining which ideas to implement, independently reimplement, use through an approved dependency, defer, or reject.

If fewer than 100 repositories can be fully reviewed within the available environment, report the exact count completed, identify remaining candidates, and do not falsely claim completion.

Do not copy entire third-party repositories into the NLI codebase merely to satisfy the research requirement.

---

# PART II — LEGAL AND INTELLECTUAL-PROPERTY SAFEGUARDS

## 4. COMMERCIAL-SAFE DEVELOPMENT

NLI is intended to be a proprietary commercial product. Treat licensing and provenance as release requirements.

Create or extend a dedicated research ledger recording:

- Repository URL, owner, license, version or commit, and date checked.
- Relevant source files and documentation reviewed.
- Exact third-party dependencies used by the proposed design.
- Copyright notices, attribution, notice files, and redistribution obligations.
- Copyleft and network-copyleft risks.
- Patent notices or explicit patent grants where applicable.
- Whether the source is archived, ambiguously licensed, or contains mixed-license files.
- Legal review status and approval evidence.
- The reason a dependency or implementation was accepted or rejected.

Rules:

1. Publicly visible source code is not automatically free for unrestricted commercial reuse.
2. A repository without a clear license must be treated as unavailable for code reuse until reviewed.
3. A license attached to a repository does not necessarily cover every bundled dataset, model, generated file, dependency, or third-party contribution.
4. Do not copy parser packs, regex collections, tests, benchmark datasets, or code from incompatible licenses into a proprietary distribution.
5. Do not assume that independently rewriting code eliminates every legal concern. Avoid copying protected expression and obtain legal review when needed.
6. Implement original algorithms from independently developed specifications and lawful public knowledge where appropriate.
7. Track dependency licenses and transitive dependencies with automated scanning where practical.
8. Preserve required notices for any approved dependencies.
9. Keep third-party parser adapters isolated and disableable.
10. Never claim legal clearance merely because a dependency is open source.

Create original NLI-owned modules, interfaces, schemas, test fixtures, documentation, and parser templates where permitted. Do not use third-party benchmark data in a commercial release without confirming its terms.

---

# PART III — THE NUPIRE ARCHITECTURE

## 5. DESIGN A MULTI-STRATEGY PARSING ENGINE

Implement a staged parsing pipeline. Keep the hot path deterministic and efficient. Put expensive discovery, model inference, candidate generation, and benchmarking on separate bounded worker paths.

Recommended logical flow:

INGEST
→ PRESERVE ORIGINAL
→ FRAME RECORDS
→ DETECT ENCODING
→ CLASSIFY STRUCTURE
→ EXTRACT KNOWN FIELDS
→ SELECT PARSING STRATEGIES
→ GENERATE CANDIDATES IF NEEDED
→ VALIDATE CANDIDATES
→ SCORE EVIDENCE
→ SELECT OR ABSTAIN
→ NORMALIZE
→ VALIDATE OUTPUT SCHEMA
→ EMIT STRUCTURED EVENT
→ OBSERVE DRIFT
→ QUARANTINE OR REPLAY WHEN REQUIRED

These stages must be adapted to the existing implementation rather than duplicated if equivalent stages already exist.

### 5.1 Original-input preservation

For each input event, preserve the exact original bytes or an equivalent lossless representation when technically possible.

Record encoding and decoding status, source identity, file identity where available, byte offsets or source cursor, original timestamp information, ingestion time, and a stable event identifier.

If decoding fails, preserve the raw evidence in a safe representation. Do not discard undecodable bytes or silently replace them with ambiguous characters.

Apply access controls, retention rules, and sensitive-data handling to raw records.

### 5.2 Framing and multiline reconstruction

Support configurable and tested framing for:

- Single-line records.
- Stack traces.
- Java and .NET exception blocks.
- Timestamp-led multiline messages.
- Continuation lines.
- Nested or embedded JSON.
- Quoted and escaped multiline content.
- Protocol conversations where reconstruction is supported.
- Explicit delimiters.
- Size and time-based framing limits.

Use source-aware rules where reliable, plus conservative general-purpose framing for unknown sources.

Do not merge unrelated records simply because the second line lacks a timestamp.

Bound multiline buffer size, record size, waiting time, and memory usage. Expose incomplete framing rather than silently losing content.

Handle partial writes, rotations, truncation, restarts, and replay according to the existing ingestion contracts.

### 5.3 Structural classification

Detect candidate structure families:

- JSON and JSON Lines.
- Key-value text.
- CSV, TSV, and other delimited records.
- Syslog-like messages.
- Common web access log formats.
- Timestamped application logs.
- Bracketed and tagged messages.
- Space-separated and fixed-width records.
- Quoted-field records.
- Nested JSON or embedded structured payloads.
- XML or other supported structured formats.
- Stack traces and multiline events.
- Database, mail, network, authentication, and security event families.
- Unknown or mixed formats.

Treat classifications as hypotheses supported by evidence, not absolute truth.

Preserve both outer message fields and embedded payloads when relevant.

### 5.4 Specialized parser adapters

Create or reuse safe adapters for:

- JSON.
- Key-value parsing.
- Delimited fields.
- Timestamp and timezone extraction.
- Syslog envelope parsing.
- Common HTTP/access log formats.
- Numeric and boolean fields.
- IP addresses, ports, hostnames, and identifiers.
- Nested structured payloads.
- Multiline and stack-trace records.
- NLI-specific IMAP, POP, SMTP inbound/outbound, MTA, Webtop, PABD, CAPD, PSS, and any other source families found in the repository.

Do not assume these are the only formats NLI will ever receive.

For known source formats, use deterministic, explicitly tested parsers. Use general discovery only when a reliable source-specific parser is absent or evidence indicates that the source format has changed.

### 5.5 Token and structural analysis

Develop original analysis routines for:

- Tokenization that respects quoting and escaping.
- Delimiter detection.
- Token-position comparison.
- Stable-token and variable-token identification.
- Timestamp pattern hypotheses.
- Field-boundary inference.
- Numeric, boolean, IP-address, and identifier candidate detection.
- Common-prefix and common-suffix analysis.
- Structural fingerprints.
- Record-shape similarity.
- Nested payload boundary detection.
- Entropy and novelty indicators where they provide measurable value.

Support non-ASCII text, Unicode, locale-specific timestamps, and different character encodings.

Never assume that a space is a delimiter or that a colon always separates a key from a value.

### 5.6 Template mining and clustering

Evaluate streaming template mining approaches conceptually, including fixed-depth trees, sequence similarity, token n-grams, and cluster-based methods.

Build NLI-owned implementations only after benchmarking the alternatives against the NLI workload.

Requirements:

- Bounded cluster count and memory.
- Stable behavior across worker restarts.
- Configurable masking of known variable tokens.
- Protection against unbounded template proliferation.
- Detection of cluster merging or splitting errors.
- Explicit template versioning.
- Confidence and support counts.
- Ability to compare candidate templates with existing production templates.
- No automatic production schema changes merely because a cluster changes.

Template discovery should propose structure; it must not be treated as proof that the inferred field names are semantically correct.

---

# PART IV — THE NEW CORE IDEA: EVIDENCE-GATED PARSER EVOLUTION

## 6. EGPE: EVIDENCE-GATED PARSER EVOLUTION

Build EGPE as a first-class NUPIRE subsystem.

Its lifecycle is:

OBSERVE
→ PROFILE
→ CLUSTER
→ HYPOTHESIZE
→ GENERATE CANDIDATES
→ COMPILE SAFELY
→ TEST
→ DIFFERENTIALLY COMPARE
→ BENCHMARK
→ SCORE EVIDENCE
→ SHADOW RUN
→ CANARY
→ APPROVE
→ PROMOTE
→ MONITOR
→ DETECT DRIFT
→ ROLLBACK OR EVOLVE

Each transition must be represented by real job state and audit evidence.

### 6.1 Candidate generation

Generate multiple independent candidate interpretations where useful:

- Known deterministic parser.
- Token-based parser.
- Key-value candidate.
- Delimiter candidate.
- Template-based candidate.
- Grammar-based candidate.
- Schema-guided candidate.
- Specialized source-family candidate.
- Optional AI-assisted candidate, if explicitly enabled by policy.

Do not invoke every strategy for every record. Use inexpensive classification and routing first.

Candidates must be compiled or interpreted within bounded execution limits and tested before production use.

### 6.2 Candidate evidence

For each candidate, record:

- Candidate parser version and immutable artifact checksum.
- Format classification evidence.
- Number of records evaluated.
- Training and held-out evaluation identifiers.
- Exact field extraction results.
- Type-validation results.
- Schema compatibility results.
- Invalid or missing-field counts.
- False extraction and ambiguous-boundary counts.
- Differential test results against the current parser.
- Latency and resource cost.
- Confidence calibration where sufficient ground truth exists.
- Known limitations and unsupported record variants.

Do not use the same records both to generate and validate a candidate without disclosing the overlap.

Prevent evaluation leakage. A parser that reproduces its own training examples is not necessarily generalizable.

### 6.3 Evidence-gated decision policy

Implement configurable policies for:

- ACCEPT FOR SHADOW.
- ACCEPT FOR CANARY.
- ACCEPT FOR APPROVAL.
- PROMOTE.
- ABSTAIN.
- QUARANTINE.
- REJECT.
- ROLLBACK.

Thresholds must be derived from defined metrics, risk class, sample size, source policy, and evaluation evidence. They must not be invented or presented as statistically meaningful without validation.

Allow source-specific thresholds for high-risk security, authentication, billing, or audit records.

When the engine cannot distinguish competing interpretations reliably, it must preserve the original record and abstain or produce an explicitly partial parse.

### 6.4 Production safety

- Candidate generation never changes the active production parser directly.
- Shadow parsing cannot alter production output.
- Canary promotion is versioned and auditable.
- Promotion binds the exact tested artifact checksum to the deployed artifact.
- Approval requirements are configurable and support separation of duties.
- Rollback restores a previously approved parser version.
- Rollback itself is tested.
- An emergency freeze stops candidate promotion without stopping ordinary parsing.
- Parser changes cannot silently rewrite historical output.
- Historical reprocessing requires an explicit job, scope, authorization, and audit record.

---

# PART V — PARSER COMPILER AND EXECUTION SAFETY

## 7. SAFE PARSER COMPILER

Create or extend a compiler and runtime for NLI-owned parser definitions.

Use a declarative, versioned parser representation where possible instead of generating unrestricted executable source code.

Support:

- Schema validation.
- Type checking.
- Pattern compilation.
- Field mapping.
- Normalization rules.
- Required and optional fields.
- Conditional branches.
- Nested structures.
- Multiline rules.
- Parser dependency validation.
- Static complexity checks.
- Bounded execution.
- Deterministic output.
- Parser artifact checksums.
- Compatibility checks.
- Explainable compile errors.
- Reproducible parser builds.

### 7.1 Regex and pattern safety

- Reject or isolate unsafe regular expressions.
- Prefer linear-time matching engines when they meet the required feature set.
- Use timeouts or equivalent execution budgets where available.
- Bound pattern size, input length, nesting, and capture counts.
- Detect pathological backtracking.
- Test adversarial inputs.
- Cache compiled patterns with bounded memory.
- Evict caches safely.
- Avoid recompiling a parser for each record.
- Never permit an untrusted parser to execute unrestricted shell commands or arbitrary host code.

### 7.2 Runtime isolation

Run candidate testing and untrusted parser experimentation in a restricted environment.

Apply CPU, memory, wall-clock, file-access, and process limits as supported by the platform.

A candidate crash must not terminate the main ingestion pipeline.

Expose compilation failures, timeouts, rejected patterns, and resource-limit events through real telemetry.

---

# PART VI — SEMANTIC NORMALIZATION AND DATA QUALITY

## 8. SCHEMA AND SEMANTIC STUDIO

Parsing is not complete when a message is merely split into strings. NUPIRE must produce semantically useful fields without inventing meaning.

Implement:

- Source-specific schemas.
- Optional canonical schemas.
- Field alias mapping.
- Explicit field types.
- Timestamp parsing with timezone awareness.
- Numeric and boolean conversion.
- IP and port validation.
- Duration and unit normalization.
- Safe nested object creation.
- Null, absent, invalid, and unknown distinctions.
- Duplicate-field handling.
- Schema versioning.
- Backward compatibility analysis.
- Schema drift detection.
- Original-value retention when conversions are lossy.
- Clear validation errors.

### 8.1 Canonical event model

Use the existing NLI event contract if one exists. Otherwise design a versioned model that can carry:

- Event identity.
- Source identity.
- Node and component identity.
- Event timestamp and ingestion timestamp.
- Raw message reference or preserved raw message.
- Detected format and parser identity.
- Parser version and schema version.
- Structured fields.
- Parsing status.
- Confidence and ambiguity metadata where justified.
- Validation warnings.
- Provenance.
- Correlation and trace identifiers where available.

Do not force every source into a rigid schema that loses source-specific fields.

Use a common envelope plus extensible source-specific attributes where appropriate.

### 8.2 Semantic validation

Validate inferred fields against evidence and defined rules.

Examples include:

- Timestamps that must be parseable and within plausible bounds.
- Ports that must fit their permitted range.
- IP addresses that must be syntactically valid.
- Durations with explicit units.
- Required identifiers that must not be silently fabricated.
- Nested payloads that must satisfy their schemas.
- Relationships between fields where source semantics are known.

A validation failure should identify the rule, actual value, source record, and recovery path.

Do not discard additional valid fields because one optional field failed validation.

---

# PART VII — ACCURACY, CONFIDENCE, AND BENCHMARKING

## 9. EVIDENCE & ACCURACY LAB

Implement an independent evaluation subsystem with reproducible datasets, ground truth, and clear metric definitions.

### 9.1 Separate evaluation dimensions

Measure at least:

1. Format-classification accuracy.
2. Event-framing accuracy.
3. Template identification quality.
4. Field-boundary accuracy.
5. Field extraction precision and recall.
6. Type-conversion accuracy.
7. Schema-validation success.
8. Semantic correctness where ground truth exists.
9. Full-record exact-match accuracy.
10. Parse coverage.
11. Abstention and quarantine rate.
12. False-success rate.
13. Performance and resource cost.

Do not combine these into a single misleading score.

Define how partial records, missing fields, extra fields, invalid types, duplicate keys, and ambiguous interpretations are scored.

### 9.2 Confidence calibration

A confidence value is meaningful only when it corresponds to a validated estimate or an explicitly defined heuristic.

Record the confidence method and version.

Where sufficient labeled data exists, evaluate calibration and error rates at different confidence thresholds.

Where ground truth is unavailable, show heuristic confidence or uncertainty as such. Do not label it proven accuracy.

### 9.3 Dataset separation

Maintain separate:

- Training/discovery samples.
- Development samples.
- Held-out evaluation samples.
- Regression fixtures.
- Adversarial inputs.
- Production-derived samples authorized for testing.

Use dataset hashes, provenance, license status, redaction status, and evaluation configuration to make results reproducible.

Do not redistribute datasets without permission.

### 9.4 Performance benchmarks

Measure:

- Records per second.
- Bytes per second.
- Throughput by source family.
- p50/p95/p99 parsing latency.
- End-to-end ingestion latency.
- CPU consumption.
- Resident memory.
- Allocation rate.
- Parser compilation time.
- Cache hit rate.
- Quarantine rate.
- Queue depth and queue age.
- Performance under format drift.
- Performance during concurrent parser candidate testing.

Include the actual hardware, software versions, worker configuration, data mix, test duration, warm-up, and methodology.

Benchmark the existing v18 implementation as the baseline. Report improvements and regressions using the same reproducible workload.

Do not claim universal superiority over competitors unless the comparison uses equivalent datasets, settings, hardware, and methodology.

---

# PART VIII — DRIFT DETECTION AND SAFE EVOLUTION

## 10. DRIFT & EVOLUTION CENTER

Detect when a previously working parser may no longer represent the incoming data.

Signals can include:

- Increased parse failures.
- Unexpected missing fields.
- Changed token positions.
- New template clusters.
- Changed timestamp patterns.
- Type-distribution changes.
- Schema incompatibility.
- Increased quarantine.
- Increased ambiguity.
- Significant latency or resource changes.

Distinguish genuine format drift from transient malformed records, deployments, traffic changes, and source outages.

Use configurable minimum sample counts, observation windows, and thresholds. Avoid noisy alerts caused by tiny sample sizes.

For every detected drift event, provide:

- Affected source and parser version.
- First and latest observation times.
- Representative redacted examples where permitted.
- Field-level and structural differences.
- Estimated impact based on observed records.
- Candidate parser availability.
- Evaluation status.
- Recommended action.
- Rollback or mitigation path.

Do not change production parser behavior merely because drift is detected.

---

# PART IX — QUARANTINE, REPLAY, AND DATA RECOVERY

## 11. QUARANTINE & RECOVERY CENTER

Every input record must end in a defined state.

Possible outcomes:

- Fully parsed and validated.
- Parsed with noncritical warnings.
- Partially parsed.
- Unknown format.
- Ambiguous interpretation.
- Invalid structure.
- Failed semantic validation.
- Quarantined.
- Explicitly rejected under a documented policy.

Never silently drop a record because a parser fails.

Preserve original data or a recoverable reference, parser version, source identity, timestamps, failure stage, error reason, and replay metadata according to the existing storage and retention policy.

Support:

- Search and filtering.
- Failure categorization.
- Grouping by parser version and error signature.
- Safe manual inspection.
- Corrected-parser replay.
- Bounded bulk replay.
- Replay progress and cancellation.
- Idempotent replay behavior where practical.
- Before/after comparisons.
- Output lineage.
- Audit history.
- Retention and deletion policies.
- Authorization and tenant isolation.

Do not replay the same records repeatedly without clear job identity and idempotency controls.

Do not let a quarantine storage outage silently discard failed records. Apply an explicit bounded recovery policy and expose overload or persistence failure.

---

# PART X — REGISTRY, APPROVALS, AND ROLLBACK

## 12. PARSER REGISTRY & ROLLBACK

Implement or extend a versioned parser registry.

Each immutable parser artifact must include:

- Parser ID.
- Version.
- Source and format scope.
- Schema version.
- Compiler/runtime compatibility.
- Artifact checksum.
- Creation time and author.
- Research and implementation provenance.
- Test dataset references and hashes.
- Evaluation results.
- Benchmark results.
- Security review status.
- License review status where relevant.
- Approval history.
- Deployment state.
- Previous approved version.
- Rollback compatibility.

Provide lifecycle states such as:

DRAFT → COMPILED → TESTED → SHADOW → CANARY → APPROVED → ACTIVE → DEPRECATED → RETIRED

A parser must not enter ACTIVE without the required evidence and approvals.

### 12.1 Rollback report

For each parser deployment, generate a report containing:

- Current and previous parser versions.
- Artifact checksums.
- Change summary.
- Expected behavior changes.
- Test and benchmark evidence.
- Number of records processed by each version.
- Observed parse failure rate before and after.
- Field-level differences.
- Quarantine impact.
- Performance impact.
- Approval and deployment timestamps.
- Rollback trigger.
- Rollback execution status.
- Recovery verification.
- Remaining risks.

Support immediate rollback to a previously approved compatible parser when configured safety thresholds are violated.

Do not promise that rollback can reverse records already delivered externally. Where outputs are immutable or downstream effects cannot be undone, report that limitation and provide controlled reprocessing or compensation workflows.

---

# PART XI — OPTIONAL AI-ASSISTED DISCOVERY

## 13. AI MUST BE OPTIONAL AND CONTROLLED

AI may help propose field meanings, identify candidate structures, explain parse failures, and suggest parser definitions.

It must not be the mandatory parser for every record.

Requirements:

- Disabled by default unless existing policy explicitly enables it.
- No external log transmission without authorization.
- Redaction and data minimization.
- Explicit model/provider configuration.
- Budget and timeout limits.
- Prompt-injection resistance for log contents.
- Strict output schemas.
- No direct execution of model-generated code.
- Deterministic validation after AI suggestions.
- Candidate-only output.
- Audit trail and model/version provenance.
- No autonomous production promotion.
- Local model support where already available and suitable.

Treat all log contents as untrusted data. A message containing instructions must never override NLI system policies.

AI-generated parser candidates must pass the same deterministic compiler, held-out testing, security checks, and approval process as any other candidate.

---

# PART XII — LIVE NLI INTEGRATION AND UI

## 14. ADD THE NEW SUBSYSTEM WITHOUT BREAKING V18

Inspect existing NLI tabs, routes, navigation, API proxy, controller integration, authentication, database, and shared telemetry system.

Reuse existing pages and extend them where appropriate. Add new tabs only where they provide genuinely distinct functionality.

Proposed navigation:

1. Parsing Command Center
2. Universal Log Explorer
3. Format Discovery Lab
4. Parser Strategy Engine
5. Parser Compiler & Optimizer
6. Schema & Semantic Studio
7. Evidence & Accuracy Lab
8. Drift & Evolution Center
9. Quarantine & Recovery Center
10. Parser Registry & Rollback
11. Research & License Ledger
12. Benchmarks & Cost Lab
13. Live Integration & Health

If equivalent existing screens already exist, integrate NUPIRE into them instead of creating duplicates.

### 14.1 Parsing Command Center

Display real values for:

- Input and parse rates.
- Records successfully parsed.
- Partial parses.
- Failed parses.
- Quarantine volume.
- Active parser versions.
- Candidate jobs.
- Drift incidents.
- Parser latency distributions.
- Queue depth and age.
- Source and destination health.
- Telemetry freshness.

Do not invent values or display unavailable values as zero.

### 14.2 Universal Log Explorer

Show a real record's:

- Original message.
- Framing result.
- Detected format hypotheses.
- Selected parser and version.
- Structured JSON output.
- Field types and normalization.
- Warnings and validation errors.
- Confidence evidence.
- Candidate comparisons where available.
- Source lineage.
- Quarantine or replay history.

Use proper access controls and redaction. Never expose sensitive production records to unauthorized users.

### 14.3 Format Discovery Lab

Provide real discovery jobs with:

- Input selection.
- Sample size and bounds.
- Clusters and representative examples.
- Token/field structure visualization.
- Candidate parser generation.
- Job progress.
- Cancellation.
- Resource usage.
- Evaluation evidence.
- Export of safe parser specifications.

Progress must reflect actual work, not fabricated percentages.

### 14.4 Parser Strategy Engine

Explain which strategies were evaluated, which were skipped, why the selected parser won, and why other candidates failed.

Expose strategy-level latency and accuracy evidence without revealing sensitive input to unauthorized users.

### 14.5 Compiler and schema tools

Provide compile/test results, schema previews, validation errors, compatibility reports, and safe diff views.

Do not execute production changes from a preview action.

### 14.6 Drift and rollback screens

Provide version comparisons, incident history, approvals, candidate status, deployment state, rollback reports, and auditable recovery actions.

Use explicit confirmation and authorization for production-changing actions.

### 14.7 Research and license ledger

Provide a searchable inventory of reviewed repositories, evidence, license status, dependency usage, unresolved legal questions, and approval state.

Never label an unreviewed repository as legally approved.

---

# PART XIII — ONE SHARED LIVE SYNCHRONIZATION FABRIC

## 15. CONNECT NUPIRE TO EXISTING NLI TELEMETRY

Reuse the existing NURSF or equivalent synchronization system.

Do not create separate polling loops for each NUPIRE page.

Publish canonical events for:

- Parser start, stop, and state changes.
- Parser candidate creation.
- Compilation outcomes.
- Validation and benchmark progress.
- Candidate acceptance and rejection.
- Drift detection.
- Quarantine and replay.
- Parser activation and rollback.
- Parser throughput and latency.
- Parser resource use.
- Data-quality and output-validation failures.

Use the existing authenticated snapshot API and incremental stream where available.

Support sequence numbers or revisions, reconnect, cursor resumption, gap detection, and snapshot resynchronization.

Keep telemetry bounded and aggregated. Do not publish every internal operation as a network event.

A disconnected webapp must not affect parsing.

Target approximately one-second updates for eligible operational metrics where feasible, but measure actual propagation latency. Do not claim a one-second end-to-end SLA without evidence.

All screens must agree on shared state and distinguish healthy, degraded, failed, stale, unknown, and unavailable telemetry.

---

# PART XIV — PERFORMANCE AND RELIABILITY

## 16. ULTRA-HIGH-THROUGHPUT DESIGN

Treat high performance as an engineering objective that must be measured.

Use:

- Streaming and incremental processing.
- Bounded concurrent workers.
- Efficient tokenization.
- Compiled parser caching.
- Reuse of immutable parser artifacts.
- Batch operations where safe.
- Source-aware routing.
- Backpressure.
- Bounded queues.
- Controlled memory usage.
- Fair scheduling.
- Separate discovery and benchmark workers.
- Independent destination delivery.
- Incremental metrics aggregation.

Avoid:

- Full-file rescans for each update.
- Recompiling patterns for each record.
- Unbounded clustering state.
- Per-record database calls when batching is appropriate.
- Network calls from the parsing hot path.
- Full page rerenders for every metric update.
- Synchronous model inference on ordinary parsing.
- Global locks that serialize unrelated source streams.
- Unbounded caches or quarantine queues.

Preserve ordering only where required by source semantics. Make parallelism and partitioning behavior explicit.

Implement configurable limits for record size, nesting depth, field count, token count, processing time, memory, candidate count, and concurrent discovery jobs.

When a limit is exceeded, produce an explicit outcome and preserve recoverable evidence according to policy.

---

# PART XV — TESTING AND RELEASE VERIFICATION

## 17. BUILD A REAL TEST PROGRAM

Add unit, integration, property-based, fuzz, regression, and end-to-end tests using the repository's existing frameworks where practical.

### 17.1 Parser tests

Cover:

- Known valid formats.
- Previously unseen formats.
- Empty and malformed records.
- Unicode and encoding edge cases.
- Embedded JSON.
- Quoted and escaped delimiters.
- Nested payloads.
- Timestamps and timezones.
- Long lines.
- Very large numbers of fields.
- Stack traces and multiline events.
- Missing and duplicate keys.
- Type-conversion failures.
- Ambiguous field boundaries.
- Schema drift.
- Malicious regex inputs.
- Pathological nesting.
- Parser timeout and resource limits.

### 17.2 Evolution tests

Verify:

- Candidate generation.
- Held-out evaluation.
- Evidence scoring.
- Candidate rejection.
- Shadow execution.
- Canary deployment.
- Approval policy.
- Artifact checksum verification.
- Production promotion.
- Drift detection.
- Rollback.
- Recovery after a crash.
- Historical replay.

### 17.3 Integration tests

Verify that real input records flow through the existing NLI source readers, parser runtime, normalized JSONL output, quarantine, and configured destinations.

Confirm that NUPIRE's output is visible in the existing Monitoring, NCIC, and relevant webapp screens through shared telemetry.

Verify that an NUPIRE crash or slow benchmark job does not interrupt the ordinary ingestion path.

### 17.4 Release tests

Test:

- Existing v18 regression suite.
- Configuration migration.
- API compatibility.
- Database migration.
- RPM installation and upgrade.
- systemd service lifecycle.
- Restart and recovery.
- Rollback.
- Release artifact contents.
- Security and dependency scanning.

Preserve existing enrollment and TLS verification behavior.

Do not change production certificates, public endpoints, credentials, firewall settings, or deployment topology without explicit authorization.

---

# PART XVI — IMPLEMENTATION ORDER

## 18. REQUIRED WORK PHASES

Execute the project in dependency-aware stages.

### Phase 1: Audit

Inspect v18, establish the baseline, identify existing capabilities and create the gap matrix.

### Phase 2: Research

Verify and investigate at least 100 distinct repositories. Produce the research matrix, license ledger, and gap analysis. Do not pretend a URL inventory is a source-level review.

### Phase 3: Architecture

Design the NUPIRE interfaces, canonical parser specification, evidence model, registry integration, and runtime lifecycle.

### Phase 4: Core deterministic parsing

Implement or repair framing, classification, specialized adapters, tokenization, extraction, normalization, validation, and output handling.

### Phase 5: Discovery and candidate generation

Implement bounded format discovery, template mining, parser hypotheses, and candidate generation.

### Phase 6: EGPE and compiler

Implement safe compilation, held-out validation, candidate scoring, shadow runs, approval, promotion, and rollback.

### Phase 7: Drift and recovery

Integrate drift detection, quarantine, replay, parser version history, and recovery reporting.

### Phase 8: Live webapp integration

Wire the actual UI, APIs, shared state, live telemetry, and existing dashboards. Add new tabs only where necessary.

### Phase 9: Benchmarks and assurance

Run reproducible correctness, adversarial, performance, recovery, and regression tests.

### Phase 10: Release v20

Update version metadata, packaging, configuration migration, documentation, release artifacts, and rollback procedures.

Adapt the order only when repository evidence justifies doing so. Do not skip research or testing merely to generate a release quickly.

---

# PART XVII — ACCEPTANCE GATES

## 19. DEFINITION OF DONE

NLI v20 is ready only when all applicable gates are verified.

### Parsing correctness

- Original records remain recoverable.
- Known formats use deterministic, tested parsers.
- Unknown formats can enter the discovery workflow.
- Invalid and ambiguous records are handled explicitly.
- JSON output is syntactically valid.
- Type and schema errors are observable.
- Parser confidence is not fabricated.
- Regression performance is measured against v18.

### Candidate safety

- Candidate parsers are isolated from production.
- Candidate evaluation uses separate held-out evidence.
- Parser artifacts are immutable and checksum-bound.
- Production promotion follows configured approval policy.
- Rollback is tested.
- Historical replay is auditable and bounded.

### Reliability

- Parser failures do not silently lose records.
- Candidate jobs do not block ingestion.
- Memory, queues, execution time, and concurrency are bounded.
- Restart recovery is verified.
- Quarantine and replay are observable.
- Destination failures remain isolated.

### Webapp and telemetry

- All new capabilities are wired to actual backend data.
- Shared telemetry is reused.
- Live updates have measurable freshness.
- The UI displays real job states and errors.
- No mock production values remain.
- Authorization applies to every endpoint and stream.
- Existing v18 screens and workflows remain functional.

### Commercial readiness

- Repository research evidence exists.
- Third-party dependencies are inventoried.
- License and provenance risks are recorded.
- Security checks and regression tests are documented.
- Release installation and rollback are tested.

If any gate fails, state exactly which gate failed, why, what evidence supports the conclusion, and what remains to be done.

---

# PART XVIII — FINAL DELIVERABLES

## 20. REQUIRED FINAL REPORT

Deliver a clear report containing:

1. The verified NLI v18 baseline.
2. The count of repositories actually inspected at source level.
3. The research matrix and license ledger.
4. The major parsing approaches studied.
5. Evidence-backed limitations and unresolved issues.
6. The ranked gap analysis.
7. The NUPIRE architecture and EGPE decision policy.
8. All added or changed modules and files.
9. API and schema changes.
10. Parser specifications and registry changes.
11. New tabs, routes, and live integrations.
12. Correctness and confidence methodology.
13. Test commands and exact outcomes.
14. Benchmark setup and measured results.
15. Security and licensing review status.
16. Configuration migration and deployment instructions.
17. RPM/systemd and release changes.
18. Rollback and recovery instructions.
19. Known limitations and blocked work.
20. A final acceptance matrix showing PASS, FAIL, BLOCKED, or NOT APPLICABLE with supporting evidence.

Never state that 100 repositories were reverse engineered unless the corresponding source-level review evidence exists.

Never state that 100% accuracy has been achieved.

Never state that all features are integrated merely because the UI has been created.

Never replace actual test evidence with a written plan.

---

## FINAL DIRECTIVE

Build NUPIRE as a new, deeply integrated NLI v20 subsystem.

The distinctive engineering contribution is **Evidence-Gated Parser Evolution**: candidate parsers compete based on measurable evidence, production changes are controlled, drift is detected, uncertainty is preserved, and every deployment can be audited and rolled back.

Research established projects to learn their techniques and limitations. Independently design the best architecture for NLI. Protect the commercial product through license and provenance controls. Preserve all working v18 capabilities. Integrate the new parser engine with the real ingestion path, real normalized output, real telemetry, existing monitoring, and tested recovery workflows.

**Research first. Prove the gaps. Design independently. Implement end to end. Measure accuracy and speed. Preserve evidence. Roll back safely. Release NLI v20 only with verifiable results.**