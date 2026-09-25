package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeVerifier stands in for the Auth0 verifier so middleware behavior can be
// asserted without key material or network access.
type fakeVerifier struct {
	identity domain.AdminIdentity
	err      error

	calls    int
	gotToken string
}

func (f *fakeVerifier) Verify(_ context.Context, rawToken string) (domain.AdminIdentity, error) {
	f.calls++
	f.gotToken = rawToken
	if f.err != nil {
		return domain.AdminIdentity{}, f.err
	}
	return f.identity, nil
}

// adminRoute is the probe route used across these tests. It records whether the
// protected handler was reached and exposes the identity middleware stored.
type adminRoute struct {
	engine   *gin.Engine
	reached  bool
	observed domain.AdminIdentity
	hadAdmin bool
}

func newAdminRoute(t *testing.T, verifier domain.AdminTokenVerifier, permission string) *adminRoute {
	t.Helper()
	gin.SetMode(gin.TestMode)

	route := &adminRoute{engine: gin.New()}

	route.engine.GET("/api/v1/products/:id/reviews",
		RequireAuth(verifier),
		RequirePermission(permission),
		func(c *gin.Context) {
			route.reached = true
			identity, ok := Identity(c)
			route.observed = identity
			route.hadAdmin = ok
			c.Status(http.StatusOK)
		},
	)
	return route
}

// perform issues a request with the given Authorization header values.
func perform(t *testing.T, engine *gin.Engine, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/products/abc/reviews", nil)
	for _, header := range headers {
		request.Header.Add("Authorization", header)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

// envelope decodes the fixed error envelope and asserts its shape.
func envelope(t *testing.T, recorder *httptest.ResponseRecorder) (code, message string, details map[string]any) {
	t.Helper()

	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if len(body) != 1 {
		t.Fatalf("response has top-level keys %v, want only \"error\"", body)
	}

	raw, ok := body["error"]
	if !ok {
		t.Fatalf("response %q has no error object", recorder.Body.String())
	}

	var errObject struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &errObject); err != nil {
		t.Fatalf("decode error object: %v", err)
	}
	if errObject.Details == nil {
		t.Fatalf("error.details = nil, want an object; body=%s", recorder.Body.String())
	}

	return errObject.Code, errObject.Message, errObject.Details
}

// ---------------------------------------------------------------------------
// Authentication success
// ---------------------------------------------------------------------------

func TestRequireAuthStoresVerifiedIdentityInNamespacedContext(t *testing.T) {
	verifier := &fakeVerifier{identity: domain.AdminIdentity{
		Subject:     "auth0|staff-42",
		Permissions: []string{"products:reviews:read", "products:update"},
	}}
	route := newAdminRoute(t, verifier, "products:reviews:read")

	recorder := perform(t, route.engine, "Bearer good-token")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !route.reached {
		t.Fatal("protected handler was not reached for an authorized request")
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	if verifier.gotToken != "good-token" {
		t.Fatalf("verifier token = %q, want the extracted bearer credential", verifier.gotToken)
	}
	if !route.hadAdmin {
		t.Fatal("Identity() reported no admin identity after successful authentication")
	}
	if route.observed.Subject != "auth0|staff-42" {
		t.Errorf("observed subject = %q, want the opaque subject", route.observed.Subject)
	}
	if !route.observed.HasPermission("products:update") {
		t.Errorf("observed permissions = %v, want the verified grants", route.observed.Permissions)
	}
}

func TestAdminContextKeysAreSeparateFromCustomerKeys(t *testing.T) {
	if ContextAdminSubjectKey == "user_id" || ContextAdminPermissionsKey == "user_id" {
		t.Fatal("admin context keys must be namespaced away from the customer user_id key")
	}

	verifier := &fakeVerifier{identity: domain.AdminIdentity{Permissions: []string{"reviews:read"}}}
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	customerKeysSeen := map[string]bool{}
	engine.GET("/probe",
		RequireAuth(verifier),
		RequirePermission("reviews:read"),
		func(c *gin.Context) {
			for _, key := range []string{"user_id", "name", "email"} {
				if _, ok := c.Get(key); ok {
					customerKeysSeen[key] = true
				}
			}
			c.Status(http.StatusOK)
		},
	)

	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set("Authorization", "Bearer good-token")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for key := range customerKeysSeen {
		t.Errorf("admin authentication set the customer context key %q", key)
	}
}

// ---------------------------------------------------------------------------
// Exact permission membership
// ---------------------------------------------------------------------------

func TestRequirePermissionRequiresExactGrant(t *testing.T) {
	tests := []struct {
		name        string
		permissions []string
		wantStatus  int
		wantReached bool
	}{
		{name: "exact grant", permissions: []string{"products:reviews:read"}, wantStatus: http.StatusOK, wantReached: true},
		{name: "exact grant among several", permissions: []string{"products:update", "products:reviews:read", "variants:delete"}, wantStatus: http.StatusOK, wantReached: true},
		{name: "suffix extension", permissions: []string{"products:reviews:read:all"}, wantStatus: http.StatusForbidden},
		{name: "prefix only", permissions: []string{"products:reviews"}, wantStatus: http.StatusForbidden},
		{name: "different case", permissions: []string{"Products:Reviews:Read"}, wantStatus: http.StatusForbidden},
		{name: "different separator", permissions: []string{"products/reviews/read"}, wantStatus: http.StatusForbidden},
		{name: "sibling resource", permissions: []string{"products:read"}, wantStatus: http.StatusForbidden},
		{name: "empty list", permissions: []string{}, wantStatus: http.StatusForbidden},
		{name: "nil list", permissions: nil, wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &fakeVerifier{identity: domain.AdminIdentity{Subject: "auth0|1", Permissions: tc.permissions}}
			route := newAdminRoute(t, verifier, "products:reviews:read")

			recorder := perform(t, route.engine, "Bearer good-token")

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if route.reached != tc.wantReached {
				t.Fatalf("handler reached = %v, want %v", route.reached, tc.wantReached)
			}
			if tc.wantStatus == http.StatusForbidden {
				code, message, _ := envelope(t, recorder)
				if code != "FORBIDDEN" {
					t.Errorf("error.code = %q, want FORBIDDEN", code)
				}
				if strings.TrimSpace(message) == "" {
					t.Error("error.message is empty, want a safe fixed message")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Ordering and fail-closed behavior
// ---------------------------------------------------------------------------

func TestRequireAuthRunsBeforeRequirePermission(t *testing.T) {
	t.Run("authentication failure aborts before the permission check", func(t *testing.T) {
		verifier := &fakeVerifier{err: domain.ErrAdminTokenInvalid}
		route := newAdminRoute(t, verifier, "products:reviews:read")

		recorder := perform(t, route.engine, "Bearer bad-token")

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", recorder.Code)
		}
		code, _, _ := envelope(t, recorder)
		if code != "UNAUTHENTICATED" {
			t.Fatalf("error.code = %q, want UNAUTHENTICATED", code)
		}
		if route.reached {
			t.Fatal("protected handler was reached despite a failed authentication")
		}
	})

	t.Run("a missing identity is unauthenticated, not forbidden", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()

		reached := false
		engine.GET("/probe", RequirePermission("products:reviews:read"), func(c *gin.Context) {
			reached = true
			c.Status(http.StatusOK)
		})

		request := httptest.NewRequest(http.MethodGet, "/probe", nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", recorder.Code)
		}
		if reached {
			t.Fatal("protected handler was reached without authentication")
		}
		code, _, _ := envelope(t, recorder)
		if code != "UNAUTHENTICATED" {
			t.Fatalf("error.code = %q, want UNAUTHENTICATED", code)
		}
	})
}

func TestRequireAuthWithNilVerifierFailsClosed(t *testing.T) {
	route := newAdminRoute(t, nil, "products:reviews:read")

	recorder := perform(t, route.engine, "Bearer anything")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if route.reached {
		t.Fatal("protected handler was reached with a nil verifier")
	}
}

// ---------------------------------------------------------------------------
// Bearer credential handling
// ---------------------------------------------------------------------------

func TestRequireAuthRejectsMalformedCredentials(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
	}{
		{name: "missing header"},
		{name: "empty header", headers: []string{""}},
		{name: "blank header", headers: []string{"    "}},
		{name: "scheme only", headers: []string{"Bearer"}},
		{name: "empty token", headers: []string{"Bearer   "}},
		{name: "wrong scheme", headers: []string{"Token good-token"}},
		{name: "basic scheme", headers: []string{"Basic dXNlcjpwYXNz"}},
		{name: "extra fields", headers: []string{"Bearer good-token extra"}},
		{name: "comma separated credentials", headers: []string{"Bearer first, Bearer second"}},
		{name: "duplicate headers", headers: []string{"Bearer first", "Bearer second"}},
		{name: "duplicate identical headers", headers: []string{"Bearer good-token", "Bearer good-token"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &fakeVerifier{identity: domain.AdminIdentity{Permissions: []string{"products:reviews:read"}}}
			route := newAdminRoute(t, verifier, "products:reviews:read")

			recorder := perform(t, route.engine, tc.headers...)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if route.reached {
				t.Fatal("protected handler was reached for a malformed credential")
			}
			if verifier.calls != 0 {
				t.Fatalf("verifier calls = %d, want 0 before any credential is extracted", verifier.calls)
			}

			code, message, details := envelope(t, recorder)
			if code != "UNAUTHENTICATED" {
				t.Errorf("error.code = %q, want UNAUTHENTICATED", code)
			}
			if strings.TrimSpace(message) == "" || len(details) != 0 {
				t.Errorf("envelope = %q / %v, want a safe message and empty details", message, details)
			}
			for _, header := range tc.headers {
				if strings.Contains(recorder.Body.String(), header) && header != "" {
					t.Errorf("response echoed the Authorization value %q", header)
				}
			}
		})
	}
}

// A case-insensitive scheme is still one well-formed bearer credential.
func TestRequireAuthAcceptsCaseInsensitiveScheme(t *testing.T) {
	verifier := &fakeVerifier{identity: domain.AdminIdentity{Permissions: []string{"products:reviews:read"}}}
	route := newAdminRoute(t, verifier, "products:reviews:read")

	recorder := perform(t, route.engine, "bearer good-token")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if verifier.gotToken != "good-token" {
		t.Fatalf("verifier token = %q, want good-token", verifier.gotToken)
	}
}

func TestDenialEnvelopeIsFixedAndSafe(t *testing.T) {
	verifier := &fakeVerifier{err: errors.New("crypto/rsa: verification error")}
	route := newAdminRoute(t, verifier, "products:reviews:read")

	recorder := perform(t, route.engine, "Bearer super-secret-token")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}

	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "UNAUTHENTICATED" {
		t.Errorf("error.code = %q, want UNAUTHENTICATED", body.Error.Code)
	}
	if body.Error.Message != messageUnauthenticated {
		t.Errorf("error.message = %q, want the fixed safe message %q", body.Error.Message, messageUnauthenticated)
	}
	if body.Error.Details == nil {
		t.Error("error.details = nil, want an object")
	}
	if strings.Contains(recorder.Body.String(), "super-secret-token") {
		t.Error("response echoed the bearer token")
	}
	if strings.Contains(recorder.Body.String(), "crypto/rsa") {
		t.Error("response leaked a verification internal")
	}
}

// ---------------------------------------------------------------------------
// Bounded observability
// ---------------------------------------------------------------------------

func TestDenialOutcomesAreRecordedWithBoundedLabels(t *testing.T) {
	const route = "/api/v1/products/:id/reviews"

	metrics.AuthorizationOutcomes.Reset()

	t.Run("authenticated", func(t *testing.T) {
		verifier := &fakeVerifier{identity: domain.AdminIdentity{Permissions: []string{"products:reviews:read"}}}
		route := newAdminRoute(t, verifier, "products:reviews:read")

		if recorder := perform(t, route.engine, "Bearer good-token"); recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		verifier := &fakeVerifier{err: domain.ErrAdminTokenInvalid}
		route := newAdminRoute(t, verifier, "products:reviews:read")

		if recorder := perform(t, route.engine, "Bearer bad-token"); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", recorder.Code)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		verifier := &fakeVerifier{identity: domain.AdminIdentity{Subject: "auth0|1", Permissions: []string{"products:update"}}}
		route := newAdminRoute(t, verifier, "products:reviews:read")

		if recorder := perform(t, route.engine, "Bearer good-token"); recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", recorder.Code)
		}
	})

	for _, outcome := range []metrics.AuthOutcome{
		metrics.AuthOutcomeAuthenticated,
		metrics.AuthOutcomeUnauthenticated,
		metrics.AuthOutcomeForbidden,
	} {
		got := testutil.ToFloat64(metrics.AuthorizationOutcomes.WithLabelValues(http.MethodGet, route, string(outcome)))
		if got != 1 {
			t.Errorf("outcome %q count = %v, want 1", outcome, got)
		}
	}
}

// captureLogOutput redirects the process logger to a pipe so denial logs can be
// inspected. The returned function closes the sink and returns what was written.
func captureLogOutput(t *testing.T) func() string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	previous := os.Stdout
	os.Stdout = writer
	if err := logger.Init("development"); err != nil {
		t.Fatalf("logger.Init() error = %v", err)
	}

	t.Cleanup(func() {
		os.Stdout = previous
		_ = reader.Close()
		_ = writer.Close()
		if err := logger.Init("production"); err != nil {
			t.Errorf("restore logger: %v", err)
		}
	})

	return func() string {
		// The logger captured the pipe as its sink, so closing the write end
		// ends the stream. The volume written here is far below the pipe buffer.
		_ = writer.Close()
		os.Stdout = previous

		captured, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read captured logs: %v", err)
		}
		return string(captured)
	}
}

func TestDenialLogsIdentifyOutcomeAndRouteWithoutCredentials(t *testing.T) {
	const (
		subject = "auth0|staff-secret-subject"
		token   = "header.payload.signature-secret"
	)

	logs := captureLogOutput(t)

	verifier := &fakeVerifier{err: domain.ErrAdminTokenInvalid}
	route := newAdminRoute(t, verifier, "products:reviews:read")
	if recorder := perform(t, route.engine, "Bearer "+token); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status %d", recorder.Code)
	}

	forbiddenVerifier := &fakeVerifier{identity: domain.AdminIdentity{Subject: subject, Permissions: []string{"products:update"}}}
	forbiddenRoute := newAdminRoute(t, forbiddenVerifier, "products:reviews:read")
	if recorder := perform(t, forbiddenRoute.engine, "Bearer "+token); recorder.Code != http.StatusForbidden {
		t.Fatalf("unexpected status %d", recorder.Code)
	}

	output := logs()

	for _, want := range []string{
		string(metrics.AuthOutcomeUnauthenticated),
		string(metrics.AuthOutcomeForbidden),
		"/api/v1/products/:id/reviews",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("denial logs are missing %q:\n%s", want, output)
		}
	}

	for _, forbidden := range []string{token, subject, "products:update", "payload.signature"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("denial logs contain the sensitive value %q:\n%s", forbidden, output)
		}
	}
}
