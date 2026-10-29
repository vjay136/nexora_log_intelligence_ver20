# V20 research batch 71–110 (source-level sample)

## Scope and count

Prompt positions 71–100 were the assigned range. **8 distinct canonical upstreams were genuinely inspected at source/docs level**: jqlang/jq, simdjson/simdjson, fastfloat/fast_float, Tencent/rapidjson, nlohmann/json, yaml/pyyaml, toml-lang/toml (specification repository), and BurntSushi/toml. This is a partial batch, not a claim that all 30 positions were reviewed. Positions 78 (TOML spec) and 79 are separate projects and both counted. No unavailable URL was counted. No 101–110 candidates were needed for this batch.

## Findings

### 71. jqlang/jq — partial source inspection

- Primary files: [`src/jv.h`](https://github.com/jqlang/jq/blob/master/src/jv.h) (stream/parser API); [`src/jv_parse.c`](https://github.com/jqlang/jq/blob/master/src/jv_parse.c) (parser implementation); [`src/jq_test.c`](https://github.com/jqlang/jq/blob/master/src/jq_test.c) (test harness); [`COPYING`](https://github.com/jqlang/jq/blob/master/COPYING) (license).
- Format/architecture: JSON parser exposes flags for sequence and streaming modes and a `jv_parser` incremental API (`set_buf`, `remaining`, `next`). These are concrete patterns for incremental framing and explicit parser state.
- Malformed/evolution: parser returns `jv` values and has a stream-errors flag; parse-error formatting is a relevant boundary. A current upstream advisory specifically describes an out-of-bounds read in the `jv_parse_sized` error path when a counted buffer is treated as NUL-terminated. This is a caution to preserve buffer length through diagnostics, not merely the successful parse path.
- Transfer to NLI: length-aware framing, explicit sequence mode, and malformed-input handling should be tested independently from valid parsing; diagnostics must not read past a supplied byte slice.
- Security/license/confidence: primary source and tests were located; GitHub advisory GHSA-2hhh-px8h-355p is direct security evidence. License file identified but full license text not reviewed in this pass. Confidence: medium.

### 72. simdjson/simdjson — parser API/docs

- Primary files: [`include/simdjson/generic/ondemand/parser.h`](https://github.com/simdjson/simdjson/blob/master/include/simdjson/generic/ondemand/parser.h); [`doc/ondemand_design.md`](https://github.com/simdjson/simdjson/blob/master/doc/ondemand_design.md); [`tests/ondemand`](https://github.com/simdjson/simdjson/tree/master/tests/ondemand); [`LICENSE`](https://github.com/simdjson/simdjson/blob/master/LICENSE).
- Format/architecture: On-Demand JSON, including concatenated-document iteration with explicit whitespace/JSON-sequence/comma-delimited format; parser capacity/batch size and buffer-padding preconditions are part of the API contract.
- Malformed/evolution/concurrency: invalid UTF-8 and oversized documents fail; documents have a capacity limit; padding bytes are read and caller must allocate them. Threaded lookahead may use one internal thread. Parser should process one document at a time and a thread-local parser must not be passed across threads.
- Transfer: make framing, byte limits, ownership/lifetime, UTF-8 validity, and concurrency constraints explicit in NLI ingestion contracts. A streaming decoder should not silently imply arbitrary-size input.
- Security/license/confidence: source documents preconditions precisely; license file found, not license text independently reviewed. Confidence: medium-high for parser contract.

### 73. fastfloat/fast_float — numeric decoder

- Primary files: [`include/fast_float/parse_number.h`](https://github.com/fastfloat/fast_float/blob/main/include/fast_float/parse_number.h); [`test/test.cpp`](https://github.com/fastfloat/fast_float/blob/main/test/test.cpp) (URL located, fetch unavailable); [`LICENSE-MIT`](https://github.com/fastfloat/fast_float/blob/main/LICENSE-MIT); repository README.
- Format/architecture: C++ `from_chars`-style number parser, returns end pointer plus `std::errc`, does not allocate or throw; README notes infinity/NaN parsing and adapted Wuffs code.
- Malformed/evolution: callers must validate both error and consumed input/end pointer, otherwise a valid numeric prefix can be accepted while trailing garbage remains. Exact behavior is controlled by the interface contract, not exceptions.
- Transfer: NLI numeric field parsing should distinguish invalid, out-of-range, and partial consumption; avoid ad hoc float parsing. Retain original token where fidelity matters.
- Security/license/confidence: MIT license file and primary API documentation; tests URL identified but contents could not be inspected. Confidence: medium.

### 74. Tencent/rapidjson — SAX parser

- Primary files: [`include/rapidjson/reader.h`](https://github.com/Tencent/rapidjson/blob/master/include/rapidjson/reader.h); [`doc/sax.md`](https://github.com/Tencent/rapidjson/blob/master/doc/sax.md); [`test/unittest`](https://github.com/Tencent/rapidjson/tree/master/test/unittest); [`license.txt`](https://github.com/Tencent/rapidjson/blob/master/license.txt).
- Format/architecture: event-driven SAX handler with callbacks for scalar values, keys, object/array boundaries; reader supports iterative parsing and configurable flags (encoding validation, comments, trailing commas, NaN/Inf, stop-at-root, full precision). Default flags are strict/no flags.
- Malformed/evolution: parse errors have a code and byte offset; handler can terminate parsing; internal stack is cleared via scope cleanup even on exceptions. Permissive syntax must be opt-in, important when evolving accepted inputs.
- Transfer: stream structured logs through bounded event handlers rather than always constructing whole DOM; use explicit strictness and carry byte offsets in diagnostics.
- Security/license/confidence: parser source contains MIT grant/header; tests directory identified. Confidence: high for documented API, medium for complete suite review.

### 75. nlohmann/json — C++ JSON DOM/SAX

- Primary files: [`single_include/nlohmann/json.hpp`](https://github.com/nlohmann/json/blob/develop/single_include/nlohmann/json.hpp); [`include/nlohmann/detail/exceptions.hpp`](https://github.com/nlohmann/json/blob/develop/include/nlohmann/detail/exceptions.hpp); [`tests/src`](https://github.com/nlohmann/json/tree/develop/tests/src); [`LICENSE.MIT`](https://github.com/nlohmann/json/blob/develop/LICENSE.MIT); README.
- Format/architecture: JSON DOM with a customizable SAX interface; SAX callbacks can reject/stop and `parse_error` receives position, last token, and exception. Library describes the single-header distribution.
- Malformed/evolution: SAX parse returns a boolean and does not throw on parse errors; consumer must handle callback and parse-error reporting. Exception type records byte position. These contracts are relevant when introducing schemas/unknown fields.
- Transfer: expose byte offset and last token in validation errors; support streaming/SAX for large event input and avoid treating success as full-document acceptance unless trailing input is checked.
- Security/license/confidence: MIT license identified; exact tests root identified. Confidence: medium-high.

### 77. yaml/pyyaml — YAML loaders

- Primary files: [`lib/yaml/constructor.py`](https://github.com/yaml/pyyaml/blob/main/lib/yaml/constructor.py); [`lib/yaml/loader.py`](https://github.com/yaml/pyyaml/blob/main/lib/yaml/loader.py); [`tests`](https://github.com/yaml/pyyaml/tree/main/tests); [`LICENSE`](https://github.com/yaml/pyyaml/blob/main/LICENSE); README.
- Format/architecture: Python YAML parser/emitter with pure-Python and optional LibYAML C bindings. README explicitly distinguishes `safe_load` for untrusted streams from general `load`.
- Malformed/evolution: loader choice changes which constructors/types may be created; therefore input trust and loader selection are part of security semantics. Separate Python and LibYAML test modes indicate behavior should be checked across implementations.
- Transfer: if NLI ever accepts YAML configuration, use safe construction only, enforce resource limits, and test both native and fallback parser paths; config format should be explicit.
- Security/license/confidence: project README provides the security-sensitive safe-load guidance; MIT license identified. Confidence: medium-high.

### 78. toml-lang/toml — format specification (not an implementation)

- Primary files: [`toml.md`](https://github.com/toml-lang/toml/blob/main/toml.md); [`README.md`](https://github.com/toml-lang/toml/blob/main/README.md); [`LICENSE`](https://github.com/toml-lang/toml/blob/main/LICENSE) (repository license file).
- Format/architecture: normative TOML spec; case-sensitive keys, UTF-8 document requirement, unambiguous mapping objective, scalar/array/table/date-time types.
- Malformed/evolution: spec version matters; current content specifies TOML 1.1 and structured rules. Consumers must not infer compatibility from file extension or accept version-dependent syntax silently.
- Transfer: version any NLI configuration/schema format; define strict UTF-8 and key-case semantics; use canonical schema rather than parser-specific permissive behavior.
- Security/license/confidence: specification source reviewed; license file identified but its text and tests were not independently reviewed (spec repository is not parser implementation). Confidence: medium.

### 79. BurntSushi/toml — Go decoder

- Primary files: [`decode.go`](https://github.com/BurntSushi/toml/blob/master/decode.go); [`parse.go`](https://github.com/BurntSushi/toml/blob/master/parse.go); [`decode_test.go`](https://github.com/BurntSushi/toml/blob/master/decode_test.go); [`fuzz_test.go`](https://github.com/BurntSushi/toml/blob/master/fuzz_test.go); [`COPYING`](https://github.com/BurntSushi/toml/blob/master/COPYING); README.
- Format/architecture: TOML 1.1 parser plus reflection decoder; supports struct/map mapping, custom unmarshaler, delayed `Primitive`, and metadata for undecoded keys. Decoder docs warn that loose mapping can leave data unrepresented; metadata can detect undecoded keys.
- Malformed/evolution: type mismatches and parser errors include contextual key/line information; unknown/unmapped keys are observable, allowing strict callers to reject schema drift. It does not handle cyclic destination types.
- Transfer: NLI config should detect unknown keys and report field path/line rather than silently dropping schema changes; preserve clear distinctions between parse errors and mapping/type errors.
- Security/license/confidence: fuzz tests and regression tests identified; COPYING located but exact license text not reviewed. Confidence: high for decoder behavior, medium for license classification.

## Not reviewed / deduplication

Prompt positions 76 (yaml/libyaml), 80–100 (toml-test, protobuf, Avro, Thrift, MessagePack, Serde, logging libraries, Sentry, OpenSearch, Pulsar, Kafka, kafka-go) remain unreviewed in this batch. No duplicate canonical upstream among the eight inspected projects was collapsed. The spec and BurntSushi implementation are distinct upstreams.
