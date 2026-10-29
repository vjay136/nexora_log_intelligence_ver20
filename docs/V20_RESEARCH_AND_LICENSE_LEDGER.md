# Ver20 NUPIRE GitHub research ledger

## Consolidated status

Review date: 2026-10-23. The checked-in primary-source batches establish **100
distinct upstream repositories reviewed**. This exact count excludes mirrors,
redirect aliases, duplicate upstreams, repository landing pages, documentation-
only checks, unavailable files, and entries for which the required source-level
evidence was incomplete. This satisfies the master prompt's numeric research
gate. It does not approve code reuse: each batch retains its license,
dependency, maintenance, and security caveats, and no upstream source was
copied into NLI.

| Evidence file | New distinct reviews counted |
|---|---:|
| `research/V20_RESEARCH_LEDGER_AGENT.md` | 11 |
| `research/V20_RESEARCH_BATCH_1_20_COMPLETE.md` | 3 |
| `research/V20_RESEARCH_BATCH_12_40.md` | 17 |
| `research/V20_RESEARCH_BATCH_41_70.md` | 24 |
| `research/V20_RESEARCH_BATCH_71_110.md` | 8 |
| `research/V20_RESEARCH_BATCH_80_110_COMPLETE.md` | 5 |
| `research/V20_RESEARCH_BATCH_80_90_DEEP.md` | 2 |
| `research/V20_RESEARCH_BATCH_95_110_DEEP.md` | 5 |
| `research/V20_RESEARCH_GAP_C.md` | 2 |
| `research/V20_RESEARCH_GAP_D1.md` | 7 |
| `research/V20_RESEARCH_GAP_D2.md` | 1 |
| `research/V20_RESEARCH_GAP_D3.md` | 5 |
| `research/V20_RESEARCH_GAP_D4.md` | 2 |
| `research/V20_RESEARCH_GAP_D5.md` | 2 |
| `research/V20_RESEARCH_GAP_D6.md` | 1 |
| `research/V20_RESEARCH_GAP_D7.md` | 5 |
| **Total** | **100** |

`research/V20_RESEARCH_BATCH_1_13_DEEP.md` is retained as supplemental
evidence but contributes zero new rows because its qualified Drain3 review is
already R01 below. The detailed batch files record exact files inspected,
availability and activity limits, architecture observations, error/evolution
behavior, transferable concepts, license/security caveats, confidence, and
uncertainty. Research did not copy upstream source into NLI.

## Initial evidence set

Review date: 2026-10-21. Scope: the first source-level batch used to guide architecture. This initial section supports **11 distinct upstream repositories inspected**; the consolidated count above includes the later non-overlapping batches. Each row below names exact upstream files opened. Repository landing pages and search snippets alone do not count as source inspection.

## Method and evidence rules

1. Resolve canonical upstream from the requested repository URL. Record aliases/forks as duplicates, never as additional projects. For example, `IBM/Drain3` and `logpai/Drain3` describe the same project lineage and must be deduplicated unless repository history proves otherwise.
2. Inspect, at minimum, one implementation file and the repository's license evidence. For architecture-dependent claims, inspect the relevant configuration/tests/docs too. Cite stable upstream URLs at file level, preferably pinned to a commit in a future finalized pass. Current links below use upstream default branch paths and therefore may move.
3. Record activity from GitHub repository metadata/history only after checking it; this initial pass did not establish last commit/release dates, so activity is explicitly **not assessed**. Do not infer maintenance from stars, README badges, or search-engine crawl dates.
4. Separate direct observation (what inspected code does), inference (possible implication), and proposal (NLI design choice). Do not report benchmark figures unless the repository's reproducible benchmark and its inputs/method are inspected and reproduced.
5. Legal status is evidence tracking, not legal advice. Record exact license text/file and any per-file SPDX/header evidence. A repository license does not automatically cover bundled datasets, generated artifacts, submodules, or transitive dependencies. No code, parser packs, fixtures, or datasets are copied by this research.

## Ledger schema

Each record should contain: `ID; canonical URL/owner; status; duplicate/redirect disposition; activity evidence; exact implementation files inspected; exact tests/data/benchmark files inspected; purpose/architecture; observed parsing/data-flow behavior; unknown/malformed/multiline/nesting/drift behavior (only when examined); streaming/concurrency/bounds/recovery (only when examined); security surface; exact license evidence and dependency/dataset caveats; concepts to study; demonstrated limitation or explicitly unknown item; independent NLI implementation feasibility; reuse decision; confidence; remaining work`.

### Duplicate, unavailable, and invalid handling

- `DUPLICATE`: count the canonical upstream once; cross-reference its canonical ledger ID. In the supplied corpus, `IBM/Drain3` is presumed a candidate duplicate of `logpai/Drain3`; confirm repository redirect/history before final count.
- `UNAVAILABLE/RENAMED`: record the exact URL and checked result, then search for and inspect a canonical successor before assigning credit. A dead URL itself contributes zero inspected repos.
- `LICENSE UNRESOLVED`: do not infer a license from language, repository purpose, licensee, or a web snippet. Block dependency/code reuse pending direct license evidence.
- `NOT REVIEWED`: preserve candidate inventory separately from inspected ledger. Never inflate inspected count with candidate URLs.

## Source-level records

### R01 — Drain3
- Canonical candidate: https://github.com/logpai/Drain3 (owner `logpai`; the prompt also lists `IBM/Drain3`, which is treated as a probable duplicate pending redirect/history confirmation).
- Status: repository reachable; last relevant activity not assessed.
- Inspected: [`drain3/drain.py`](https://raw.githubusercontent.com/logpai/Drain3/master/drain3/drain.py), especially lines 0–2 and 56–80.
- Observation: file identifies itself as a Drain algorithm implementation; maintains a prefix tree (`Node`) and log clusters, configures depth/similarity/max-children and optional max-cluster count; comments specify LRU replacement when a configured cluster cap is reached. This supports bounded cluster state only when `max_clusters` is configured.
- Unknown/multiline/nesting/schema drift: not established by this file review. Template mining is not semantic field typing; that is an architectural inference.
- Performance/security: profiler hooks and LRU cache are present, but no benchmark was inspected. Input tokenization/regex safety was not assessed.
- License: `LICENSE` at both tested raw paths (`master/LICENSE`, `main/LICENSE`) returned 404 from the direct fetch; therefore license is **unresolved here**. The implementation file contains no SPDX header, but does attribute algorithm lineage to logpai/logparser. Do not reuse code/depend until exact license is confirmed.
- Concept: bounded online template mining as an isolated discovery signal, not a production parser promotion criterion.
- Feasibility/reuse: independently implement the idea; direct code reuse blocked pending license and dependency audit. Confidence: medium on observed mechanics; low on suitability/legal status.

### R02 — LogPAI logparser
- Canonical URL/owner: https://github.com/logpai/logparser (`logpai`). Reachable; activity not assessed.
- Inspected: [`logparser/Drain/Drain.py`](https://raw.githubusercontent.com/logpai/logparser/master/logparser/Drain/Drain.py), lines 0–14, 23–69, 74–167.
- Observation: offline/file-oriented `LogParser` accepts a declared `log_format`, preprocessing regexes, tree depth, similarity threshold, child cap; Drain tree routes by token count and token path, with wildcard branches and numeric-token generalization. The source's line header explicitly states Apache-2.0 for this file. Its `Drain.py` requires the third-party Python `regex` package and pandas; their exact selected versions/licenses were not inspected.
- Unknown/malformed/multiline/drift: clustering can form wildcard templates for variable tokens. No evidence in inspected range that it discovers typed field semantics, handles multiline framing, or evaluates parser candidates on held-out evidence. These remain unverified, not asserted absent repository-wide.
- Benchmark/data, recovery, security: not inspected. Regex behavior and input controls need separate review.
- License: implementation header states Apache License 2.0; top-level `LICENSE` direct raw fetch returned 404, so repository-wide licensing and bundled data status remain unresolved. Source header is evidence for this file, not all assets.
- Concept: algorithmic comparator and testable baseline for template-mining accuracy.
- Feasibility/reuse: independently implement comparable bounded tree approach; code reuse requires confirming project-wide license and dependency/data provenance. Confidence: medium.

### R03 — Vector
- Canonical URL/owner: https://github.com/vectordotdev/vector (`vectordotdev`). Reachable; activity not assessed.
- Inspected: [`src/topology/builder.rs`](https://raw.githubusercontent.com/vectordotdev/vector/master/src/topology/builder.rs), lines 0–80; [`LICENSE`](https://raw.githubusercontent.com/vectordotdev/vector/master/LICENSE), lines 0–4 and 85–96.
- Observation: topology builder imports bounded-channel/buffer abstractions, async stream/future concurrency, timeouts, utilization and telemetry; defines source sender buffer sizing and ready-array capacity. The license file is Mozilla Public License 2.0.
- Parsing-specific behavior: this file establishes pipeline topology/runtime concepts, not a parser implementation; no unknown format, multiline, schema drift, or parser recovery claims are made.
- Dependencies: the root license alone does not clear all dependencies; no dependency inventory reviewed.
- Concept: bounded event flow, per-component observability, and pipeline topology as a pattern for keeping discovery separate from hot path.
- Feasibility/reuse: architectural concepts are independently implementable. No code reuse recommended without file-level notices and MPL obligations reviewed. Confidence: high for narrow observations.

### R04 — Fluent Bit
- Canonical URL/owner: https://github.com/fluent/fluent-bit (`fluent`). Reachable; activity not assessed.
- Inspected: [`src/flb_filter.c`](https://raw.githubusercontent.com/fluent/fluent-bit/master/src/flb_filter.c), lines 0–16 and 31–68; [`LICENSE`](https://raw.githubusercontent.com/fluent/fluent-bit/master/LICENSE), lines 0–4.
- Observation: filter core exposes configurable tag and regex matching, aliasing, log level, and repeated-message suppression controls. File header and root license state Apache-2.0.
- Parsing-specific behavior: filter dispatcher/config evidence only; no claims about individual parser plugins, multiline, buffering durability, or malformed record policy. Those require inspecting their specific plugin files and tests.
- Security: configurable regex matching is an input/configuration surface worth reviewing in the actual plugin and regex engine; this file alone does not demonstrate a vulnerability.
- Concept: keep filtering/routing stage config explicit and observable. Feasible to implement independently.
- Reuse: no source copied; project license appears permissive for the inspected file, but dependency and component-level provenance review remains required. Confidence: medium-high.

### R05 — OpenTelemetry Collector
- Canonical URL/owner: https://github.com/open-telemetry/opentelemetry-collector (`open-telemetry`). Reachable; activity not assessed.
- Inspected: [`processor/batchprocessor/batch_processor.go`](https://raw.githubusercontent.com/open-telemetry/opentelemetry-collector/main/processor/batchprocessor/batch_processor.go), lines 0–103; [`LICENSE`](https://raw.githubusercontent.com/open-telemetry/opentelemetry-collector/main/LICENSE), lines 0–4.
- Observation: batch processor uses configured size/time flush triggers, optional metadata-key sharding, goroutine lifecycle tracking, and a metadata cardinality limit that returns a permanent error. The root license is Apache-2.0; source has SPDX Apache-2.0.
- Parsing-specific behavior: batching/processor architecture only; this does not establish parsing semantics, multiline handling, or replay durability.
- Concept: enforce explicit cardinality limits and report overflow as a visible error; batch by size/time with shutdown-aware workers.
- Feasibility/reuse: straightforward to implement the policy concept independently. Reuse requires dependency and distribution review. Confidence: high on narrow observations.

### R06 — Grafana Loki
- Canonical URL/owner: https://github.com/grafana/loki (`grafana`). Reachable; activity not assessed.
- Inspected: [`pkg/logql/log/parser.go`](https://raw.githubusercontent.com/grafana/loki/main/pkg/logql/log/parser.go), lines 0–55, 57–123 and 134–186; [`LICENSE`](https://raw.githubusercontent.com/grafana/loki/main/LICENSE), lines 0–10.
- Observation: parser stages include JSON, regexp, and logfmt. JSON parser recursively walks objects, supports extraction hints/short-circuiting, labels malformed JSON through an error annotation, and appends `_extracted` to duplicate label names. Root license is GNU AGPL-3.0.
- Parsing caveat: its output model is label extraction for query stages; not equivalent to lossless universal event normalization. That distinction is an inference from inspected code and declared API purpose.
- Security/dependencies: imports `regexp`, `jsonparser`, `json-iterator`, etc.; their versions/licenses and regex resource behavior were not audited.
- Concept: explicit duplicate-key collision policy, nested key path handling, parser error annotation, and selective extraction.
- Feasibility/reuse: concepts can be independently implemented. Do not copy or link this code into a proprietary NLI distribution without specific legal review of AGPL-3.0 obligations. Confidence: high on source/license observations.

## Preliminary cross-project implications (observations separated from recommendations)

Observed across the inspected files: template mining (Drain/Drain3), configurable pipeline stages and bounded event topology (Vector/OTel), and explicit nested JSON extraction/error/duplicate handling (Loki) are separate concerns. No inspected repository here proves the universal-parsing objective or an evidence-safe automatic promotion workflow.

NLI proposal: treat deterministic parsing, template discovery, and candidate evaluation as separate stages. Keep source bytes/raw record immutable, enforce limits, surface malformed/ambiguous outcomes, and require held-out evidence plus approval before registry promotion. These are design recommendations, not claims that the upstream projects lack all such features.

## Remaining work / count guard

This is an initial 11-project evidence set, far short of the prompt's 100 distinct repo gate. Continue through the corpus with the schema above; verify redirects/forks, activity, source/tests/benchmarks, exact license plus dataset/dependency provenance. For each uninspected candidate use `NOT REVIEWED`, and do not represent the candidate list as completed research. No v18 files were modified by this task.

## Continued source-level records

### R07 — Elastic go-grok
- Canonical URL/owner: https://github.com/elastic/go-grok (`elastic`); distinct from the upstream Logstash project and not counted as a Logstash fork. Reachable; activity not assessed.
- Inspected: [`grok.go`](https://raw.githubusercontent.com/elastic/go-grok/main/grok.go), lines 0–40, 42–63, 64–123, 125–132.
- Observation: Grok syntax expands named patterns into Go regex captures; capture metadata supports string/float/int/bool conversion, built-in pattern families are registered in `NewComplete`, and duplicate pattern names overwrite existing definitions. Parse failure and unsupported type/name conditions have explicit errors.
- License: source header directly licenses this file under Apache-2.0 and points to distributed NOTICE. Root LICENSE direct fetch failed (cache miss), so repository-wide status/NOTICE/dependencies are unresolved; do not treat the file header as proof for bundled pattern definitions.
- Failure/security: parser is regex based; inspected region does not show regex execution bounds or timeout. Do not infer exploitability.
- Transfer: compile a restricted, typed capture specification with explicit errors; keep pattern pack provenance separate. Independent implementation feasible. Confidence: medium-high for source mechanics, medium for file license, low for whole-repo legal inventory.

### R08 — mapstructure
- Canonical URL/owner: https://github.com/mitchellh/mapstructure (`mitchellh`); reachable; activity not assessed.
- Inspected: [`mapstructure.go`](https://raw.githubusercontent.com/mitchellh/mapstructure/main/mapstructure.go), lines 0–109.
- Observation: Go map-to-struct decoder supports nested structs/tags/squashing; by default unused keys are ignored, while DecoderConfig can error and metadata or `remain` can capture them. This is schema projection, not text log parsing.
- Failure/drift: the documented default can silently drop unknown fields; an explicit strict/capture-remaining mode exists. No runtime tests or resource bounds were inspected.
- License: root LICENSE direct raw fetch returned cache miss, and the inspected source excerpt had no license header. **Unresolved**; no code reuse pending direct license verification.
- Transfer: unknown-field preservation must be a deliberate policy, with strict and remainder-capture modes. Independent implementation feasible. Confidence: high for documented behavior; low for legal status.

### R09 — tidwall/gjson
- Canonical URL/owner: https://github.com/tidwall/gjson (`tidwall`); reachable; activity not assessed.
- Inspected: [`gjson.go`](https://raw.githubusercontent.com/tidwall/gjson/master/gjson.go), lines 0–24, 26–75, 107–150; [`LICENSE`](https://raw.githubusercontent.com/tidwall/gjson/master/LICENSE), lines 0–17.
- Observation: query-oriented JSON access returns typed result categories and preserves raw JSON and source index where available; integer conversion uses guarded paths and fallback conversion. Source identifies MIT license and the exact LICENSE states MIT.
- Failure/dependencies: excerpt does not establish all malformed-input behavior or resource ceilings. Imports `tidwall/match` and `tidwall/pretty`; their versions/licenses were not audited.
- Transfer: keep raw slices/offsets and explicit typed values in a decoder, and avoid unnecessary whole-tree materialization where an extraction query suffices. Concept independently implementable; direct dependency requires transitive/license and benchmark review. Confidence: high for narrow source/license evidence.

### R10 — buger/jsonparser
- Canonical URL/owner: https://github.com/buger/jsonparser (`buger`); reachable; activity not assessed.
- Inspected: [`parser.go`](https://raw.githubusercontent.com/buger/jsonparser/master/parser.go), lines 0–25, 51–85; [`LICENSE`](https://raw.githubusercontent.com/buger/jsonparser/master/LICENSE), lines 0–18.
- Observation: parser exports distinct errors for malformed JSON/string/array/object/value, integer overflow, invalid escape, and null; source comments and implementation include token boundary checks. Root license is MIT.
- Failure/recovery: callers can distinguish malformed shape and numeric overflow rather than receive one generic decode failure. Exact recovery strategy and full parser behavior require further tests/functions review; dependencies were not audited.
- Transfer: preserve structured parse error categories in NUPIRE outcomes. Independent implementation straightforward; dependency use subject to versions, fuzz history, and transitive review. Confidence: high for listed errors and license evidence.

### R11 — rs/zerolog
- Canonical URL/owner: https://github.com/rs/zerolog (`rs`); reachable; activity not assessed.
- Inspected: [`event.go`](https://raw.githubusercontent.com/rs/zerolog/master/event.go), lines 0–86, 89–131; [`LICENSE`](https://raw.githubusercontent.com/rs/zerolog/master/LICENSE), lines 0–1.
- Observation: structured logging event buffers are pooled; fields are reset before return, and buffers above 64 KiB are not returned to the pool. Write errors are returned from the writer path. API comments warn that event instances should be disposed after Msg/Send.
- Architecture/failure: this is a producer library rather than an ingestion parser; event finalization is explicit. The cap limits retention of oversized backing arrays, not event size or end-to-end memory.
- License: exact root file states MIT. Dependencies/transitive notices not audited.
- Transfer: bounded pool retention and explicit lifecycle cleanup can reduce memory retention in high-volume paths. Independent concept useful; dependency adoption merits profiling and provenance review. Confidence: high for observations.

## Updated count and limitations

Count now supported by exact implementation source review: **11 distinct upstream repositories** (R01–R11). Five added records cover Go Grok extraction, Go map projection, JSON querying and decoding, and structured logging buffer lifecycle. Several entries have unresolved repository-wide license evidence and are still counted as source reviews, but must not be treated as approved dependencies. Activity, dependency trees, benchmark replication, and test-suite review remain open for every added entry. The 100-repository prompt gate remains unmet; no other candidate URL is counted.
