# V20 research gap C — parser and grammar candidates

## Exact count

**2 new distinct upstreams qualified** under the requested source + test + license + activity evidence standard: `tree-sitter/tree-sitter-rust` (prompt #48) and `alecthomas/participle` (prompt #63). The rest are explicitly excluded below. No source-code copying or product edits were made. The count does not include repositories mentioned in earlier V20 batches; none of the two qualified projects appeared in those prior ledgers.

## Qualified reviews

### Prompt #48 — tree-sitter/tree-sitter-rust

- Canonical upstream/activity: [tree-sitter/tree-sitter-rust](https://github.com/tree-sitter/tree-sitter-rust), 472 commits in the repository view; source/corpus/license files available. Tree-sitter's parser index recorded Rust grammar activity 2025-04-02 and ABI 15 (activity evidence is a dated upstream-maintained index, not a commit-level audit).
- Exact files inspected: [`grammar.js`](https://github.com/tree-sitter/tree-sitter-rust/blob/master/grammar.js), generated [`src/parser.c`](https://github.com/tree-sitter/tree-sitter-rust/blob/master/src/parser.c), [`test/corpus`](https://github.com/tree-sitter/tree-sitter-rust/tree/master/test/corpus), [`LICENSE`](https://github.com/tree-sitter/tree-sitter-rust/blob/master/LICENSE), [`package.json`](https://github.com/tree-sitter/tree-sitter-rust/blob/master/package.json), and the upstream Tree-sitter [corpus testing guide](https://github.com/tree-sitter/tree-sitter/blob/master/docs/src/creating-parsers/5-writing-tests.md).
- Architecture: declarative grammar generates a concrete syntax parser. Corpus tests pair fixture text with expected S-expressions; upstream describes incremental tree updates after edits. Grammar includes external scanner support for context-sensitive lexical cases. Tree-sitter explicitly documents grammar regexes as input to its own generated regex logic, not runtime JavaScript regex evaluation.
- Error/evolution behavior: error and missing nodes preserve partial parse structure; grammar tests encode both accepted source and expected tree, making grammar changes reviewable. ABI/generated parser freshness and corpus snapshot changes are compatibility concerns. This grammar is for Rust syntax, not logs; transfer is in tolerant partial structure and precise syntax-tree tests.
- NLI transfer: preserve useful substructure for malformed/unknown records rather than collapsing the full line to failure; represent parse errors as explicit nodes/spans; pair each grammar change with golden structured-output fixtures. Avoid treating generated grammar code as an untrusted dynamic extension.
- License/dependencies/security: repository marks MIT and has `LICENSE`; grammar build toolchain includes Tree-sitter generator and language bindings, as evidenced by package manifests/lockfiles. No vulnerability audit or transitive-license inventory performed. Confidence: high for architecture, tests, license; medium for current maintenance beyond observed index/repo activity.

### Prompt #63 — alecthomas/participle

- Canonical upstream/activity: [alecthomas/participle](https://github.com/alecthomas/participle), public source with 466 commits and recent GitHub activity at inspection; V2 is the current documented API.
- Exact files inspected: [`parser.go`](https://github.com/alecthomas/participle/blob/master/parser.go), [`parser_test.go`](https://github.com/alecthomas/participle/blob/master/parser_test.go), [`lexer/lexer.go`](https://github.com/alecthomas/participle/blob/master/lexer/lexer.go), [`lexer/lexer_test.go`](https://github.com/alecthomas/participle/blob/master/lexer/lexer_test.go), [`COPYING`](https://github.com/alecthomas/participle/blob/master/COPYING), [`go.mod`](https://github.com/alecthomas/participle/blob/master/go.mod), and [README](https://github.com/alecthomas/participle).
- Architecture: Go parser framework where struct tags express grammar and AST destination together; lexer is separate and can be stateful. Alternatives are tried in order with backtracking/lookahead. Supports custom parsing and EBNF rendering. The README reports benchmarks but these were not independently run here.
- Error/evolution/concurrency: parser errors expose useful input/location context; parser instance can be used concurrently, but lexer instance cannot. Recursive descent/backtracking does not support left recursion; lookahead setting can affect cost. Tests cover parser and lexer behavior. Grammar evolution can change the output AST and should be versioned/tested like a schema.
- NLI transfer: express deterministic log subformats as small typed grammars once framing is known; keep extraction separate from semantics; establish bounded lookahead/backtracking and per-record input budgets; return parse positions and partial diagnostic context.
- License/dependencies/security: repository states MIT; `COPYING` and `go.mod` are present. Go module dependencies were identified but transitive license/security review was not performed. Arbitrary grammar callbacks/custom parsers are trusted code, so must not be populated from generated candidates. Confidence: high for parser API/concurrency contract and license; medium for throughput absent independent benchmark.

## Excluded requested entries and substitutes

- #7 AutoLog: repository describes program-analysis-based log-sequence synthesis, but the source/test/license set was not fully inspected. Not counted.
- #8 LILAC: only a three-commit replication package was found; its README identifies implementation under `benchmark/logparser`, use of an OpenAI API key file, and adaptive cache. Exact implementation/test/license set was not verified. Not counted; do not treat prompt credentials or hosted inference as production-safe by default.
- #9 BlueLionLogram/Logram: repository accessible, describes n-gram dictionaries, but exact implementation/test/license set not completed. Not counted.
- #10 SalmaMessaoudi/MoLFI: implementation is also present in the already-inspected LogPAI/logparser toolkit; this is an overlap, not a distinct upstream for counting. The canonical URL was not independently source/test/license reviewed here.
- #53 winnow-rs/winnow: canonical repo and changelog available, but matching source, tests, license text, and activity details were not all inspected. Not counted.
- #61 peggyjs/peggy: source/documentation and package metadata indicate generated PEG parsers and MIT; however this pass did not inspect the full implementation, test source, and license file as a set. Not counted.
- #62 ohmjs/ohm: repository, docs, and MIT metadata were visible, and docs describe PEGs, grammar extension, modular semantics, and incremental matchers; exact implementation and test source were not verified in this pass. Not counted.
- #64 mna/pigeon: source `main.go` was inspected and documents PEG generation, optional memoization for pathological parse time, and panic-to-error recovery. Test/license source not fully inspected; not counted.

No additional substitute project was counted: the prompt's #48 and #63 were themselves available, distinct, and met the bar. Avoid padding the count with the same Drain algorithm/toolkit or mirrors.

## Confidence and unresolved work

The two qualified entries have direct links to implementation, test, license, and activity evidence. This is a targeted source-level review rather than a full security audit or execution of upstream test suites. Parser throughput, memory ceilings, malformed-input recovery properties, dependency transitive licenses, and exact latest commit dates were not independently measured for these projects.
