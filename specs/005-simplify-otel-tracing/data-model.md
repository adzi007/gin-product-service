# Data Model and Lifecycle

This refactor introduces no domain entity, persistence model, database table, migration, or HTTP payload. The following are operational contracts for the proposed design, subject to the decisions in [plan.md](plan.md).

## Owned startup configuration

| Field | Meaning and validation owner |
| --- | --- |
| Enabled | Strict true/false with existing trimming/case policy; absent means false. Parsed first. |
| Endpoint | Required absolute http/https traces URL when enabled; host required; no user info, query, or fragment; errors never echo input. |
| Environment | Deployment environment, falling back to APP_ENV; required when enabled. |
| BaggageAllowlist | Valid unique baggage keys; absent denies all. Retain current token grammar and reject duplicate/empty entries. |
| ShutdownTimeout | Positive integer milliseconds; default 5000; strictly less than lifecycle.ServiceShutdownBudget; detect integer/duration overflow. |

Disabled mode stops before unused-field validation and before SDK/exporter/observation setup.

One construction path owns validation. Tests and runtime composition must use it rather than assembling unchecked exported structs. Runtime construction must not revalidate a previously validated value.

SDK-owned tuning and the unresolved sampling exception are documented in [contracts/telemetry-contract.md](contracts/telemetry-contract.md). Neither runtime nor main may become a second tuning parser.

## Telemetry runtime

- Mode: disabled, running, stopping, stopped.
- Dependencies: explicit tracer provider, unchanged propagator, existing counters, safe diagnostic sink.
- Lifecycle: effective shutdown timeout and one shutdown result guarded for idempotency.
- SDK bridge (if D1 is selected): singleton process hook installed only while enabled runtime owns the SDK. It is not business state or persisted state.

Transitions:

1. Disabled construction returns a no-op provider and unchanged default-deny propagator without contacting a destination.
2. Enabled validation succeeds before resource/exporter/provider construction.
3. Running accepts ended sampled spans through native BSP; a full waiting queue rejects the new span and emits its observation event.
4. Stopping closes span intake and makes one bounded drain attempt for already-accepted work.
5. Stopped repeats no delivery or diagnostic accounting on further Shutdown calls.

The native SDK may continue a worker after its Shutdown wait deadline. The service's guarantee is a bounded delivery opportunity and bounded process shutdown, not successful export or forced termination of an exporter that ignores cancellation.

## Outcome event

| Event | Unit | Existing counter | Allowed reason values |
| --- | --- | --- | --- |
| Queue rejection | One sampled ended span actually rejected | telemetry_spans_dropped_total | queue_full, other |
| Failed export | One failed batch-level ExportSpans call | telemetry_exporter_failures_total | transport, timeout, server_error, serialization, unknown, other |

Unrecognized categories normalize to other. Raw endpoint/error/header/body values and SDK component identifiers are never labels. Unsampled spans and successfully queued spans are not drops.

An exporter wrapper records one failure and returns a sanitized marker; the global handler recognizes the marker and does not increment again. Configuration diagnostics have no export event and must not increment the failure counter.

## Warning state

A single runtime-wide limiter retains the one-minute default window. A bounded channel/notification and one worker decouple sink I/O from request completion. Counters update independently of warning delivery; a full notification slot may suppress another warning, never a counter increment. No unbounded goroutine-per-warning design.

Selected debug details pass through the same bounded diagnostic path. Redaction means allowlisting known-safe structure and omitting unknown text, not attempting to remove only known passwords.

## Shutdown allocation

Let T be the earlier of stop-start plus the service budget and the caller's deadline. Let R be the smaller of effective telemetry timeout and positive remaining time, or zero when disabled.

- Total context retains caller cancellation and ends at T.
- HTTP-drain context ends at T minus R.
- Flush context derives from total context; telemetry applies its own timeout too.
- Deadline-expired HTTP requests are canceled by closing active connections.
- Dependency close starts without blocking telemetry and is waited for only until T.
- Repeated stop does not start another close worker or flush.

No new full-budget context is created after drain. An already-canceled parent offers no delivery promise. Dependency cleanup has a single process owner so a deferred close cannot extend the bound after Stop.

## Trace graph

Current: upstream -> HTTP -> app.* -> pgx / Redis.

D2 removal proposal: upstream -> HTTP -> pgx / Redis. Application logs inherit HTTP trace/span IDs.

D2 exact-parent alternative: same current graph, with app.* spans at individual delivery call sites and child log enrichment.

Both variants preserve transport response behavior and continuous trace context. Only the second preserves immediate parents exactly; the selected contract must be reflected in spec and tests.
