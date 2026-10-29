# V20 research gap D7 — structured log and JSON parser candidates

## Exact qualified count

**5 distinct upstreams qualified**: `go-logfmt/logfmt`, `vmihailenco/msgpack`, `json-iterator/go`, `valyala/fastjson`, and `tidwall/sjson`. Each has an identifiable parser/codec implementation, concrete test file, license, and repository history or release evidence. These are implementation references, not log-template mining algorithms. No upstream source copied; only this memo written.

## Qualified reviews

### `go-logfmt/logfmt`

- Evidence: [`decode.go`](https://github.com/go-logfmt/logfmt/blob/main/decode.go), [`decode_test.go`](https://github.com/go-logfmt/logfmt/blob/main/decode_test.go), [`LICENSE`](https://github.com/go-logfmt/logfmt/blob/main/LICENSE), [history](https://github.com/go-logfmt/logfmt/commits/main/), and [v0.6.1 release](https://github.com/go-logfmt/logfmt/releases/tag/v0.6.1) (2025-10-05). Repository history lists 86 commits. Decoder/encoder implement the practical logfmt key-value line format; upstream notes the format has no formal standard, and aims to reduce ambiguity. Tests directly exercise decoding/encoding. Errors depend on malformed key/value quoting and byte sequences; no separate log-line/record-size cap is documented in sampled evidence.
- NLI transfer: applicable to native logfmt records and quote/escape edge cases, not free-text template inference. MIT. Keep input-size caps in caller; review transitive Go module dependencies. Confidence: high.

### `vmihailenco/msgpack`

- Evidence: [`decode.go` on v5](https://github.com/vmihailenco/msgpack/blob/v5/decode.go), [`types_test.go` on v5](https://github.com/vmihailenco/msgpack/blob/v5/types_test.go), [`LICENSE`](https://github.com/vmihailenco/msgpack/blob/v5/LICENSE), [history](https://github.com/vmihailenco/msgpack/commits/v5), and [published v5.4.1](https://gopkg.in/vmihailenco/msgpack.v5). History lists 577 commits. Decoder supports streaming `io.Reader`, typed/reflective decoding, error reporting on invalid type/code and pointer misuse, pooled decoders, and explicit allocation limits (default byte allocation 1 MiB; slices/maps each capped at 1 million unless disabled). Tests exercise round-trip types and decoder errors.
- NLI transfer: only relevant if log/event payloads embed MessagePack; binary protocol parsing does not transfer to text tokenization. BSD-2-Clause; reflection, pooling, and configurable limits require integration review. Confidence: high.

### `json-iterator/go`

- Evidence: [`jsoniter.go`](https://github.com/json-iterator/go/blob/master/jsoniter.go), [`stream_test.go`](https://github.com/json-iterator/go/blob/master/stream_test.go), [`LICENSE`](https://github.com/json-iterator/go/blob/master/LICENSE), [repository history](https://github.com/json-iterator/go/commits/master/). Repository view lists 812 commits and archive status since 2025-12-15 (read-only). It is a drop-in JSON encoder/decoder with iterator/stream and reflection paths; test tree includes API, value, type, extension, and stream cases. Archive means no upstream fixes should be expected; benchmark claims in README are historical and workload-dependent.
- NLI transfer: a possible JSON envelope decoder or compatibility benchmark; little use for raw log templates. MIT. Prefer standard `encoding/json` or a maintained successor unless a measured compatibility/performance need justifies archived dependency risk. Confidence: high.

### `valyala/fastjson`

- Evidence: [`parser.go`](https://github.com/valyala/fastjson/blob/master/parser.go), [`parser_test.go`](https://github.com/valyala/fastjson/blob/master/parser_test.go), [`LICENSE`](https://github.com/valyala/fastjson/blob/master/LICENSE), [master history](https://github.com/valyala/fastjson/commits/master/). Repository history lists 191 commits; issue/PR activity includes 2026 parser behavior and arena fixes. It parses/validates complete JSON strings into a custom tree and returns errors for invalid JSON; parser/value instances are not safe for concurrent goroutines and returned values become invalid on the parser's next parse. README bounds expected memory at up to `sizeof(Value) * len(input)` and explicitly requires callers to cap input size; it cannot parse an `io.Reader` directly (Scanner handles sequences from strings). Fuzzing intent is documented, and tests include parser and validator behavior.
- NLI transfer: useful for validated JSON log envelopes and reusable value trees, not text log parsing. MIT; API lifetime and per-goroutine parser constraints are material. Confidence: high.

### `tidwall/sjson`

- Evidence: [`sjson.go`](https://github.com/tidwall/sjson/blob/master/sjson.go), [`sjson_test.go`](https://github.com/tidwall/sjson/blob/master/sjson_test.go), [`LICENSE`](https://github.com/tidwall/sjson/blob/master/LICENSE), [master history](https://github.com/tidwall/sjson/commits/master/). Repository lists 75 commits. SJSON edits a JSON value by path and rewrites targeted portions; README states input is expected to be well-formed JSON, and invalid JSON may produce unexpected results rather than panic. Invalid paths can return errors. Test file covers path/set behavior. This is mutation rather than parsing/validation; do not use as an input validator.
- NLI transfer: potential post-parse enrichment/normalization of structured records only after validation; avoid direct mutation of raw untrusted input. MIT; depends on related Tidwall JSON packages through module graph. Confidence: high.

## Caveats

These five qualify as parser/codec source references but have limited direct algorithm transfer to NLI. They demonstrate explicit malformed-input behavior, reuse/lifetime contracts, fuzzing, allocation bounds, and tests. No test suites were executed. Exact qualified count: **5**.
