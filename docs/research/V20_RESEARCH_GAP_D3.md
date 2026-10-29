# V20 research gap D3 — threat intel, endpoint, and fuzzing candidates

## Exact new qualified count

**5 new distinct upstreams qualified**: #105 SigmaHQ/sigma, #106 osquery/osquery, #107 Velocidex/velociraptor, #108 Caldera (canonical current repo `apache/caldera`, historically `mitre/caldera`), and #62 ohmjs/ohm. No candidate repeats a qualified upstream from D1. The research also deepens #7 AutoLog, which remains excluded. No source code copied; only this memo created.

## Qualified reviews

### #105 SigmaHQ/sigma

- Evidence: [rule/parser source and corpus](https://github.com/SigmaHQ/sigma/tree/master/rules), [rule validation test](https://github.com/SigmaHQ/sigma/blob/master/tests/test_rules.py), [LICENSE](https://github.com/SigmaHQ/sigma/blob/master/LICENSE), [repository history](https://github.com/SigmaHQ/sigma/commits/master/). The repository identifies itself as the main Sigma rule repository and reports 16,886 commits. `tests/test_rules.py` is a concrete unittest validator for rules; it checks common errors/noncompliance across the YAML rule corpus. Rules encode log source, detection selection/condition, metadata, and fields; they are portable detection logic requiring conversion/runtime support, not generic parser inference. License is Detection Rule License (DRL) 1.1, so content reuse has attribution/redistribution terms distinct from code licensing. NLI transfer: schema and fixture discipline, normalized source metadata, and machine-readable detection predicates. Dependency on YAML/Sigma tooling and backend-specific semantics. Confidence: high.

### #106 osquery/osquery

- Evidence: [table implementation tree](https://github.com/osquery/osquery/tree/master/osquery/tables), [integration tests](https://github.com/osquery/osquery/tree/master/tests/integration/tables), [LICENSE](https://github.com/osquery/osquery/blob/master/LICENSE), [master history](https://github.com/osquery/osquery/commits/master/). The official table development guide specifies generated C++ from Python table specs, C++ implementations, and end-to-end SQL integration tests; it explicitly describes enabling integration tests and fuzz-path declarations for filesystem parsers. The project uses a dual SPDX license (Apache-2.0 OR GPL-2.0-only) in source examples. NLI transfer: query-aware structured extraction, per-table modularity, and requirement for end-to-end fixtures; it is endpoint instrumentation rather than a log parser. C++/SQLite build surface and dual licensing need review. Confidence: high.

### #107 Velocidex/velociraptor

- Evidence: [VQL/artifact source](https://github.com/Velocidex/velociraptor/tree/master/artifacts), [artifact test fixtures](https://github.com/Velocidex/velociraptor/tree/master/artifacts/testdata), [AGPL-3.0 LICENSE](https://github.com/Velocidex/velociraptor/blob/master/LICENSE), [release history](https://github.com/Velocidex/velociraptor/releases). Current release listing includes v0.77.3 dated 2026-10-05. Repository says VQL is used to collect host state and its CI Windows workflow builds and runs tests; contributor guide names `make test`. Artifacts are declarative query/collection definitions, with concrete YAML testcases. NLI transfer: data-driven collection/query definitions and fixtures; product is endpoint forensics, not general log template discovery. AGPL network-use obligations and broad endpoint privileges make adoption risk high. Confidence: high.

### #108 `apache/caldera` (current canonical successor to `mitre/caldera`)

- Evidence: [core implementation](https://github.com/apache/caldera/tree/master), [pytest test tree](https://github.com/apache/caldera/tree/master/tests), [LICENSE](https://github.com/apache/caldera/blob/master/LICENSE), [master history](https://github.com/apache/caldera/commits/master/). Current README describes an asynchronous C2 server/API and plugin architecture; contributor guide requires tests and documents pytest/tox runs. `mitre/caldera` remains the historical clone URL in contributor instructions, while the active canonical repository is `apache/caldera`. Use only for controlled adversary emulation: operational/security risk is substantial and irrelevant to ordinary log parsing. Third-party/plugin licenses vary. Confidence: high on upstream identity and architecture; low on NLI algorithm transfer.

### #62 `ohmjs/ohm` (deepened from D1; newly qualified here)

- Evidence: [grammar implementation](https://github.com/ohmjs/ohm/tree/main/packages/ohm-js/src), [unit tests](https://github.com/ohmjs/ohm/tree/main/packages/ohm-js/test), [MIT LICENSE](https://github.com/ohmjs/ohm/blob/main/LICENSE), [main history](https://github.com/ohmjs/ohm/commits/main/). Current repository view shows 2,175 commits; contributor instructions say `pnpm test` runs unit tests and identify `src/ohm-grammar.ohm`, bootstrap rebuild, and test workflow. Ohm separates PEG grammar from semantic actions, supports left recursion and grammar extension, and provides parse-match APIs. NLI transfer: separate log grammar definitions from parsed-record transforms and keep grammar fixtures testable. PEG/runtime dependency and ambiguous/large-input performance should be benchmarked. Confidence: high.

## Excluded candidates

- #103 `mitre/cti`: current repo contains STIX 2.0 ATT&CK/CAPEC datasets; [`LICENSE.txt`](https://github.com/mitre/cti/blob/master/LICENSE.txt) and 351-commit history are visible, but no source implementation/test suite or validation fixture set is identified in repository root. It is a data corpus, and the project directs STIX 2.1 consumers to `mitre-attack/attack-stix-data`. Excluded for missing repo-level test evidence and low NLI code transfer.
- #109 `OWASP/CheatSheetSeries`: source Markdown, [CC BY-SA 4.0 project license](https://owasp.org/projects/cheat-sheet-series), and active contribution history exist, but this is published guidance, not an implementation repository with test suite/fixtures. Excluded.
- #110 `google/fuzzing`: exact canonical repository was not resolved confidently; search results instead surfaced `google/oss-fuzz`, `google/honggfuzz`, and `googleprojectzero/fuzzilli`, distinct projects. Since the requested candidate could not be resolved to exact implementation + tests + license + activity evidence, excluded rather than substituted.
- #7 `logpai/AutoLog` (deeper review): [source directory](https://github.com/logpai/AutoLog/tree/main/src), [demo data fixtures](https://github.com/logpai/AutoLog/tree/main/data), [MIT LICENSE](https://github.com/logpai/AutoLog/blob/main/LICENSE), and repository activity (updated 2024-02-20) are identifiable. It performs Java bytecode/log-statement and execution-path analysis, then walks call paths to synthesize sequences; useful as a source-assisted log-sequence generation research reference. But no independently identifiable test suite or automated test/fixture harness was found; demo datasets are outputs, not test proof. Excluded under the evidence rule. Confidence: medium on architecture, high on exclusion basis.

## Limits

This is a targeted source review, not execution of upstream tests, security audit, dependency/license closure, or performance assessment. For Sigma, rule-content terms should be checked independently from code license. For Velociraptor and Caldera, operational threat models matter before any live deployment. Exact new qualified count: **5**.
