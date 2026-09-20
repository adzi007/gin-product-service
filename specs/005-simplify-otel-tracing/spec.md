# Feature Specification: Simplify OpenTelemetry Tracing

**Feature Branch**: `development` (existing branch; specification directory is independent)

**Created**: 2026-09-20

**Status**: Draft

**Input**: Simplify `internal/infrastructure/telemetry` enough for one engineer to understand in one sitting, reducing owned code by at least 60% while preserving service behavior, operator contracts, and trace compatibility.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Start the service safely (Priority: P1)

As an operator, I can leave tracing disabled or enable it with valid configuration without introducing a dependency on the trace destination's availability. Invalid enabled configuration fails before the service accepts traffic and identifies the field without exposing its contents.

**Why this priority**: A tracing refactor must not prevent normal startup, accept traffic with unusable configuration, or disclose credentials.

**Independent Test**: Exercise startup with tracing unset, false, valid, and invalid; capture listener binding, outbound telemetry traffic, errors, and logs.

**Acceptance Scenarios**:

1. **Given** tracing is unset or false and unused tracing settings are invalid, **When** startup and requests run, **Then** tracing performs no network I/O, causes no startup failure or wait, and adds no measurable request latency.
2. **Given** tracing is enabled with an unusable field, **When** startup runs, **Then** startup fails before listener binding with the field name and a safe reason, without echoing any offending value, header contents, or credential.
3. **Given** valid configuration and an unavailable trace destination, **When** startup and requests run, **Then** destination availability does not gate startup or change request results.
4. **Given** an existing supported operator configuration, **When** the refactored service loads it, **Then** its identity, sampling, propagation, export settings, defaults, and precedence retain their observable meaning.

### User Story 2 - Serve traffic during telemetry overload (Priority: P1)

As an operator, I can diagnose a saturated or failing export path while customers continue to receive normal responses.

**Why this priority**: Telemetry must remain independent of request progress and preserve existing operational alerts.

**Independent Test**: Hold export completion, exhaust the queue with concurrent requests, scrape `/metrics`, and record warnings using a controlled clock and warning sink.

**Acceptance Scenarios**:

1. **Given** an exporter that cannot make progress and a full queue, **When** requests finish spans, **Then** requests complete before the exporter is released and newly completed spans are dropped without waiting for telemetry capacity or export completion.
2. **Given** known queue drops and failed batch exports, **When** `/metrics` is scraped, **Then** each event is counted in its existing counter and bounded reason category, without double counting, renaming labels, or counting unsampled spans as queue drops.
3. **Given** repeated or concurrent drops and failures, **When** the warning window has not elapsed, **Then** at most one sanitized warning is emitted across the runtime in that window while all counter increments remain visible.
4. **Given** a slow warning sink during sustained pressure, **When** requests finish spans, **Then** request progress does not depend on that sink and telemetry does not create an unbounded backlog of work or goroutines.

### User Story 3 - Follow the same request across boundaries (Priority: P1)

As an engineer investigating a request, I see the same HTTP and database operations, parent relationships, and correlated logs after the refactor, with the same restrictions on propagated metadata.

**Why this priority**: A smaller implementation is useful only if existing investigations and trace consumers remain reliable.

**Independent Test**: Capture the same requests before and after the change with a local trace receiver, real PostgreSQL integration coverage, controlled Redis responses, and captured request logs.

**Acceptance Scenarios**:

1. **Given** a sampled request exercising application, PostgreSQL, and Redis work, **When** traces are compared, **Then** HTTP and database span names, kinds, and immediate parent relationships are unchanged; Redis work remains in the same trace under the corresponding application operation.
2. **Given** valid sampled or unsampled upstream context, **When** a request arrives, **Then** the upstream trace and sampling decision are honored regardless of the configured root ratio; absent or invalid upstream context uses the root ratio.
3. **Given** no baggage allowlist, **When** incoming or locally attached baggage is processed, **Then** no baggage is extracted or re-injected while valid W3C Trace Context still propagates.
4. **Given** an explicit allowlist and mixed baggage, **When** context is extracted and injected, **Then** only allowlisted keys survive each boundary, including the outbound Redis request.
5. **Given** request-scoped logs at the HTTP and application boundaries, **When** the active span changes, **Then** `trace_id` and `span_id` identify the corresponding trace and active span.
6. **Given** traced business/readiness routes, excluded routes, expected rejections, server failures, and interrupted requests, **When** they execute, **Then** current coverage, bounded names, response attributes, error classification, and sensitive-data exclusions remain unchanged.

### User Story 4 - Deliver accepted telemetry during bounded shutdown (Priority: P1)

As an operator stopping an instance, I get one bounded delivery opportunity for accepted telemetry even when HTTP draining is slow, and the process exits within its shutdown budget.

**Why this priority**: Termination must neither lose the entire telemetry delivery window nor overrun the deployment deadline.

**Independent Test**: Queue completed spans, keep an HTTP request open through the drain deadline, trigger termination, and observe delivery attempts and total elapsed time with both responsive and stalled destinations.

**Acceptance Scenarios**:

1. **Given** queued accepted telemetry and a slow HTTP drain, **When** termination begins, **Then** telemetry receives a positive, bounded delivery window before the total shutdown deadline, rather than first receiving an already-expired context.
2. **Given** an unavailable or stalled destination, **When** shutdown runs, **Then** telemetry stops intake and makes one bounded shutdown delivery attempt; total shutdown remains within the service budget and telemetry delivery failure does not become a business-operation failure.
3. **Given** shutdown already started or completed, **When** shutdown is called again, **Then** it does not initiate another flush or duplicate failure accounting.
4. **Given** an earlier caller deadline, **When** shutdown runs, **Then** both HTTP and telemetry honor that deadline and do not extend it to obtain their usual budgets.

### User Story 5 - Understand and extend tracing without mirrored interfaces (Priority: P2)

As a maintaining engineer, I can understand telemetry ownership in one sitting and add a use-case method without maintaining a second representation of the domain interfaces.

**Why this priority**: This is the primary maintainability benefit, gated by all P1 compatibility requirements.

**Independent Test**: Review the final production source and configuration ownership map, measure the source reduction, and demonstrate a new uninstrumented use-case method without editing telemetry files.

**Acceptance Scenarios**:

1. **Given** a new use-case method, **When** it is added and wired normally, **Then** no telemetry file or mirrored tracing interface requires an edit.
2. **Given** an application operation that needs a span, **When** instrumentation is opted into at its individual call site, **Then** the existing context and log correlation are preserved without adding a per-use-case decorator type.
3. **Given** the final implementation, **When** a reviewer traces startup, export outcomes, propagation, and shutdown, **Then** each configuration field has one validation owner and every shutdown-budget constraint refers to one authoritative service-budget definition.

### Edge Cases

- Explicit false with malformed unused settings versus a malformed enablement flag: the former remains disabled; the latter retains its safe field-level failure.
- Empty required identity or endpoint, credential-bearing/malformed URLs, malformed headers, invalid baggage keys, non-finite/out-of-range ratios, and non-positive/overflowing timeouts.
- Delegated SDK settings whose defaults, units, precedence, or invalid-value fallbacks differ from the current contract; delegation must not silently bypass a required startup rejection or alter supported configuration behavior.
- Root sampling ratios of zero and one; sampled and unsampled remote/local parents; malformed trace headers and malformed baggage independently.
- Baggage already present in a context or reusable outbound carrier; a denied key must not escape by reinjection.
- Simultaneous queue saturation, export failure, and warning suppression; dropped spans, unsampled spans, and failed export batches must remain distinct.
- Error messages or destination responses containing fake secret sentinels; none may surface in errors, warnings, metrics, or span attributes.
- Shutdown while a batch is already exporting, spans end during drain, no spans are queued, the caller deadline has elapsed, or termination is repeated.
- Cancellation and panic unwinding must not leave retained application spans unfinished or change business results.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Tracing MUST remain disabled by default. Explicitly disabled tracing MUST ignore unused invalid tracing settings, perform no telemetry network I/O, never delay or fail startup, and add no measurable request latency under the comparison protocol in SC-003.
- **FR-002**: Enabled-but-unusable configuration MUST fail before listener binding. Errors MUST identify the field and a value-free reason; errors and logs MUST never include the offending value, credentials, or header contents. A structurally valid configuration with an unreachable destination MUST still start.
- **FR-003**: Request goroutines MUST never wait for export completion, telemetry queue capacity, or operator-warning delivery. Saturation MUST drop newly completed spans and keep buffering and background work bounded.
- **FR-004**: The existing `/metrics` endpoint MUST expose `telemetry_spans_dropped_total{reason=...}` and `telemetry_exporter_failures_total{reason=...}` with their existing meanings: one increment per dropped span and one per failed batch export. Drop reasons remain `queue_full` and `other`; failure reasons remain `transport`, `timeout`, `server_error`, `serialization`, `unknown`, and `other`. No dynamic labels or additional label names are permitted.
- **FR-005**: Repeated telemetry warnings MUST be sanitized and limited across the runtime to at most one per warning window, retaining the current one-minute default. Warning suppression MUST NOT suppress metric accounting. SDK-originated diagnostics MUST obey the same confidentiality and rate-limit contract.
- **FR-006**: Baggage MUST be default-deny on extraction and injection, including preexisting context baggage. Only explicitly allowlisted keys may cross a boundary. W3C Trace Context propagation MUST remain independent of the baggage policy; malformed context MUST fail safely without breaking requests.
- **FR-007**: Sampling MUST remain parent-based, honoring valid upstream decisions and applying the configured ratio only to newly created roots. The current default root ratio of 0.10 MUST remain unchanged.
- **FR-008**: Trace context MUST remain continuous through HTTP, application, pgx, and Redis work. Existing HTTP and database span names and immediate parent relationships MUST remain unchanged. Existing Redis span naming, kinds, and parent correlation MUST also be preserved.
- **FR-009**: Request-scoped logs MUST continue to carry the active `trace_id` and `span_id`, refreshing correlation when entering retained child application spans.
- **FR-010**: Existing tracing coverage and export contracts MUST remain compatible: registered `/api/v1` routes and `/readyz` are traced; `/healthz`, `/metrics`, Swagger assets, and unmatched routes remain excluded. Resource identity, bounded attributes/statuses, sensitive-data exclusions, configured destination, headers, and transport behavior MUST retain their externally observable meaning.
- **FR-011**: Shutdown MUST stop telemetry intake and provide accepted telemetry one bounded shutdown delivery attempt that cannot be starved by HTTP drain. The combined shutdown MUST fit within the single service budget and any earlier caller deadline; repeated shutdown MUST be idempotent.
- **FR-012**: The production service shutdown budget MUST be defined in exactly one authoritative place and imported wherever it constrains another value. Its current value of 15 seconds MUST be preserved; test-specific shorter budgets may remain injectable.
- **FR-013**: Application span creation MUST be an explicit choice at individual call sites. Adding an uninstrumented use-case method MUST require no telemetry-file edits. Retained spans MUST preserve context, completion on all exits, safe error status, and log correlation without changing domain interfaces or handler signatures.
- **FR-014**: Every configuration field MUST have exactly one validation path shared by all supported construction entry points. Owned parsing MUST be limited to genuine startup gates. SDK-native tuning MUST be delegated without silently changing the supported operator contract or weakening FR-002; any compatibility conflict MUST be resolved in planning before implementation.
- **FR-015**: Every behavior numbered 1-8 in the request MUST have automated regression coverage that demonstrably fails when its guarantee is violated, including integration checks at listener, export, log, and `/metrics` boundaries where unit tests alone cannot prove the guarantee.
- **FR-016**: The simplification MUST preserve domain behavior, request/response contracts, persistence effects, transactions, and dependency direction required by the constitution. No schema migration, business concurrency change, or outward dependency from the domain is authorized.

### Required Refactor Constraints

These are explicit user requirements, retained here even though they constrain implementation. Detailed design belongs in the subsequent plan.

- **RC-A**: Retire `TokenGate`, `DropProcessor`, and `CapacityExporter` in favor of the SDK's native non-blocking batch processor. Retain only minimal adaptation for existing counters, safe failure reporting, and rate-limited warnings. Renaming or relocating a custom capacity/back-pressure mechanism does not satisfy this requirement.
- **RC-B**: Retire the per-use-case decorator hierarchy; application spans that remain must be opt-in at individual call sites. Retain existing application parents where removing them would change the required HTTP/database parent relationships.
- **RC-C**: Delegate SDK-native tuning, reduce owned parsing to startup gates, and give each field one validation path, as required by FR-014.
- **RC-D**: Define the shutdown budget once and import it wherever it constrains values, as required by FR-012.
- **RC-E**: Guarantee telemetry a bounded delivery opportunity despite slow HTTP drain, as required by FR-011.

### Scope and Non-Goals

- Scope includes the telemetry package and the startup, shutdown, wiring, and individual instrumentation call sites needed to meet the compatibility contract.
- No new backend, exporter, vendor SDK, or telemetry signal is introduced. Existing Prometheus counters and structured logs are preserved; no new metrics/logs export pipeline is introduced.
- No HTTP surface, domain interface, handler-signature, metric-name, or label-name changes.
- No business-rule, database-schema, transaction, or persistence change.
- The reduction must come from removing owned complexity, not moving the old processor/decorator/parser hierarchy to another package or compressing formatting.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Total physical lines in production Go files recursively under `internal/infrastructure/telemetry`, excluding `*_test.go`, fall by at least 60% from the fixed pre-change baseline. Blank and comment lines count equally before and after; normal formatting is retained. The inspected baseline is 1,781 lines, so the maximum is 712 lines. Review also confirms removed machinery was not relocated outside the measured directory.
- **SC-002**: Before/after captures of identical requests have the same HTTP/database span names, kinds, and immediate parent topology, plus unchanged Redis correlation and applicable resource/status attributes. Comparison normalizes generated trace/span IDs and timestamps while preserving graph edges; it does not flatten away application parents. At minimum, evidence includes a database-backed business request, readiness, and a reservation request exercising Redis and PostgreSQL.
- **SC-003**: Disabled-mode comparison against the same request path with tracing hooks bypassed detects no latency regression. Use at least ten interleaved warmed benchmark runs per variant on the same environment; neither median nor p95 latency may show a positive difference significant at the 95% confidence level. Independently verify zero telemetry network calls and no telemetry-dependent startup wait.
- **SC-004**: A blocked-exporter saturation test completes requests before releasing the exporter, accounts for all induced drops and failed exports exactly once through `/metrics`, and observes no more than one warning per window under concurrent pressure.
- **SC-005**: A slow-drain shutdown test observes a delivery attempt for queued telemetry before the common deadline and completes within the 15-second production budget or an injected shorter budget. Repeat with a stalled destination and repeated shutdown calls.
- **SC-006**: All eight requested behavior guarantees map to passing regression tests. The complete `go test ./...` suite, relevant PostgreSQL integration tests, and affected formatting/static checks pass before implementation is accepted.
- **SC-007**: A new use-case method can be added without changing any telemetry file, and review finds zero per-use-case tracing decorators, one authoritative shutdown-budget definition, and one validation owner per field.
- **SC-008**: An engineer unfamiliar with the refactor can read the production telemetry implementation and explain enablement, validation ownership, batching/drop reporting, propagation, sampling, and shutdown in one review session of at most 60 minutes without consulting a parallel hierarchy of domain interfaces. Record the reviewer and session duration with implementation evidence.

### Behavior-to-Test Coverage

| Requested behavior | Requirements | Required regression evidence |
| --- | --- | --- |
| 1. Disabled by default | FR-001 | Unset/false enablement, ignored unused invalid settings, zero network/startup wait, comparative latency measurements |
| 2. Safe startup rejection | FR-002, FR-014 | Invalid-field matrix, secret sentinels in captured diagnostics, listener never bound; valid unavailable destination still starts |
| 3. Non-blocking request path | FR-003 | Concurrent saturation with held exporter and slow warning sink; requests finish independently; bounded retained work |
| 4. Counters and warnings | FR-004, FR-005 | Actual scrape names/labels, exact event counts, no double counting, unknown-reason normalization, controlled-window concurrency test |
| 5. Baggage and trace context | FR-006 | Extraction/injection with empty and explicit allowlists, preexisting baggage, malformed headers, independent trace-context propagation |
| 6. Parent-based sampling | FR-007 | Sampled/unsampled parents at ratios zero/one, root decisions at configured/default ratios |
| 7. Bounded shutdown | FR-011, FR-012 | Slow HTTP drain plus queued spans, stalled export, early caller deadline, repeated stop, recorded delivery opportunity and total duration |
| 8. Correlated trace and logs | FR-008, FR-009, FR-010 | Before/after trace graph comparison across HTTP/application/pgx/Redis, captured active-span log IDs, coverage/status/privacy regressions |

## Assumptions

- This invocation creates specification artifacts only; application implementation and runtime verification occur in later phases.
- The current checkout at commit `844edc5a1d1fa375962441794a2e3d9c49ad94ec` is the compatibility and size baseline. File counts are `config.go` 399, `decorators.go` 609, `processor.go` 329, `propagation.go` 89, and `runtime.go` 355. This replaces the approximate 1,300-line estimate with a reproducible repository measurement.
- "Same parent relationships" means immediate parent relationships, not merely membership in one trace. Existing application spans that parent required spans are retained through explicit call-site instrumentation; new methods need not acquire spans automatically.
- "One bounded delivery attempt" means one shutdown drain/flush lifecycle, not a new promise of exactly one network request or guaranteed successful delivery. Existing bounded transport retries may operate within that deadline. Accepted telemetry means completed sampled spans admitted before telemetry intake closes; spans ending after closure are not promised delivery.
- The current supported defaults and operator configuration remain the compatibility reference. SDK ownership is not permission to silently change defaults, confidentiality, or required startup rejection. Planning must document field ownership and verify mismatches against the pinned SDK before choosing the minimal adapter.
- The 60-minute review session operationalizes "a single sitting"; the line-count target is an independent acceptance gate.

## Recorded Decisions

Maintainer-approved outcomes recorded during implementation (T002):

- **D1**: Adopt the experimental SDK observability bridge plus a small exporter outcome wrapper. Native drops are observed through the SDK's experimental processing counter (`OTEL_GO_X_OBSERVABILITY=true`, scope `otel.sdk.processor.span.processed`, filtered to `error.type=queue_full` and `otel.component.type=batching_span_processor`) and bridged directly to the existing Prometheus `telemetry_spans_dropped_total{reason="queue_full"}` counter. `TokenGate`, `DropProcessor`, and `CapacityExporter` are retired (RC-A satisfied); Stages 3-6 and the Polish phase proceed.
- **D2**: Remove the per-use-case `app.*` spans; database/Redis spans reparent directly under HTTP and application logs carry the HTTP span ID. FR-008/FR-009/RC-B/SC-002 are interpreted as documented-removal.
- **D3**: Five owned startup gates (enabled, endpoint, environment, baggage allowlist, shutdown timeout) with native SDK tuning delegation and parent-based 0.10 sampling.
- **D4**: Allowlisted/redacting debug diagnostics; unknown raw error text is omitted.

## Dependencies

- Existing [OpenTelemetry tracing specification](../004-opentelemetry-tracing/spec.md), instrumentation tests, and operator documentation supply the baseline contracts; this feature supersedes its decorator/capacity implementation choices only as explicitly required above.
- The existing OpenTelemetry Go SDK and OTLP/HTTP exporter must support a non-blocking batch path whose drop outcomes can be observed without rebuilding the retired capacity layer. Planning must verify that integration point and any necessary compatible dependency change.
- Existing explicit composition in `internal/wire`, HTTP middleware, request-context logger, pgx instrumentation, Redis adapter, and service lifecycle.
- A local trace capture receiver, isolated PostgreSQL test database, controlled Redis endpoint, and benchmark environment for implementation evidence; no hosted backend is required.
- The project [constitution](../../.specify/memory/constitution.md), particularly dependency direction, explicit composition, and verification/operational visibility. Moving spans to individual call sites must preserve these boundaries.
