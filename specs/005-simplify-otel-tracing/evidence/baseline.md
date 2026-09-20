# Baseline Evidence

Recorded before any production refactor (T001).

## Reproducible facts

- Commit: `844edc5a1d1fa375962441794a2e3d9c49ad94ec`
- Go toolchain: `go1.25.0 windows/amd64`
- Baseline test suite: `go test ./... -count=1` — all packages green (no failures).
- Production telemetry physical line count (excluding `*_test.go`):

| File | Lines |
| --- | --- |
| config.go | 399 |
| decorators.go | 609 |
| processor.go | 329 |
| propagation.go | 89 |
| runtime.go | 355 |
| **Total** | **1,781** |

Maximum allowed after refactor (SC-001): **712** lines.

## Propagation file hashes (SHA256)

- `internal/infrastructure/telemetry/propagation.go`:
  `1D091568BA6FFA4A8756A44E4F8E5FCE82D7FDA33C69EB2514EA1470870C582A`
- `internal/infrastructure/telemetry/propagation_test.go`:
  `10AFD5F98A2827DDB6B7411DC45664B7C4C3067B9AF6BE9DCE5C73E7E5E7ADB4`

These match the values captured during planning and MUST remain byte-for-byte
unchanged.

## Representative trace graphs

**Deferred.** Capturing the database-backed business, readiness, and reservation
trace graphs requires a live PostgreSQL database (`TEST_DATABASE_URL`), a running
service, and a local OTLP receiver. At baseline time no `TEST_DATABASE_URL` is
configured in the environment.

This evidence is only consumed by Stage 4 (US3) trace comparison, which is
currently blocked by the recorded D1 decision (see `implementation.md`). It will
be captured against the same seeded request fixtures before Stage 4 is unblocked.
