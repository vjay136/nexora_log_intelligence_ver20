# Ver20 research batch 41–70: parsers and grammar systems

## Scope and count

**24 distinct canonical upstream projects were genuinely reviewed** in this slice, and the batch is partial: it does not claim all 30 prompt candidates were inspected. Counted projects: ANTLR; Tree-sitter runtime; tree-sitter JSON, YAML, TOML, Bash, Python, Go, JavaScript and TypeScript grammars; nom; chumsky; pest; Lark; pyparsing; Parsimonious; Chevrotain; nearley; Participle; Bison; lexy; cpp-peglib; Parsimmon; and Instaparse. Activity metadata was not established consistently; raw-file crawl timestamps are not commit dates and are not reported as activity evidence. No throughput claims are made without benchmark reproduction.

Prompt #66 (`antlr/antlr4-runtime`) duplicates the runtime component of #41 (`antlr/antlr4`) and receives no additional credit. Prompt #43 used `tree-sitter-grammars/tree-sitter-json`, which returned 404 for `grammar.js`; the canonical successor `tree-sitter/tree-sitter-json` was inspected and credited once. Repos for which only a shallow peek was possible are listed as uncounted.

## Evidence method

Each counted entry has a canonical upstream URL, exact relevant file paths, direct implementation or grammar evidence, and license evidence (root license text, source SPDX/header, or package-scoped metadata). Claims are limited to checked files; security, activity, benchmark, dependency, and integration characteristics not seen directly are called out as unknown. No source code, grammar, test corpus or dataset is copied. Legal status is evidence tracking, not legal advice.

## Findings

### 41. antlr/antlr4 — BSD 3-Clause

- Upstream: https://github.com/antlr/antlr4 (owner `antlr`). Activity: not assessed; no commit/release metadata established.
- Inspected: `runtime/Java/src/org/antlr/v4/runtime/Parser.java`, `runtime/Java/src/org/antlr/v4/runtime/DefaultErrorStrategy.java`, `LICENSE.txt`; no tests inspected.
- Parser delegates to configurable `ANTLRErrorStrategy`, defaulting to `DefaultErrorStrategy`. Recovery mode suppresses duplicate reports until a successful match. The strategy tracks the last error input index as a fail-safe to ensure token/tree progress and avoid infinite recovery loops; expected-token sets can be computed using the original parser context.
- NLI concept: explicitly distinguish clean parse from recovery, retain structured diagnostics, and enforce a progress guarantee for every retry. Resource limits were not established in inspected files.
- License evidence: root `LICENSE.txt` gives BSD 3-Clause conditions/disclaimer. Confidence high for recovery behavior and license; low for uninspected runtime behavior.

### 42. tree-sitter/tree-sitter — MIT runtime

- Upstream: https://github.com/tree-sitter/tree-sitter. Activity: not assessed.
- Inspected: `lib/src/parser.c`, root `LICENSE`; no tests/docs inspected.
- C incremental parsing runtime; parser source includes lexer, stack, subtree and changed-range components. This pass did not trace recovery or ambiguity branches, so no claims about their detailed behavior are made.
- NLI concept: investigate incremental parse state and edit-aware parsing for multiline updates. Source includes bounded serialization/debug-buffer mechanisms, but whole-input limits were not established.
- License: root `LICENSE` is MIT. Confidence medium-low.

### 43. tree-sitter/tree-sitter-json — MIT (canonical resolution)

- Prompt URL `tree-sitter-grammars/tree-sitter-json` returned 404 for `grammar.js`; inspected canonical project: https://github.com/tree-sitter/tree-sitter-json. Activity not assessed.
- Inspected: `grammar.js`, `README.md`, `LICENSE`; corpus fixture fetches unavailable.
- Grammar has recursive objects/arrays, labeled key/value fields, explicit numeric/string productions and comment tokens. It permits comments and repeated top-level values, so this grammar alone should not be treated as strict standalone JSON validation.
- NLI concept: separate grammar acceptance from strict decoding/schema validation; preserve duplicate-key and raw-source evidence. No runtime bounds were inspected.
- License: root `LICENSE` is MIT. Confidence medium.

### 44. tree-sitter-grammars/tree-sitter-yaml — MIT

- Upstream: https://github.com/tree-sitter-grammars/tree-sitter-yaml. Activity not assessed.
- Inspected: `grammar.js`, `src/scanner.c`, `README.md`, `LICENSE`; corpus fixture unavailable.
- External scanner models indentation, block/flow collections, document markers, tags, aliases, quoted and plain multiline scalars. It tracks indentation stacks and serializes scanner state; guards include rejection of tab indentation in indentation setup and prevention of popping root indentation during error recovery. Grammar declares conflicts and an error-recovery external token.
- NLI concept: indentation and multiline continuation require explicit state across chunks; preserve recovery status and never instantiate arbitrary YAML tags or expand aliases in log ingestion. Size/depth/alias-expansion limits were not inspected.
- License: root license body contains MIT grant and disclaimer; grammar header also declares MIT. Confidence high for scanner structure, low for semantic security (not examined).

### 45. tree-sitter-grammars/tree-sitter-toml — MIT

- Upstream: https://github.com/tree-sitter-grammars/tree-sitter-toml. Activity not assessed.
- Inspected: `grammar.js`, `src/grammar.json`, `LICENSE`; test fixture fetch unavailable.
- Grammar defines key/value pairs, tables/table-arrays, typed scalar productions, date/time forms, and explicit external tokens for line-ending/EOF plus multiline basic/literal string contents/endings.
- NLI concept: preserve syntax-level value types and raw spans, then perform semantic constraints separately. Duplicate-key behavior and parser recovery were not inspected; no bounds established.
- License: root `LICENSE` is MIT. Confidence medium-high.

### 46. tree-sitter/tree-sitter-bash — MIT

- Upstream: https://github.com/tree-sitter/tree-sitter-bash. Activity not assessed.
- Inspected: `grammar.js`, `src/scanner.c`, `test/corpus/commands.txt`, `LICENSE`.
- Grammar declares conflicts around expressions, command names, assignments, redirects and substitutions. Scanner tracks heredoc delimiters/raw/started/indent properties and serializes them subject to Tree-sitter serialization buffer bounds. Corpus pairs shell examples with expected trees for assignments, redirects, quoting and line continuations. Scanner comments document approximate word interpretation and omitted POSIX substitution behavior.
- NLI concept: multiline record framing may need delimiter state; syntax parsing must remain inert and never execute parsed content. No full hostile-input/resource audit was done.
- License: root `LICENSE` MIT. Confidence high for inspected behavior.

### 47. tree-sitter/tree-sitter-python — MIT

- Upstream: https://github.com/tree-sitter/tree-sitter-python. Activity not assessed.
- Inspected: `grammar.js`, `src/scanner.c`, `test/corpus/expressions.txt`, `LICENSE`.
- Scanner tracks indentation, delimiters, bracket context, quote style, raw/format/triple/bytes flags and interpolation. Grammar lists syntactic conflicts. Corpus fixture checks Unicode identifiers, slices, calls and async/await tree shapes.
- NLI concept: keep indentation, nesting and quoted multiline content in parser state; assert expected syntax trees with fixtures. No whole-input limits inspected.
- License: root `LICENSE` MIT. Confidence high.

### 48. tree-sitter/tree-sitter-rust — not reviewed

- No implementation or license file inspected; zero credit.

### 49. tree-sitter/tree-sitter-go — MIT

- Upstream: https://github.com/tree-sitter/tree-sitter-go. Activity not assessed.
- Inspected: `grammar.js`, `LICENSE`; tests not inspected.
- Grammar explicitly accepts statements at top level to support partial documentation snippets, and declares conflicts between expressions/types/parameters. This makes partial syntax acceptance distinct from whole-program validity.
- NLI concept: distinguish a recognized fragment from a complete record and preserve parse completeness metadata. No recovery/resource behavior inspected.
- License: root `LICENSE` MIT. Confidence medium.

### 50. tree-sitter/tree-sitter-javascript — MIT

- Upstream: https://github.com/tree-sitter/tree-sitter-javascript. Activity not assessed.
- Inspected: `grammar.js`, `test/corpus/expressions.txt`, `LICENSE`.
- External tokens represent automatic semicolon insertion, template characters, regex patterns and JSX. Corpus demonstrates multiline template strings, nested interpolations and escapes with expected syntax trees.
- NLI concept: quoted/template regions can span physical lines and require lexical mode state; do not split records on newline blindly. Regex/resource bounds were not inspected.
- License: root `LICENSE` MIT. Confidence high.

### 51. tree-sitter/tree-sitter-typescript — MIT

- Upstream: https://github.com/tree-sitter/tree-sitter-typescript. Activity not assessed.
- Inspected: `typescript/grammar.js`, `common/define-grammar.js`, `test/corpus/types.txt`, `LICENSE`.
- TypeScript grammar extends JavaScript with explicit precedence/conflict tables for call/type instantiation, identifiers vs type identifiers, and expression/pattern/type ambiguities; it introduces an error-recovery external token. Corpus has expected trees for built-in/object/mapped types.
- NLI concept: make ambiguity explicit and regression-test it; do not silently coerce uncertain field interpretations. Resource bounds not inspected.
- License: root `LICENSE` MIT. Confidence high.

### 52. rust-bakery/nom — MIT

- Upstream: https://github.com/rust-bakery/nom. Activity not assessed.
- Inspected: `src/lib.rs`, `src/internal.rs`, `Cargo.toml`. Manifest lists JSON, multiline, overflow and custom-error tests; bodies not inspected.
- Byte-oriented, zero-copy combinators return remaining input with output. Results distinguish ordinary error, fatal failure, and `Incomplete(Needed::Unknown/Size)`. Docs warn `finish()` panics on `Incomplete`; streaming callers must gather more input and retry.
- NLI concept: model `NeedMoreData`, `Invalid` and `Complete` separately; retain unconsumed bytes for framing. Bounded combinators depend on caller grammar; no benchmark reproduced.
- License: package manifest states MIT and includes LICENSE in package; file text not independently reviewed. Confidence high for streaming contract, medium for license.

### 53. winnow-rs/winnow — partial, not counted

- Inspected Cargo manifest and `LICENSE-MIT`, but implementation source fetch failed. Do not count.

### 54. zesterer/chumsky — MIT

- Upstream: https://github.com/zesterer/chumsky. Activity not assessed.
- Inspected: `src/lib.rs`, `src/recovery.rs`, `src/error.rs`, `Cargo.toml`; tests not inspected.
- Recovery documentation states strategies are heuristic and their placement/order affect recovered AST and diagnostics. `skip_then_retry_until` is a configurable synchronization/retry strategy. Error types include rich/cheap/empty alternatives; feature flags include memoization and optional stack growth. Crate can be `no_std`.
- NLI concept: choose recovery policy by parser/record context and never treat recovered output as clean success; permit cheaper errors on hot path with richer sampled diagnostics. No hard quota established.
- License: Cargo manifest states MIT. Optional dependency licenses not checked. Confidence high for recovery model.

### 55. pest-parser/pest — MIT OR Apache-2.0

- Upstream: https://github.com/pest-parser/pest. Activity not assessed.
- Inspected: `pest/src/parser.rs`, `pest/Cargo.toml`; tests not inspected.
- Parser trait takes a rule and `&str`, returns pairs or typed error. Manifest shows optional stacker/psm stack-growth dependencies, optional serde output and Criterion stack/Pratt benchmarks (benchmark inputs not reviewed).
- NLI concept: separate syntax pairs from domain decoding; test deeply nested input and account for stack growth/resource behavior. No limits/benchmark results assessed.
- License: source header and package manifest state MIT OR Apache-2.0. Dependency licenses not reviewed. Confidence medium.

### 56. lark-parser/lark — MIT

- Upstream: https://github.com/lark-parser/lark. Activity not assessed.
- Inspected: `lark/lark.py`, `LICENSE`; tests not inspected.
- Configuration exposes Earley/LALR, strict ambiguity checks, and `auto`/`resolve`/`explicit`/`forest` ambiguity policies. Optional third-party `regex` backend is present. Grammar caching/serialization options also exist.
- NLI concept: represent ambiguity as an explicit result or policy; include grammar identity/version in cache keys. Third-party regex and resource behavior were not audited.
- License: root license text MIT. Confidence medium-high.

### 57. pyparsing/pyparsing — MIT

- Upstream: https://github.com/pyparsing/pyparsing. Activity not assessed.
- Inspected: `pyparsing/core.py`, `LICENSE`; tests not inspected.
- Parser-combinator core imports regex support, result/exception machinery, reentrant lock and bounded/unbounded memo/cache implementations. Method-level failure/recovery semantics were not traced.
- NLI concept: test caching and regex cost under malformed inputs; use structured diagnostic types. No parser resource limits reviewed.
- License: root `LICENSE` MIT. Confidence medium-low.

### 58. erikrose/parsimonious — MIT

- Upstream: https://github.com/erikrose/parsimonious. Activity not assessed.
- Inspected: `parsimonious/grammar.py`, `parsimonious/expressions.py`, `parsimonious/exceptions.py`, `parsimonious/tests/test_grammar.py`, `LICENSE`.
- PEG expression tree with cached matching/packrat behavior; custom expressions are callable hooks. `IncompleteParseError` separates prefix match from full consumption; `LeftRecursionError` explains unsupported left recursion. Tests exercise grammar syntax, precedence, lookahead and parse trees.
- NLI concept: full consumption should be explicit; preserve incomplete parse state and reject untrusted custom callbacks. Memoization can consume input-dependent memory; quotas not inspected.
- License: root `LICENSE` MIT. Confidence high.

### 59. Chevrotain/chevrotain — package Apache-2.0

- Upstream: https://github.com/Chevrotain/chevrotain. Activity not assessed.
- Inspected: `packages/chevrotain/src/parse/parser/parser.ts`, `packages/chevrotain/package.json`; test bodies and root license body not inspected.
- Parser composes grammar validation, lookahead, recovery, error handling and CST construction. Defaults disable recovery, use max lookahead 3, enable CST, and enable rule resync. Definition errors identify unresolved references, left recursion, ambiguous alternatives/prefixes.
- NLI concept: lint/validate parser definitions before runtime; make recovery opt-in and expose ambiguity diagnostics. Defaults are not resource guarantees.
- License: package manifest declares Apache-2.0 for this package; workspace dependencies unreviewed. Confidence high at package scope.

### 60. kach/nearley — MIT

- Upstream: https://github.com/kach/nearley. Activity not assessed.
- Inspected: `lib/nearley.js`, `LICENSE.txt`; tests not inspected.
- Earley chart columns hold state, wanted, scannable and completed sets; states track rule/dot/origin/waiters and progress through predict, scan and complete. Postprocess may reject a candidate with `Parser.fail`; ambiguous derivations are representable.
- NLI concept: preserve parse alternatives/ambiguity instead of silently selecting a value. Chart growth for ambiguous grammars and arbitrary postprocess callbacks are resource/trust concerns; not bounded here.
- License: root `LICENSE.txt` MIT. Confidence medium-high.

### 61. peggyjs/peggy — not reviewed

- No implementation or license inspected; zero credit.

### 62. ohmjs/ohm — not reviewed

- No implementation or license inspected; zero credit.

### 63. alecthomas/participle — license unresolved

- Upstream: https://github.com/alecthomas/participle. Activity not assessed.
- Inspected: `parser.go`, `README.md`, `go.mod`. Root LICENSE fetch returned 404; no exact license evidence found.
- Go generics and struct-tag grammar map tokens into typed structs; parser build configures lexer, lookahead, mappers, unions and custom parse functions. README describes stateful lexers, error reporting and concurrency, but those behaviors were not verified in code.
- NLI concept: compile/validate parser schema once, retain token spans, and constrain custom hooks to trusted code. No bounds/recovery semantics assessed.
- License status unresolved; do not reuse/add dependency pending direct license evidence. Confidence medium for architecture, none for legal status.

### 64. mna/pigeon — partial peek, not counted

- `pigeon.go` was inspected and is generated bootstrap parser code (`Code generated by pigeon; DO NOT EDIT`); `go.mod` also inspected. Runtime/generator implementation and license could not be inspected. Zero credit.

### 65. akimd/bison — GPL-3.0-or-later

- Upstream: https://github.com/akimd/bison. Activity not assessed.
- Inspected: `src/parse-gram.y`, root `COPYING`. The inspected grammar is for Bison's own grammar parser, not all generated skeletons.
- Grammar explicitly enables pure API, locations, custom errors, full lookahead correction, tracing and zero expected conflicts. Useful model for explicit diagnostics and conflict gates; no runtime resource behavior inspected.
- NLI concept: fail parser compilation on unresolved grammar conflicts and keep precise source locations. GPL code is not proposed for reuse.
- License: grammar header states GPL v3 or later; root COPYING is GPL v3. Confidence high.

### 66. antlr/antlr4-runtime — duplicate

- Same upstream/runtime tree as #41; no additional count.

### 67. foonathan/lexy — source SPDX BSL-1.0; behavior review partial

- Upstream: https://github.com/foonathan/lexy. GitHub landing page reports 1,232 commits and BSL-1.0; latest commit date not available in retrieved view.
- Inspected: `include/lexy/dsl.hpp`, which has exact `SPDX-License-Identifier: BSL-1.0` and exports recovery, lookahead, delimited, UTF-8/unicode and scanner DSL headers. Implementation of recovery and tests could not be fetched.
- Only the DSL surface was reviewed; no recovery semantics claimed. Candidate concept: typed delimiters and explicit recovery rules, pending deeper source validation.
- License: per-file SPDX is exact; license body not inspected. Confidence low-medium for behavior, high for notice.

### 68. yhirose/cpp-peglib — MIT

- Upstream: https://github.com/yhirose/cpp-peglib. Activity not assessed.
- Inspected: `peglib.h`, `LICENSE`; tests not inspected.
- Single-header PEG implementation identifies C++17 requirement and version; UTF-8 decoder rejects invalid starts, truncated/overlong forms, surrogates, and out-of-range code points. Error-token heuristic default cap is 32 characters. Full ambiguity/recovery paths not traced.
- NLI concept: validate byte sequences and cap diagnostic excerpts while retaining original raw bytes. No complete hostile-input/resource audit.
- License: header and root license state MIT. Confidence medium-high.

### 69. jneen/parsimmon — MIT (manifest evidence)

- Upstream: https://github.com/jneen/parsimmon. Activity not assessed.
- Inspected: `src/parsimmon.js`, `package.json`; root LICENSE fetch unavailable, tests not inspected.
- Result tracks status, furthest failure offset and expected tokens; alternative replies merge expectations at furthest point; `.parse()` enforces EOF and returns line/column diagnostics. `.many()` throws on zero-width success to prevent an infinite loop.
- NLI concept: preserve furthest-offset expected-token sets and enforce complete consumption/progress. No input size quotas reviewed.
- License: package manifest declares MIT; root license text not independently retrieved. Confidence high for behavior, medium for license.

### 70. Engelberg/instaparse — EPL evidence provisional

- Upstream: https://github.com/Engelberg/instaparse. GitHub page showed 829 commits and a LICENSE file, but commit recency was unavailable.
- Inspected: `src/instaparse/core.cljc`, `README.md`. LICENSE body fetch unavailable; README names EPL (same as Clojure).
- README describes GLL parsing for EBNF/ABNF, left/right recursion and ambiguity, lazy all-parses enumeration, detailed failures, partial parsing, total mode embedding leftover input, and optional memory optimization. Core API distinguishes complete parse, partial parse and total parse.
- NLI concept: preserve leftover input and distinguish partial from complete parse; ambiguity enumeration/output can grow and should be constrained. Do not load remote grammars selected by log data.
- License is provisional from README only; resolve exact LICENSE before reuse. Confidence medium for architecture, low for legal status.

## Unreviewed/partial candidates and batch limits

- #48 Rust grammar, #53 Winnow implementation, #61 Peggy, #62 Ohm, and #64 Pigeon runtime were not counted. #43 invalid supplied path was resolved to canonical upstream and credited once. #66 is duplicate of #41.
- #63 Participle has unresolved license evidence; #70 Instaparse has README-level license identification but its license body was unavailable. Neither is approved for dependency/code reuse by this ledger.
- This is a 24-project partial batch and does not satisfy the prompt's separate 100-repository research target. No complete history, dependency-license, benchmark, or security audit is claimed.

## NLI design implications

The directly inspected parser systems support studying explicit `NeedMoreData` / `Invalid` / `Complete` statuses, progress guarantees, source offsets, strict whole-input validation, ambiguity reporting, stateful multiline/indentation scanning, and parser-version provenance. Recovery must remain visibly distinct from clean output. Hard limits on bytes, nesting, cache growth and diagnostic excerpts still need independent NLI design and tests. Grammar parsing is not semantic safety: YAML tags/aliases, regex engines, callbacks, remote grammar loading, and executable-language syntax require separate containment.

