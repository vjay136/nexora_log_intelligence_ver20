# V20 throughput qualification

`nli-throughput` creates real JSONL input, runs it through the production agent in one-shot mode, writes normalized and quarantine JSONL, flushes the local output, commits input offsets, and validates both output files and checkpoint accounting. The report records the host OS, architecture, logical CPU count, Go parallelism, requested size, source count, worker count, actual byte/record totals, and elapsed processing time.

From the V20 repository root on Windows with Docker running:

```powershell
powershell -File scripts\run-throughput-qualification.ps1
```

That runs a 10 MiB smoke qualification. It should pass exact record accounting and checkpoint checks; `target_met_on_this_host` stays false because the smoke run is smaller than the 5 GiB SLO.

Run the full 5 GiB qualification with:

```powershell
powershell -File scripts\run-throughput-qualification.ps1 -Full
```

The full run uses four generated input files and four agent source workers. Allow roughly 11 GiB free in the Docker VM for the input and normalized output. The full SLO passes only if at least 5 GiB were read, all generated records were normalized into valid JSON with no quarantine records, every checkpoint matches its source bytes and record count, and production processing completes within 600 seconds. A smoke run or extrapolated rate never counts as passing the 5 GiB SLO.

The generated workload is bounded in memory; output validation rejects and drains any record larger than 2 MiB. Reports are written in the repository root as `THROUGHPUT_SMOKE.json` or `THROUGHPUT_5G.json`; these are local measurements and must be reviewed for host, size, accounting, checkpoints, and SLO before making a performance claim.

For performance diagnosis, invoke `go run ./cmd/nli-throughput` directly and
pass `-cpu-profile /tmp/nli-v20.pprof`. This option is diagnostic only and does
not change the accounting or acceptance gate.
