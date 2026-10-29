# V20 research gap D6: targeted source pass

Scope: #76, #82, #84, #85, #88, #89, #97, #101, #102, and #103. Previously counted repositories #80, #83, and #90 are excluded to prevent duplicate credit. Qualification requires directly inspectable implementation, corresponding test/fixture, an exact license file, and repository activity evidence. **Exact new qualified count: 1 distinct repository.** No upstream code copied; only this research note changed.

## Qualified

### #89 apache/logging-log4net — pattern-based event formatting

- Evidence: implementation [`PatternLayout.cs`](https://github.com/apache/logging-log4net/blob/master/src/log4net/Layout/PatternLayout.cs); focused test [`PatternLayoutTest.cs`](https://github.com/apache/logging-log4net/blob/master/src/log4net.Tests/Layout/PatternLayoutTest.cs); root [`LICENSE`](https://github.com/apache/logging-log4net/blob/master/LICENSE); repository reports 1,675 commits and recent commit activity, including a 2026 pattern-layout test documentation change.
- Observed: `PatternLayout` maps conversion tokens to converter types and formats logging events according to a supplied pattern. It supports aliases and width/truncation options for fields. The test module is specific to PatternLayout. No malformed log ingestion or schema evolution behavior is claimed; this is output formatting code.
- NLI transfer: keep field extraction separate from rendering; make the output pattern explicit and test token aliases, width/truncation, and defaults. Formatting policy should not silently change parsed values.
- Caveats: Apache-2.0 root license; check per-file notices and dependencies before reuse. The source is a logger formatter, not a general parser. Confidence: high for the narrow source/test scope.

## Excluded

- #76 `yaml/libyaml`: implementation [`src/parser.c`](https://github.com/yaml/libyaml/blob/master/src/parser.c) and focused test [`tests/test-reader.c`](https://github.com/yaml/libyaml/blob/master/tests/test-reader.c) are directly readable; repository history shows 228 commits. However, the expected root `LICENSE` path returned 404 during direct inspection; project/repository metadata describes MIT licensing but does not satisfy the exact LICENSE-file gate. Excluded.
- #82 `apache/avro`: [`Schema.java`](https://github.com/apache/avro/blob/main/lang/java/avro/src/main/java/org/apache/avro/Schema.java) and current releases (1.12.2) are evidenced. `TestSchema.java` path was indicated in upstream change history but its test source was not directly retrievable in this pass; root `LICENSE` returned 404. Excluded; do not infer license-file evidence from a source header.
- #84 `msgpack/msgpack`: this upstream is the format specification (`spec.md`), not an implementation repository. A different project, `msgpack/msgpack-c`, provides C/C++ implementations, but it is not the prompt's canonical repo and cannot be substituted. Excluded.
- #85 `serde-rs/serde`: inspected [`serde_core/src/de/mod.rs`](https://github.com/serde-rs/serde/blob/master/serde_core/src/de/mod.rs) and [`serde/src/lib.rs`](https://github.com/serde-rs/serde/blob/master/serde/src/lib.rs); package metadata establishes dual MIT/Apache-2.0 and repo is active. Matching test source was not established from the current workspace layout (`serde_core` is the current implementation location); no exact implementation-test-license-activity set, so excluded.
- #88 `apache/logging-log4j2`: [`Logger.java`](https://github.com/apache/logging-log4j2/blob/2.x/log4j-core/src/main/java/org/apache/logging/log4j/core/Logger.java) is readable and current branch activity exists, but the candidate test path did not resolve to readable source in this pass. Excluded.
- #97 `opensearch-project/OpenSearch`: inspected REST handler [`RestSearchAction.java`](https://github.com/opensearch-project/OpenSearch/blob/main/server/src/main/java/org/opensearch/rest/action/search/RestSearchAction.java), root [`LICENSE.txt`](https://github.com/opensearch-project/OpenSearch/blob/main/LICENSE.txt), and current repository page. The corresponding test path `RestSearchActionTests.java` was not directly retrievable, so no test-behavior claim and no credit.
- #101 `zeek/zeek`: current v8.2.2 release history establishes activity, but this pass did not pin a focused runtime implementation file and matching test/fixture in the same relevant path. Excluded.
- #102 `OISF/suricata`: [`src/detect-engine.c`](https://github.com/OISF/suricata/blob/main/src/detect-engine.c) and [`COPYING`](https://github.com/OISF/suricata/blob/main/COPYING) are readable; activity is current. A corresponding focused test source could not be verified in this pass, so excluded.
- #103 `mitre/cti`: repository is a STIX 2.0 threat-intelligence dataset and provides [`LICENSE.txt`](https://github.com/mitre/cti/blob/master/LICENSE.txt) and ongoing data updates. It contains data and usage guidance rather than a relevant implementation/test pair. Excluded.

