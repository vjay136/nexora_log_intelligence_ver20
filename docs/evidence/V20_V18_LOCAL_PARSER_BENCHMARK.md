# Ver18 and Ver20 local parser microbenchmark

Date: 2026-10-21  
Host: Windows/amd64, AMD Ryzen 5 5600H, 12 logical benchmark workers  
Go: 1.21.13 portable toolchain  
Command: `go test ./internal/benchmarks -run '^$' -bench 'BenchmarkParser(Throughput|Concurrency)$' -benchmem -benchtime=250ms -count=3`

| Benchmark median | Ver18 | Ver20 | Difference |
|---|---:|---:|---:|
| ParserThroughput ns/op | 107,528 | 107,860 | +0.31% |
| ParserConcurrency ns/op | 9,071 | 8,996 | -0.83% |
| ParserThroughput allocs/op | 260 | 260 | 0 |
| ParserConcurrency allocs/op | 135 | 135 | 0 |

The measurements are effectively level at this run length and show no material
regression in the preserved parser hot path. They are local microbenchmarks,
not a production throughput or five-gigabyte qualification. The unfiltered
Ver18 benchmark package also contains `BenchmarkAuthenticationAPI`, which
panics on a nil server in the released baseline; the comparison therefore uses
the two valid parser benchmarks only. Linux release qualification remains a
separate gate.

Raw Ver18 samples:

```text
BenchmarkParserThroughput-12  108037, 105972, 107528 ns/op
BenchmarkParserConcurrency-12 9071, 9403, 8672 ns/op
```

Raw Ver20 samples:

```text
BenchmarkParserThroughput-12  107860, 109549, 106061 ns/op
BenchmarkParserConcurrency-12 9004, 8996, 8937 ns/op
```
