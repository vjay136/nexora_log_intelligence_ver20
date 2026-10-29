# V20 research gap D4: source-level review

Scope: new pass on #76, #80, #82, #84, #85, #88–90, #97, #101–102. Excludes #83 Thrift, qualified in D2, to avoid duplicate credit. Qualification requires directly identifiable implementation source, a corresponding test or fixture, repository license evidence, and activity evidence. **Exact new qualified count: 2 distinct repositories.** No upstream code copied; no consolidated ledger/checklist or product code edits.

## Qualified

### #80 BurntSushi/toml-test (now toml-lang/toml-test) — TOML conformance harness

- Evidence: canonical upstream documents move from BurntSushi to [`toml-lang/toml-test`](https://github.com/toml-lang/toml-test); runner implementation [`runner.go`](https://github.com/toml-lang/toml-test/blob/main/runner.go) / [`cmd/toml-test`](https://github.com/toml-lang/toml-test/tree/main/cmd/toml-test), harness tests [`runner_test.go`](https://github.com/toml-lang/toml-test/blob/main/runner_test.go), fixtures under [`tests`](https://github.com/toml-lang/toml-test/tree/main/tests), root [`LICENSE`](https://github.com/toml-lang/toml-test/blob/main/LICENSE). Repository listing reports 474 commits and current v2 installation guidance; changelog records the repository migration and recent test/spec changes.
- Observed: this is a test runner and fixture corpus, not a TOML parser. It invokes an implementation through a decoder/encoder interface, runs valid and invalid test groups, and treats decoder nonzero exit as rejection for invalid input. That architecture keeps language-neutral cases separate from parser internals.
- NLI transfer: model parser conformance as data-driven valid/invalid fixtures with a tiny adapter contract; preserve expected outcomes and per-case identifiers. Don't treat TOML semantics as log semantics. MIT license is present; do not copy fixtures without checking their individual provenance and notices. Harness dependencies were not audited. Confidence: medium-high for harness design; none for parser internals.

### #90 apache/logging-log4cxx — bounded asynchronous appender behavior

- Evidence: implementation [`asyncappender.cpp`](https://github.com/apache/logging-log4cxx/blob/master/src/main/cpp/asyncappender.cpp); corresponding test [`asyncappendertestcase.cpp`](https://github.com/apache/logging-log4cxx/blob/master/src/test/cpp/asyncappendertestcase.cpp); root [`LICENSE`](https://github.com/apache/logging-log4cxx/blob/master/LICENSE). The repository has current master commits; commit history includes recent maintenance fixes, including preventing a spurious 2 GB test failure.
- Observed: async appender uses a ring buffer (default capacity 128), atomics and a dispatch thread. A configurable blocking mode waits for capacity; nonblocking/full-buffer behavior coalesces discarded events into summaries. Dispatch catches sink exceptions, reports them, and bounds retries within a batch so a persistent sink failure does not endlessly retry that batch.
- NLI transfer: make backpressure and drop behavior explicit, expose counts/summaries for dropped records, and bound retry work when a sink fails. NLI should preserve source records or durable checkpoints according to its own contract; logging-framework lossy overflow behavior should not be copied blindly.
- Caveats: Apache-2.0 at root; check third-party and per-file notices before reuse. Concurrency and failure policy need targeted stress testing before adapting. Confidence: high on the inspected class behavior; medium on test coverage without running it.

## Excluded

- #76 `yaml/libyaml`: direct implementation [`src/api.c`](https://github.com/yaml/libyaml/blob/master/src/api.c), MIT project license and repository history are identifiable, but a corresponding readable parser test/fixture file was not established in this pass; excluded under the complete-evidence rule.
- #82 `apache/avro`: [`Schema.java`](https://github.com/apache/avro/blob/main/lang/java/avro/src/main/java/org/apache/avro/Schema.java) and Apache file header are readable; the corresponding test source, root LICENSE file, and activity evidence were not all established as a matching set here. Its schema model and schema evolution concepts remain promising follow-up research, but no credit.
- #84 `msgpack/msgpack`: this named upstream is the MessagePack specification, not an implementation; specification text alone cannot satisfy implementation + test + license requirements. Separate `msgpack/msgpack-c` is a different candidate and was not counted.
- #85 `serde-rs/serde`: repository/package metadata indicates active development and dual MIT/Apache-2.0 licensing, but this pass did not pin a relevant implementation file and matching behavior test with sufficient source evidence; excluded.
- #88 `apache/logging-log4j2`: `Logger.java` is implementation evidence, but exact corresponding test source and root LICENSE file were not both inspected in this pass; excluded.
- #89 `apache/logging-log4net`: `PatternLayout.cs` is implementation evidence and repository metadata shows Apache-2.0 and recent activity, but matching test source was not inspected; excluded.
- #97 `opensearch-project/OpenSearch`: project license and repository activity are visible, but this pass did not identify a narrowly scoped parser/ingestion implementation and matching test source; excluded.
- #101 `zeek/zeek`: release history shows current v8.2.2, but a matching implementation/test/license trio was not inspected; excluded.
- #102 `OISF/suricata`: `src/detect-engine.c` and active repository/QA evidence exist, but the corresponding test file and COPYING/license text were not directly verified together; excluded.

