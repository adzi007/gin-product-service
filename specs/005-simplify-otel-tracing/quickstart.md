# Validation Guide: Simplify OpenTelemetry Tracing

This is an implementation validation guide, not a record of tests already run. Resolve the design gates in [plan.md](plan.md) before implementing their dependent stages. No application tests were run for the documentation-only planning invocation.

## Prerequisites

- Repository's Go 1.25 toolchain (see go.mod); existing module dependencies.
- A disposable PostgreSQL database for TEST_DATABASE_URL, with the repository's required test schema/migrations where repository tests need them. Never use production data.
- Local loopback networking for httptest OTLP receiver and fake Redis transport.
- For race tests, a supported compiler/CGO environment. No Jaeger, collector deployment, or hosted backend is needed.
- Run commands from the repository root. Examples use PowerShell; environment-mutating tests must not run in parallel with one another.

## Record the baseline before implementation

```powershell
git rev-parse HEAD
go version
go test ./... -count=1
Get-FileHash internal/infrastructure/telemetry/propagation.go,internal/infrastructure/telemetry/propagation_test.go -Algorithm SHA256
Get-ChildItem internal/infrastructure/telemetry -Recurse -Filter '*.go' |
  Where-Object Name -NotLike '*_test.go' |
  ForEach-Object { (Get-Content -LiteralPath $_.FullName).Count } |
  Measure-Object -Sum
```

Current measured total: 1,781 lines; final maximum 712. Count comments/blanks identically and retain normal formatting.

Unchanged SHA256 values captured during planning:

- propagation.go: 1D091568BA6FFA4A8756A44E4F8E5FCE82D7FDA33C69EB2514EA1470870C582A
- propagation_test.go: 10AFD5F98A2827DDB6B7411DC45664B7C4C3067B9AF6BE9DCE5C73E7E5E7ADB4

Preserve representative original OTLP captures before deleting decorators: real database-backed business request, /readyz, and reservation work exercising Redis and PostgreSQL. Store decoded names/kinds/parent edges/resource attributes with secrets excluded. Existing simulated lower-layer span tests alone do not satisfy this comparison.

## Stage checks

Execute the focused commands below after each stage and `go test ./... -count=1` before proceeding. All production edits and dependent test migrations for a stage belong in the same green change.

| Stage | Focused checks |
| --- | --- |
| 1. Shared budget | go test ./cmd/server ./internal/infrastructure/telemetry -count=1 |
| 2. Shutdown fix | go test ./cmd/server -count=1 -v |
| 3. Native batch outcomes | go test ./internal/infrastructure/telemetry ./internal/infrastructure/metrics ./cmd/server -count=1 |
| 4. Direct use cases | go test ./internal/wire ./internal/delivery/http/... ./internal/infrastructure/logger ./cmd/server -count=1 |
| 5. SDK tuning | go test ./cmd ./internal/infrastructure/telemetry ./cmd/server -count=1 |
| 6. Housekeeping | go test ./... -count=1; go vet ./... |

Where a proposed test does not yet exist, add it during its stage; a command reporting no tests matched is not evidence.

After stage 1, inspect constant references:

```powershell
rg -n 'ServiceShutdownBudget' cmd internal
```

Expected: one authoritative production definition in internal/lifecycle; callers import it. Test-specific injectable shorter budgets are allowed.

After stage 3:

```powershell
rg -n 'TokenGate|DropProcessor|CapacityExporter|WithBlocking' cmd internal
```

Expected: no surviving production gate/wrapper references or obsolete gate tests. A real native saturation test must drive the bridge, not manually fabricate a drop.

After stage 4:

```powershell
go list -f '{{join .Imports "\n"}}' ./internal/wire
rg -n 'Decorator|EnrichContext' internal/wire internal/delivery/http
```

Expected: wire has no telemetry import or decorator construction. The HTTP hook remains. The selected D2 topology governs any explicit call-site spans.

## Required lifecycle regression

Use a short injected service budget and a smaller telemetry timeout, plus a recording receiver. Queue a completed sampled span with batch delay longer than the test window. Keep an HTTP handler open through its allocated drain deadline.

Assert:

1. HTTP stops accepting work and the slow request is canceled when its drain allocation expires.
2. The receiver actually receives the prequeued span despite the slow drain.
3. Flush begins with positive time remaining; it does not inherit the drain cancellation.
4. Stop completes within the common budget, and a second Stop does not cause another flush.
5. A stalled destination, blocking database Close, and an earlier/canceled caller context do not extend the bound.

Use channel handshakes to establish exporter/handler state; avoid race-prone sleep-only tests. Demonstrate this regression fails against the original shutdown sequence. Release blocked test fixtures during cleanup.

## Required overload and diagnostic regression

Hold the first batch export, fill the native waiting queue, then finish additional sampled spans. Requests must complete before the held export is released. Scrape /metrics and assert exact queue_full increments and unchanged metric/label vocabulary.

Test transport, timeout/cancellation, server failure, unknown-reason normalization, and final-flush errors. One failed batch increments once, including when the SDK reports its sanitized error again. Configuration warnings are not export failures.

Advance an injected warning clock; at most one warning per shared window. Stall the warning sink and confirm request completion and bounded background work. Enable debug diagnostics with fake credentials in endpoint/header/error body; none may appear in any sink.

## Configuration and sampling regression

- Unset/false tracing ignores unused invalid settings and makes zero network calls.
- Invalid owned fields return field-only errors before binding.
- Valid enabled settings start against an unavailable destination.
- Delegated settings use the approved D3 defaults/precedence/invalid-value policy. Record approved changes instead of silently deleting expectations.
- Root ratios zero and one behave deterministically; valid sampled/unsampled upstream decisions win. Verify the .10 default and existing ratio-only operator configuration.
- Native SDK parsing emits no raw sensitive values, including malformed headers.
- Propagation tests run unchanged with empty/mixed allowlists and malformed trace context.

## End-to-end and final checks

Set TEST_DATABASE_URL to an isolated database using the local environment's normal secret mechanism, then run:

```powershell
go test ./internal/infrastructure/database/... ./internal/infrastructure/repository/... -count=1 -v
go test ./internal/delivery/http/... ./internal/infrastructure/redis/... ./cmd/server/... -count=1
go test ./... -count=1
go test -race ./...
go vet ./...
git diff --check
git diff --exit-code -- internal/infrastructure/telemetry/propagation.go internal/infrastructure/telemetry/propagation_test.go
```

Database tests marked SKIP do not establish integration success. Run trace captures against the same seeded request fixtures before/after. Compare HTTP/database names and the selected explicit parent policy; verify Redis context, sampling, status attributes, and active log IDs.

Run disabled-path benchmarks as specified in SC-003 using interleaved warmed measurements and a matching bypass path. Adding benchmark fixtures belongs to implementation; no benchmark pass is claimed here.

Recheck hashes and production line count. Review deletions to ensure old complexity was not moved outside telemetry. Have a reviewer complete the one-sitting comprehension check. Record test/capture/benchmark/size evidence before marking implementation complete.

## Unchanged follow-ups

Redis/JWT configuration relocation from setupRoutes and the unexported server return type are separate work. Do not include either cleanup in this sequence.
