# Feature Specification: End-to-End OpenTelemetry Tracing

**Feature Branch**: Not created (no branch hook configured)

**Created**: 2026-09-12

**Status**: Draft

**Input**: User description: "I want to implement OpenTelemetry. I want to trace from start to finish. Make sure the setup complies with clean architecture in this project. Do not set up Jaeger for now; set up OpenTelemetry and verify that the exporter works correctly."

## Clarifications

### Session 2026-09-12

- Q: When tracing is explicitly enabled but its startup configuration is invalid, how should the service behave? → A: Fail startup with a safe, actionable configuration error.
- Q: When a valid upstream trace context marks a request as not sampled, how should this service apply its sampling configuration? → A: Honor upstream sampling; locally sample only new root traces.
- Q: When the bounded telemetry queue is full, what should happen to newly completed trace data? → A: Drop new trace data without blocking request processing and report the drop safely.
- Q: Which client-visible outcomes should mark the inbound request segment as an error in the tracing system? → A: Mark server failures and abnormal interruptions as errors; record expected 4xx responses without error status.
- Q: How should the service handle incoming OpenTelemetry baggage, meaning arbitrary key-value metadata carried alongside trace context? → A: Propagate explicitly allowlisted keys only, default to none, and never copy baggage automatically into traces or logs.
- Q: Does this feature provision Jaeger or another trace backend? → A: No. Configure the service's OTLP exporter and prove its transport contract with an automated local capture receiver; backend deployment and UI-based inspection are deferred.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Follow a Request From Entry to Completion (Priority: P1)

An operator investigating a product-service request can open one trace and follow the request from its arrival, through authentication and the invoked business operation, to every relevant database or coordination-service interaction and the final response.

**Why this priority**: A complete causal view is the core value of tracing; partial or disconnected records would still leave the source of latency and failures ambiguous.

**Independent Test**: Send a sampled request to each representative route family and verify that one connected trace shows the request entry, business operation, downstream work, outcome, and total duration without requiring correlation by timestamps.

**Acceptance Scenarios**:

1. **Given** tracing is enabled and a valid product request is sampled, **When** the service completes the request, **Then** one trace connects the inbound request, invoked business operation, database work, and successful response in causal order.
2. **Given** tracing is enabled and a valid inventory-reservation request is sampled, **When** the service coordinates the reservation and commits its data changes, **Then** one trace connects the inbound request, reservation operation, coordination calls, transactional database work, and final response.
3. **Given** a request carries a valid upstream trace context, **When** the service processes it, **Then** the service activity appears as part of that upstream trace rather than as an unrelated trace.

---

### User Story 2 - Diagnose Failures and Slow Operations (Priority: P2)

An operator can use a trace to identify where a rejected, failed, cancelled, or slow request spent time and can correlate that trace with the service's structured logs.

**Why this priority**: Traces are operationally useful only when they distinguish failure points and connect to the detailed diagnostic evidence already emitted by the service.

**Independent Test**: Exercise authentication rejection, validation failure, database failure, coordination failure, request cancellation, and a deliberately slow dependency, then verify that each sampled trace identifies the failed or slow segment and its logs carry matching correlation identifiers.

**Acceptance Scenarios**:

1. **Given** a sampled request is rejected before business execution with an expected 4xx response, **When** authentication or input validation fails, **Then** the trace records the actual HTTP outcome without marking the request segment as an error and does not claim that uninvoked downstream work occurred.
2. **Given** a sampled downstream operation fails, **When** the service returns its established error response, **Then** the failing segment and overall request are marked unsuccessful with a safe diagnostic description.
3. **Given** a sampled request is cancelled or exceeds its deadline, **When** processing stops, **Then** the trace records the interruption and all started segments are closed.
4. **Given** the service writes a request-scoped log during a sampled request, **When** an operator inspects that log, **Then** its trace and segment identifiers locate the corresponding trace directly.

---

### User Story 3 - Operate Tracing Safely (Priority: P3)

An operator can configure and disable trace collection per environment, and the product service continues serving requests predictably when the trace destination is slow or unavailable.

**Why this priority**: Observability must be deployable across environments without leaking data or turning a monitoring-system outage into a product outage.

**Independent Test**: Start the service with tracing enabled, disabled, and pointed at both a local OTLP capture receiver and an unavailable destination; verify successful export, configuration handling, safe failure reporting, unchanged request behavior, and a bounded shutdown flush without installing a trace backend.

**Acceptance Scenarios**:

1. **Given** tracing is disabled, **When** the service starts and serves requests, **Then** business behavior is unchanged and no trace export is attempted.
2. **Given** tracing is enabled with valid configuration and a reachable OTLP capture receiver, **When** a sampled span is completed and flushed, **Then** the exporter sends a valid OTLP trace request containing the configured service and deployment environment and treats the receiver's success response as successful delivery.
3. **Given** the trace destination becomes unavailable, **When** requests are processed, **Then** request success and error contracts remain unchanged, resource use remains bounded, and the observability failure is reported without exposing credentials.
4. **Given** the service receives a graceful-shutdown signal, **When** shutdown completes, **Then** accepted requests finish within the existing shutdown policy and queued traces receive a bounded flush attempt.

### Edge Cases

- An incoming trace context is missing, malformed, or unsupported; the service starts a valid new trace and does not reject the request solely because of trace metadata.
- Incoming baggage is absent, malformed, or contains keys outside the configured allowlist; the service ignores those values without rejecting the request, propagating them downstream, or recording them in traces or logs.
- A request is rejected by authentication or validation before a use case runs; the request segment still records the final outcome while showing no nonexistent business or downstream work.
- A route template contains identifiers while the actual URL contains customer or resource values; exported route attributes use bounded route templates rather than raw paths.
- A database transaction rolls back or a coordination lease release fails after earlier work succeeded; the corresponding segments show their own outcomes while remaining children of the same request trace.
- Multiple database statements occur in one transaction; their timing and failures remain attributable without recording statement parameters or customer data.
- A client disconnects, the request times out, or the service panics; every started segment is closed and the final outcome is represented accurately.
- Trace buffering reaches its configured capacity; newly completed trace data is dropped without blocking request processing, and drops are reported through a rate-limited warning or metric without exposing sensitive data.
- Readiness checks execute database work and can be traced; high-volume scrape and static-documentation endpoints do not create unnecessary detailed trace traffic by default.

## Scope

This feature covers end-to-end distributed tracing for all product-service business routes under `/api/v1` and for readiness checks that exercise dependencies. A trace begins before request middleware, follows the existing request context through application operations, PostgreSQL work, and outbound reservation-coordination calls, and ends after the actual response outcome is known. It also covers trace/log correlation, safe deployment configuration, destination-failure behavior, and graceful shutdown.

The feature does not change product, category, review, inventory, authentication, persistence, or public API behavior. It does not replace the existing metrics or structured logging capabilities, create a new business-data store, or require domain entities and business rules to understand observability. Static API documentation and metrics-scrape traffic are excluded from detailed tracing by default. Provisioning or configuring Jaeger, an OpenTelemetry Collector, or any other trace storage and visualization backend is explicitly outside the current scope.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The service MUST produce standards-compatible distributed traces for sampled requests to every business route under `/api/v1` and every readiness check that invokes a dependency.
- **FR-002**: Each traced request MUST have one entry segment that starts before authentication and other request middleware, uses the matched route template and request method as bounded identifiers, and closes after the final response status is known.
- **FR-003**: A valid incoming trace context MUST be continued and its upstream sampling decision MUST be honored; local sampling policy MUST apply only when the service starts a new root trace because trace context is missing or invalid, without changing the request's response.
- **FR-004**: The active trace context MUST follow the existing request context across delivery, application orchestration, and infrastructure calls, including cancellation and deadlines.
- **FR-005**: Sampled traces MUST distinguish the invoked application operation from its inbound request and downstream dependency work so operators can attribute processing time without embedding tracing concerns in business entities or invariants.
- **FR-006**: PostgreSQL connections, queries, commands, and transaction lifecycle operations performed for a traced request MUST appear as correctly parented downstream segments with operation type, target system, duration, and outcome.
- **FR-007**: Outbound reservation-coordination requests MUST appear as correctly parented downstream segments and MUST propagate the active trace context using accepted interoperability conventions.
- **FR-008**: Successful, failed, rejected, cancelled, timed-out, and recovered-panic outcomes MUST be represented accurately on the relevant segment and completed request trace. The inbound request segment MUST be marked as an error for 5xx responses, recovered panics, and abnormal cancellations or interruptions; expected 4xx responses MUST retain their exact HTTP outcome without being marked as trace errors. A failed downstream segment MUST retain its own error outcome even if the overall request succeeds through established recovery behavior.
- **FR-009**: Request-scoped structured logs MUST contain the active trace identifier and segment identifier whenever a valid active trace exists, while preserving existing log fields and error causes.
- **FR-010**: Trace attributes, events, and correlated logs MUST NOT contain authorization values, credentials, request or response bodies, database parameters, raw coordination commands, personally identifying review content, or other customer-sensitive values. Incoming OpenTelemetry baggage MUST NOT be copied automatically into traces or logs; only explicitly allowlisted baggage keys MAY be propagated downstream, and the default allowlist MUST be empty.
- **FR-011**: Resource names, route names, operation names, status values, and other searchable attributes MUST use a documented, consistent, bounded-cardinality vocabulary; raw identifiers and unbounded URL paths MUST NOT be used as grouping labels.
- **FR-012**: Operators MUST be able to enable or disable tracing, identify the service and environment, select the trace destination, and control trace volume through deployment configuration without changing application code.
- **FR-013**: Missing optional tracing configuration or temporary trace-destination failure MUST NOT alter established business responses, persistence behavior, transaction guarantees, or coordination guarantees. Telemetry buffering and retry work MUST remain bounded; when the queue is full, newly completed trace data MUST be dropped without blocking request processing, and drops MUST be reported through a rate-limited warning or metric.
- **FR-014**: When tracing is explicitly enabled, invalid configuration that prevents the selected tracing mode from operating MUST cause startup to fail with a safe, actionable configuration error; credentials and other secrets MUST NOT appear in that error.
- **FR-015**: Graceful shutdown MUST stop accepting new trace work and make a bounded attempt to deliver already accepted telemetry within the service's shutdown window.
- **FR-016**: Tracing responsibilities MUST remain outside domain entities and business rules, MUST preserve the repository's inward dependency direction, and MUST be attached through delivery, infrastructure, or composition boundaries without changing domain outcomes.
- **FR-017**: All required observability dependencies MUST be explicitly composed at the service startup or composition root; hidden package-global business dependencies and service-locator access MUST NOT be introduced.
- **FR-018**: Existing metrics, structured logging behavior, route contracts, and automated business tests MUST remain operational after tracing is introduced.
- **FR-019**: Automated verification MUST cover trace creation, parent-child continuity, incoming and outgoing context propagation, success and failure outcomes, sensitive-data exclusion, disabled operation, graceful shutdown, and the OTLP export boundary. Export verification MUST use a local capture receiver to assert the request path, protocol content type, decodable non-empty trace payload, required resource attributes, successful flush, and safe handling of non-success or unavailable destinations.
- **FR-020**: Operator documentation MUST describe the trace coverage boundary, configuration fields, safe defaults, sampling behavior, expected attributes, failure behavior, and a repeatable way to verify exporter delivery and one complete request trace without requiring Jaeger or another vendor backend.

### Key Entities

- **Trace**: The complete causal record for one request as it moves through this service and any participating upstream or downstream systems; identified by one correlation identifier and an overall outcome.
- **Segment**: A timed unit of work within a trace, such as request handling, an application operation, a database action or transaction, or a reservation-coordination request; has a parent, name, timing, attributes, and outcome.
- **Trace Context**: The correlation state carried with a request and through internal calls so independently recorded segments join the same trace; includes cancellation and deadline behavior already associated with request processing.
- **Telemetry Configuration**: Environment-specific operational settings that control whether tracing runs, how the service is identified, where traces are delivered, and how trace volume and buffering are bounded. Credentials are configuration secrets, not trace data.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In acceptance testing with all requests selected for tracing, 100% of representative routes from the category, product, review, and inventory families produce one complete trace from request entry to final response.
- **SC-002**: In an inventory-reservation acceptance test, 100% of traces show the application operation, coordination calls, database transaction work, and final response as one connected parent-child hierarchy.
- **SC-003**: Across success, authentication rejection, validation error, dependency error, cancellation, timeout, and panic test cases, 100% of completed traces report the same high-level outcome as the client-visible response or interruption.
- **SC-004**: In automated sensitive-data checks, zero exported trace fields contain configured secrets, authorization values, payload bodies, database parameters, raw coordination commands, or review content.
- **SC-005**: Under representative load with the normal production trace-volume policy, tracing increases the 95th-percentile end-user request duration by no more than 10% and does not cause unbounded growth in memory or background work.
- **SC-006**: During a simulated trace-destination outage, 100% of otherwise valid test requests retain their established response and data-integrity behavior, and the service remains responsive for the full test period.
- **SC-007**: For every sampled request that emits structured application logs, 100% of those request-scoped log records contain identifiers that locate the matching trace.
- **SC-008**: A new operator can use the project documentation to verify successful OTLP exporter delivery and one complete test request trace in 15 minutes or less without modifying source code or installing Jaeger or another trace backend.
- **SC-009**: The complete automated test suite and all feature-specific observability tests pass with tracing enabled and disabled.

## Assumptions

- OpenTelemetry is the required interoperability standard, but the implementation plan will select compatible libraries, versions, and integration points.
- The initial trace boundary is this service's inbound HTTP request through its synchronous application, PostgreSQL, and reservation-coordination work. No message broker or background-job path currently exists in scope.
- The existing request context is the canonical carrier for trace state, cancellation, and deadlines inside the service.
- Production tracing uses parent-based sampling: valid upstream sampling decisions are honored, while configurable local sampling applies to new root traces. Tests may select every new root request for tracing to make coverage deterministic.
- Tracing is an operational aid and fails open after successful initialization: destination outages do not fail product requests. If tracing is explicitly enabled but its startup configuration is structurally invalid, the service fails startup with a safe, actionable error rather than silently disabling tracing or substituting fallback settings.
- Existing `/metrics` output and structured logs remain distinct observability signals and are correlated where useful rather than replaced.
- Health checks that do not invoke dependencies, metrics scrapes, and static Swagger assets are excluded from detailed tracing by default to limit noise; readiness checks remain in scope because they exercise the database.
- The service exports traces over OTLP/HTTP to an environment-supplied compatible endpoint. Selecting, deploying, storing data in, and operating a collector or visualization backend is deferred.

## Dependencies

- Existing propagation of `context.Context` from HTTP handlers through application use cases and domain-defined ports to PostgreSQL and Redis adapters.
- Existing structured logger support for request-context fields and the current Prometheus metrics endpoint.
- Access to an environment-managed OpenTelemetry-compatible destination where production traces are to be retained or inspected; automated exporter verification supplies its own local capture receiver and has no backend dependency.
- The repository's `internal/wire` composition root and startup/shutdown lifecycle.
- The project constitution, especially Domain-Centered Clean Architecture, SOLID Contracts and Explicit Composition, and Verification and Operational Visibility.
