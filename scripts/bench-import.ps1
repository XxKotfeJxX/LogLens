param(
    [int]$Entries = 1000000,
    [string]$Output = "./samples/benchmark.jsonl"
)

$ErrorActionPreference = "Stop"
go run ./scripts/generate-sample.go --format jsonl --entries $Entries --output $Output
Write-Host "Sample created at $Output. Import it through LogLens and record machine, file size, throughput, peak memory, and query p95."

