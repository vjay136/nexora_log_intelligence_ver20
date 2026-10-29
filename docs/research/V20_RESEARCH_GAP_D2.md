# V20 research gap D2: source-level review

Scope: assigned candidates #76, #80, #82–85, #88–90, #97, #101–102. Qualification requires readable relevant implementation, a concrete test/fixture file, repository license evidence, and activity evidence. **Exact qualified count: 1 distinct repository.** No upstream source copied; no product or ledger edits.

## Qualified

### #83 apache/thrift — protocol decoding and serialization

- Evidence: [`TBinaryProtocol.py`](https://github.com/apache/thrift/blob/master/lib/py/src/protocol/TBinaryProtocol.py) is a concrete binary protocol implementation; [`SerializationTest.py`](https://github.com/apache/thrift/blob/master/test/py/SerializationTest.py) exercises protocol/serialization paths; root [`LICENSE`](https://github.com/apache/thrift/blob/master/LICENSE) is Apache-2.0 and explicitly lists bundled components with separate terms. Repository page exposes active `master` history (recently crawled; 1,000+ commits), and current source/test tree is present.
- Observed: Python binary protocol has explicit typed reads/writes over a transport, with protocol exceptions surfaced from invalid/unsupported paths. The test file includes binary-protocol serialization tests and framed transport coverage. This is a multi-language RPC framework; the observed Python files do not establish behavior of other language implementations or universal length bounds.
- NLI transfer: model decode failures separately from transport failures; define max frame/string/container sizes in NLI's own boundary; add round-trip and malformed/truncated fixture cases. Those are recommendations, not claims that this inspected implementation enforces every bound.
- Caveats: protocol parsers are security-sensitive; do not assume safe bounds based on this limited file pair. Apache-2.0 applies at project level, but root LICENSE calls out subcomponents with distinct licensing; audit per-file notices and dependencies before reuse. Confidence: medium for the narrow Python protocol/test observations, low for resource-limit conclusions.

## Excluded

- #76 `yaml/libyaml`: repo page confirms MIT and active history, but this pass did not directly inspect a relevant implementation and test file pair; no credit.
- #80 `BurntSushi/toml-test`: upstream moved to `toml-lang/toml-test`; this is a conformance harness, not the requested parser implementation. README/changelog evidence alone does not qualify.
- #82 `apache/avro`: `Schema.java` and its Apache header were readable, but a corresponding test source and root license file were not verified in the same source-level pass; no credit.
- #84 `msgpack/msgpack`: canonical repository is the MessagePack specification, not an implementation; no implementation/test/license trio to qualify.
- #85 `serde-rs/serde`: repository and package metadata are active and dual MIT/Apache-2.0, but this pass did not establish a specific implementation + test file pair with behavior inspected; no credit.
- #88 `apache/logging-log4j2`: `Logger.java` and Apache file header were inspected; a corresponding concrete test plus root LICENSE file were not directly verified here. Not counted.
- #89 `apache/logging-log4net`: repository page confirms Apache-2.0, source tree and history, but no specific implementation/test pair was inspected; no credit.
- #90 `apache/logging-log4cxx`: repository page confirms project identity, but no specific source/test/license file trio was inspected; no credit.
- #97 `opensearch-project/OpenSearch`: project-level Apache-2.0 evidence exists, but this pass did not inspect a bounded ingestion/parser implementation and corresponding test source; no credit.
- #101 `zeek/zeek`: current release evidence exists (v8.2.2 in release history), but no exact source/test/license trio was inspected; no credit.
- #102 `OISF/suricata`: `src/detect-engine.c` and repo QA description were inspected; source repository reports GPL-2.0 and test/QA infrastructure, but a corresponding focused test file and COPYING/license text were not directly inspected. Not counted.

