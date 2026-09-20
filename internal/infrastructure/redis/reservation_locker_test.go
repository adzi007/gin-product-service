package redis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/telemetry"

	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// commandArg captures the command array sent to the fake Upstash endpoint.
func decodeCommand(t *testing.T, r *http.Request) []interface{} {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var cmd []interface{}
	if err := json.Unmarshal(body, &cmd); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	return cmd
}

func writeResult(w http.ResponseWriter, result interface{}) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": result})
}

func TestReservationLocker_Acquire_SendsFixedFiveSecondTTL(t *testing.T) {
	var gotKeys, gotArgs []string
	var gotScript string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", got)
		}
		cmd := decodeCommand(t, r)
		if cmd[0] != "EVAL" {
			t.Fatalf("command = %v, want EVAL", cmd[0])
		}
		gotScript = cmd[1].(string)
		numKeys, err := strconv.Atoi(cmd[2].(string))
		if err != nil {
			t.Fatalf("numkeys %v: %v", cmd[2], err)
		}
		for _, k := range cmd[3 : 3+numKeys] {
			gotKeys = append(gotKeys, k.(string))
		}
		for _, a := range cmd[3+numKeys:] {
			gotArgs = append(gotArgs, a.(string))
		}
		writeResult(w, 1)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	_, err := locker.Acquire(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	if !strings.Contains(gotScript, "PX") {
		t.Errorf("acquire script must set a PX TTL: %s", gotScript)
	}
	if len(gotArgs) != 2 || gotArgs[1] != "5000" {
		t.Errorf("args = %v, want [owner, 5000] with fixed five-second TTL", gotArgs)
	}
	if len(gotKeys) != 2 || gotKeys[0] != "a" || gotKeys[1] != "b" {
		t.Errorf("keys = %v, want [a b]", gotKeys)
	}
}

func TestReservationLocker_Acquire_ReturnsOwnerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 1)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	token, err := locker.Acquire(context.Background(), []string{"k"})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty owner token")
	}
}

func TestReservationLocker_Acquire_ContentionFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 0)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	_, err := locker.Acquire(context.Background(), []string{"k"})
	if err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
}

func TestReservationLocker_Release_IsTokenChecked(t *testing.T) {
	var gotKeys, gotArgs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cmd := decodeCommand(t, r)
		numKeys, err := strconv.Atoi(cmd[2].(string))
		if err != nil {
			t.Fatalf("numkeys %v: %v", cmd[2], err)
		}
		for _, k := range cmd[3 : 3+numKeys] {
			gotKeys = append(gotKeys, k.(string))
		}
		for _, a := range cmd[3+numKeys:] {
			gotArgs = append(gotArgs, a.(string))
		}
		writeResult(w, 2)
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	if err := locker.Release(context.Background(), []string{"a", "b"}, "owner-1"); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	if len(gotKeys) != 2 || gotKeys[0] != "a" || gotKeys[1] != "b" {
		t.Errorf("release keys = %v, want [a b]", gotKeys)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "owner-1" {
		t.Errorf("release args = %v, want [owner-1]", gotArgs)
	}
}

func TestReservationLocker_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	locker := NewReservationLocker(srv.URL, "test-token")

	if _, err := locker.Acquire(context.Background(), []string{"k"}); err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
}

func TestReservationLocker_TransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 1)
	}))
	locker := NewReservationLocker(srv.URL, "test-token")
	srv.Close() // force a transport failure

	if _, err := locker.Acquire(context.Background(), []string{"k"}); err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
}

func TestReservationLocker_MissingConfigFailsClosed(t *testing.T) {
	locker := NewReservationLocker("", "")

	if _, err := locker.Acquire(context.Background(), []string{"k"}); err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
	if err := locker.Release(context.Background(), []string{"k"}, "owner"); err != nil {
		t.Fatalf("release should be a no-op for fail-closed locker, got %v", err)
	}
}

// --- tracing ----------------------------------------------------------------

type tracedLocker struct {
	locker   domain.ReservationLocker
	recorder *tracetest.SpanRecorder
	provider *sdktrace.TracerProvider
}

func newTracedLocker(t *testing.T, baseURL string, allowlist []string) *tracedLocker {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	return &tracedLocker{
		locker:   NewReservationLocker(baseURL, "test-token", WithTracing(provider, telemetry.NewPropagator(allowlist))),
		recorder: recorder,
		provider: provider,
	}
}

func tracedAttribute(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.Emit(), true
		}
	}
	return "", false
}

func TestReservationLocker_AcquireClientSpanIsParented(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 1)
	}))
	defer srv.Close()

	traced := newTracedLocker(t, srv.URL, nil)

	parentCtx, parent := traced.provider.Tracer("test").Start(context.Background(), "HTTP POST /api/v1/inventory/reservations")
	defer parent.End()

	if _, err := traced.locker.Acquire(parentCtx, []string{"reservation:order:1"}); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	span := findSpan(t, traced.recorder.Ended(), "redis.reservation.acquire")

	if got := span.SpanKind(); got != trace.SpanKindClient {
		t.Fatalf("span kind = %v, want client", got)
	}
	if got := span.Parent().SpanID(); got != parent.SpanContext().SpanID() {
		t.Fatalf("parent span ID = %q, want the active request span %q", got, parent.SpanContext().SpanID())
	}
	if got := span.SpanContext().TraceID(); got != parent.SpanContext().TraceID() {
		t.Fatalf("trace ID = %q, want %q", got, parent.SpanContext().TraceID())
	}

	if got, ok := tracedAttribute(span, "db.system"); !ok || got != "redis" {
		t.Errorf("db.system = %q (present=%v), want redis", got, ok)
	}
	if got, ok := tracedAttribute(span, "db.operation"); !ok || got != "acquire" {
		t.Errorf("db.operation = %q (present=%v), want acquire", got, ok)
	}
	if got, ok := tracedAttribute(span, "reservation.outcome"); !ok || got != "acquired" {
		t.Errorf("reservation.outcome = %q (present=%v), want acquired", got, ok)
	}
}

func TestReservationLocker_ReleaseClientSpanIsParented(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 1)
	}))
	defer srv.Close()

	traced := newTracedLocker(t, srv.URL, nil)

	parentCtx, parent := traced.provider.Tracer("test").Start(context.Background(), "HTTP POST /api/v1/inventory/reservations")
	defer parent.End()

	if err := traced.locker.Release(parentCtx, []string{"reservation:order:1"}, "owner-1"); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	span := findSpan(t, traced.recorder.Ended(), "redis.reservation.release")

	if got := span.SpanKind(); got != trace.SpanKindClient {
		t.Fatalf("span kind = %v, want client", got)
	}
	if got := span.Parent().SpanID(); got != parent.SpanContext().SpanID() {
		t.Fatalf("parent span ID = %q, want the active request span %q", got, parent.SpanContext().SpanID())
	}
	if got, ok := tracedAttribute(span, "reservation.outcome"); !ok || got != "released" {
		t.Errorf("reservation.outcome = %q (present=%v), want released", got, ok)
	}
}

func TestReservationLocker_InjectsTraceContextAndAllowlistedBaggageOnly(t *testing.T) {
	var gotTraceParent, gotBaggage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceParent = r.Header.Get("traceparent")
		gotBaggage = r.Header.Get("baggage")
		writeResult(w, 1)
	}))
	defer srv.Close()

	traced := newTracedLocker(t, srv.URL, []string{"safe-test"})

	parentCtx, parent := traced.provider.Tracer("test").Start(context.Background(), "HTTP POST /api/v1/inventory/reservations")
	defer parent.End()

	allowed, err := baggage.NewMember("safe-test", "allowed")
	if err != nil {
		t.Fatalf("NewMember(): %v", err)
	}
	disallowed, err := baggage.NewMember("customer-secret", "must-not-propagate")
	if err != nil {
		t.Fatalf("NewMember(): %v", err)
	}
	bag, err := baggage.New(allowed, disallowed)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	parentCtx = baggage.ContextWithBaggage(parentCtx, bag)

	if _, err := traced.locker.Acquire(parentCtx, []string{"reservation:order:1"}); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	if gotTraceParent == "" {
		t.Fatalf("no traceparent header was injected on the outbound coordination request")
	}
	if want := parent.SpanContext().TraceID().String(); !strings.Contains(gotTraceParent, want) {
		t.Errorf("traceparent = %q, want it to carry the parent trace ID %q", gotTraceParent, want)
	}
	if !strings.Contains(gotBaggage, "safe-test=allowed") {
		t.Errorf("baggage = %q, want the allowlisted member", gotBaggage)
	}
	if strings.Contains(gotBaggage, "customer-secret") {
		t.Errorf("baggage = %q must not carry disallowed keys", gotBaggage)
	}
}

// Exported spans must never contain the Redis script, keys, owner token, or
// credentials.
func TestReservationLocker_SpansExcludeCommandsKeysAndCredentials(t *testing.T) {
	const (
		leaseKey   = "reservation:order:sup3r-key"
		ownerToken = "owner-sup3r-token"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, 1)
	}))
	defer srv.Close()

	traced := newTracedLocker(t, srv.URL, nil)

	ctx := context.Background()
	if _, err := traced.locker.Acquire(ctx, []string{leaseKey}); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := traced.locker.Release(ctx, []string{leaseKey}, ownerToken); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	spans := traced.recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("recorded spans = %d, want 2", len(spans))
	}

	forbidden := []string{leaseKey, ownerToken, "test-token", "Bearer", "redis.call", "EVAL"}

	for _, span := range spans {
		checked := make([]string, 0, len(span.Attributes())+len(span.Events())+1)
		for _, attr := range span.Attributes() {
			checked = append(checked, string(attr.Key)+"="+attr.Value.Emit())
		}
		for _, event := range span.Events() {
			checked = append(checked, event.Name)
			for _, attr := range event.Attributes {
				checked = append(checked, string(attr.Key)+"="+attr.Value.Emit())
			}
		}
		checked = append(checked, span.Status().Description)

		for _, value := range checked {
			for _, secret := range forbidden {
				if strings.Contains(value, secret) {
					t.Errorf("span %q exported forbidden value %q from %q", span.Name(), secret, value)
				}
			}
		}
	}
}

func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	t.Fatalf("span %q not found; recorded spans: %v", name, names)
	return nil
}
