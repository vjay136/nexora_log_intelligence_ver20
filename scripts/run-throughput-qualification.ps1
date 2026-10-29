param(
    [switch]$Full,
    [string]$GoImage = "golang@sha256:737b40b61ce956d738bed59f18ba854d8d67e7a4c4fa63f64437f4c70247ac5b"
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$sizeGiB = if ($Full) { "5" } else { "0.01" }
$sources = if ($Full) { "4" } else { "2" }
$workers = $sources
$reportName = if ($Full) { "THROUGHPUT_5G.json" } else { "THROUGHPUT_SMOKE.json" }

Write-Host "Running $sizeGiB GiB throughput qualification in Docker ($sources sources, $workers workers)."
Write-Host "The full run needs about 11 GiB of free space in the Docker VM."

docker run --rm --mount "type=bind,source=$repoRoot,target=/src" -w /src $GoImage `
    go run ./cmd/nli-throughput `
    -gib $sizeGiB `
    -line-bytes 4096 `
    -sources $sources `
    -source-workers $workers `
    -work-dir "/tmp/nli-v20-throughput" `
    -report "/src/$reportName"

if ($LASTEXITCODE -ne 0) {
    throw "Throughput qualification failed with exit code $LASTEXITCODE. Inspect the report and agent output."
}

Write-Host "Report: $(Join-Path $repoRoot $reportName)"
