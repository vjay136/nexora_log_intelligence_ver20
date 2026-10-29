# V20 research gap D1 — parser and log-processing candidates

## Exact count

**7 distinct upstreams qualified**: #25 OpenTelemetry Collector Contrib, #33 mtail, #53 Winnow, #61 Peggy, #64 Pigeon, #67 lexy, and #70 Instaparse. Qualification requires directly identifiable implementation, tests/fixtures, license, and activity evidence. This is repository-source review; no upstream code copied and no product files changed.

## Qualified candidates

### #25 `open-telemetry/opentelemetry-collector-contrib`

- Evidence: [filelog implementation](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/filelogreceiver), [filelog tests](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/filelogreceiver), [root LICENSE](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/LICENSE), [current repository history](https://github.com/open-telemetry/opentelemetry-collector-contrib/commits/main/). Operator documentation describes composable single-purpose parser/transformer operators and chained pipelines; filelog supports regex parsing, multiline split, checkpointed offsets, concurrency bounds (`max_concurrent_files`, default 1024), and batched polling. Tests include receiver package tests/fixtures. Active main history and current docs provide activity evidence.
- NLI transfer: useful architecture for separating acquisition, framing, parsing, and transforms; explicit file concurrency and offset state. It is a large Go ecosystem component, not a log-template miner. Review module/transitive licenses and the project's security advisories before adoption. Confidence: high for architecture and bounds; medium for direct algorithm transfer.
- Sources: [operator model](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/pkg/stanza/docs/operators/README.md), [file input bounds and multiline](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/pkg/stanza/docs/operators/file_input.md), [receiver docs](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/receiver/filelogreceiver/README.md).

### #33 `google/mtail`

- Evidence: [compiler/parser sources](https://github.com/google/mtail/tree/main/internal), [tests and example programs](https://github.com/google/mtail/tree/main/internal), [Apache-2.0 LICENSE](https://github.com/google/mtail/blob/main/LICENSE), [main history](https://github.com/google/mtail/commits/main/). README identifies parser/compiler development and `goyacc`; repository view shows 4,318 commits. Language documentation gives the execution model: each program runs on each input line, pattern/action rules use Go RE2 syntax, and state persists through declared variables. Regex engine therefore avoids backtracking constructs unsupported by Go regexp. Tests/examples are maintained in repository.
- NLI transfer: line-oriented bounded semantics and regex compatibility are valuable; mtail extracts metrics, rather than inferring event templates. Go module dependency and generated parser maintenance are caveats. Confidence: high.
- Sources: [language and semantics](https://github.com/google/mtail/blob/main/docs/Language.md), [repository/readme and activity](https://github.com/google/mtail), [tests](https://github.com/google/mtail/tree/main/internal).

### #53 `winnow-rs/winnow`

- Evidence: [parser implementation](https://github.com/winnow-rs/winnow/tree/main/src), [test suite](https://github.com/winnow-rs/winnow/tree/main/tests/testsuite), [MIT license](https://github.com/winnow-rs/winnow/blob/main/LICENSE-MIT), [changelog](https://github.com/winnow-rs/winnow/blob/main/CHANGELOG.md). Repository lists 4,839 commits and 112 tags; changelog records 1.0.4 (2026-07-13). It is a Rust parser-combinator library with explicit backtracking/error controls and examples; test suite and fuzz/property infrastructure are present.
- NLI transfer: can inform a Rust streaming/framing parser and composable grammar, while preserving explicit error/backtrack decisions. It is a library, not a log parser. Dependency/MSRV and API churn (changelog notes v1 reflects API churn) matter. Confidence: high.

### #61 `peggyjs/peggy`

- Evidence: [grammar parser source](https://github.com/peggyjs/peggy/blob/main/src/parser.pegjs), [tests](https://github.com/peggyjs/peggy/tree/main/test), [MIT LICENSE](https://github.com/peggyjs/peggy/blob/main/LICENSE), [main history](https://github.com/peggyjs/peggy/commits/main/). Repository view lists 2,076 commits and an active test/build setup. Peggy generates JavaScript parsers from PEG grammars; integrated lexical/syntactic analysis and structured error reporting are documented.
- NLI transfer: promising for declarative format grammars and rich diagnostics for known log dialects; generated parsers and embedded grammar actions introduce build, input-size, and dependency review needs. No log-template inference. Confidence: high.

### #64 `mna/pigeon`

- Evidence: [parser generator/runtime source](https://github.com/mna/pigeon/tree/master), [parser tests](https://github.com/mna/pigeon/blob/master/parse_test.go), [BSD-3-Clause LICENSE](https://github.com/mna/pigeon/blob/master/LICENSE), [master history](https://github.com/mna/pigeon/commits/master/). Repository lists 320 commits and documents test directory. PEG parser generator emits Go; API exposes error recovery, memoization, and `MaxExpressions` control. Input is UTF-8. The upstream notes its security policy tracks Go's support policy.
- NLI transfer: good candidate for maintainable Go grammars and bounded expression-count configuration; PEG choice/backtracking and generated source behavior need adversarial-input profiling. Confidence: high.

### #67 `foonathan/lexy`

- Evidence: [implementation](https://github.com/foonathan/lexy/tree/main/include), [tests](https://github.com/foonathan/lexy/tree/main/tests), [BSL-1.0 LICENSE](https://github.com/foonathan/lexy/blob/main/LICENSE), [main history](https://github.com/foonathan/lexy/commits/main/). Repository lists 1,232 commits. C++17 combinator DSL maps to recursive descent; documentation says backtracking/lookahead are explicit, supports Unicode, error recovery, and customizable callbacks.
- NLI transfer: useful model for deterministic high-throughput typed parsing and deliberate error recovery. BSL-1.0 has conditions; C++ integration and build/toolchain risk are material. Confidence: high for described design; medium for NLI fit.

### #70 `Engelberg/instaparse`

- Evidence: [GLL parser implementation](https://github.com/Engelberg/instaparse/tree/master/src/instaparse), [test suite](https://github.com/Engelberg/instaparse/tree/master/test), [EPL LICENSE](https://github.com/Engelberg/instaparse/blob/master/LICENSE), [master history](https://github.com/Engelberg/instaparse/commits/master/). Repository lists 829 commits and release 1.5.0. README documents arbitrary context-free grammars including left-recursive/ambiguous grammars, parse-error reporting, optional enumeration of all parses, and total-parsing mode. README explicitly warns that flexible grammars may perform poorly and that memoization can exhaust memory if not cleared between inputs.
- NLI transfer: useful for ambiguous log dialects and parse-tree diagnostics, but ambiguity/memory/performance make it a poor default for untrusted high-volume input. Clojure/JVM and EPL dependency implications. Confidence: high.

## Excluded candidates

- #7 `logpai/AutoLog`: source `src/` and MIT are identifiable; README provides generated demo data, but this pass did not establish a repository test suite or test fixture set. The separate paper is not test evidence. Excluded.
- #9 `BlueLionLogram/Logram`: source folders and algorithm description are visible, but root listing lacks an identifiable license file and formal test suite. Excluded.
- #10 MoLFI: LogPAI's license inventory identifies the canonical project as [`SalmaMessaoudi/MoLFI`](https://github.com/SalmaMessaoudi/MoLFI), but current upstream source/test/license/activity could not be verified as a complete evidence set. The mirrored entry in `logpai/logparser` is not counted as a substitute upstream. Excluded.
- #38 `elastic/kibana`: source, tests, LICENSE.txt, and active history exist, but this is a broad visualization product; the sampled evidence did not establish a focused parser implementation appropriate to this prompt. Root license is conditional: default triple AGPL-3.0-only/SSPL-1/Elastic-2.0, with x-pack-only Elastic License 2.0 code; do not assume permissive use. Excluded from qualified count.
- #62 `ohmjs/ohm`: contributor docs establish grammar source and unit tests, but this pass did not verify the exact license text and recent activity as a complete source/test/license/activity set. Excluded.

## Confidence limits

Activity means public repository history/release evidence, not a full maintainer responsiveness or vulnerability audit. No candidate's test suite was executed. Dependency closure, performance ceilings, security posture under malformed adversarial logs, and product fit require follow-up before adoption. Exact qualified count remains **7**.
