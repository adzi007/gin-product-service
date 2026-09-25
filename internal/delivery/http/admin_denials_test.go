package http

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// denialCredential is one credential that must be rejected on every admin route,
// together with the outcome it must produce.
type denialCredential struct {
	name       string
	wantStatus int
	wantCode   string
	// header builds the request headers. An empty map omits the credential.
	header func(t *testing.T, app *routeTestApp, permission string) map[string]string
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// denialCredentials covers every credential failure the contract distinguishes
// from a missing permission.
func denialCredentials() []denialCredential {
	return []denialCredential{
		{
			name:       "no credential",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header:     func(*testing.T, *routeTestApp, string) map[string]string { return nil },
		},
		{
			name:       "wrong scheme",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(*testing.T, *routeTestApp, string) map[string]string {
				return map[string]string{"Authorization": "Basic dXNlcjpwYXNz"}
			},
		},
		{
			name:       "empty bearer token",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(*testing.T, *routeTestApp, string) map[string]string {
				return map[string]string{"Authorization": "Bearer    "}
			},
		},
		{
			name:       "malformed token",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(*testing.T, *routeTestApp, string) map[string]string {
				return bearer("not-a-jwt")
			},
		},
		{
			name:       "forged signature",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()

				attacker, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatalf("rsa.GenerateKey() error = %v", err)
				}

				claims := jwt.MapClaims{
					"iss":         app.auth.config.Issuer(),
					"aud":         routeTestAudience,
					"sub":         "auth0|attacker",
					"exp":         time.Now().Add(time.Hour).Unix(),
					"iat":         time.Now().Unix(),
					"permissions": []string{permission},
				}
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
				// The attacker claims the trusted kid without holding its key.
				token.Header["kid"] = app.auth.kid
				signed, err := token.SignedString(attacker)
				if err != nil {
					t.Fatalf("SignedString() error = %v", err)
				}
				return bearer(signed)
			},
		},
		{
			name:       "unknown kid",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()

				token := jwt.NewWithClaims(jwt.SigningMethodRS256, adminClaims(app, time.Now().Add(time.Hour), []string{permission}))
				token.Header["kid"] = "kid-not-published"
				signed, err := token.SignedString(app.auth.key)
				if err != nil {
					t.Fatalf("SignedString() error = %v", err)
				}
				return bearer(signed)
			},
		},
		{
			name:       "expired token",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()
				return bearer(app.auth.tokenWithClaims(t, adminClaims(app, time.Now().Add(-time.Minute), []string{permission})))
			},
		},
		{
			name:       "wrong issuer",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()

				claims := adminClaims(app, time.Now().Add(time.Hour), []string{permission})
				claims["iss"] = "https://attacker.us.auth0.com/"
				return bearer(app.auth.tokenWithClaims(t, claims))
			},
		},
		{
			name:       "wrong audience",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()

				claims := adminClaims(app, time.Now().Add(time.Hour), []string{permission})
				claims["aud"] = "https://another-api.example.com/"
				return bearer(app.auth.tokenWithClaims(t, claims))
			},
		},
		{
			name:       "unsupported hs256 algorithm",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()

				// Algorithm confusion: the customer secret must not verify an
				// admin credential.
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, adminClaims(app, time.Now().Add(time.Hour), []string{permission}))
				token.Header["kid"] = app.auth.kid
				signed, err := token.SignedString([]byte(routeTestCustomerSecret))
				if err != nil {
					t.Fatalf("SignedString() error = %v", err)
				}
				return bearer(signed)
			},
		},
		{
			name:       "alg none",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHENTICATED",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()

				token := jwt.NewWithClaims(jwt.SigningMethodNone, adminClaims(app, time.Now().Add(time.Hour), []string{permission}))
				token.Header["kid"] = app.auth.kid
				signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
				if err != nil {
					t.Fatalf("SignedString() error = %v", err)
				}
				return bearer(signed)
			},
		},
		{
			name:       "valid token with an unrelated permission",
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
			header: func(t *testing.T, app *routeTestApp, _ string) map[string]string {
				t.Helper()
				return bearer(app.auth.token(t, []string{"unrelated:permission"}))
			},
		},
		{
			name:       "valid token with a similar permission",
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
			header: func(t *testing.T, app *routeTestApp, permission string) map[string]string {
				t.Helper()
				return bearer(app.auth.token(t, []string{permission + ":extra"}))
			},
		},
		{
			name:       "valid token with several unrelated permissions",
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
			header: func(t *testing.T, app *routeTestApp, _ string) map[string]string {
				t.Helper()
				// The read grants exist in Auth0 but are required by no admin
				// route, so they must not authorize any protected action.
				return bearer(app.auth.token(t, []string{"categories:read", "products:read", "users:reviews:read"}))
			},
		},
		{
			name:       "valid token without a permissions claim",
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
			header: func(t *testing.T, app *routeTestApp, _ string) map[string]string {
				t.Helper()
				return bearer(app.auth.token(t, nil))
			},
		},
		{
			name:       "valid token with an empty permission list",
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
			header: func(t *testing.T, app *routeTestApp, _ string) map[string]string {
				t.Helper()
				return bearer(app.auth.token(t, []string{}))
			},
		},
	}
}

// adminClaims builds a valid admin claim set with a chosen expiry and grants.
func adminClaims(app *routeTestApp, expiry time.Time, permissions []string) jwt.MapClaims {
	claims := jwt.MapClaims{
		"iss": app.auth.config.Issuer(),
		"aud": routeTestAudience,
		"sub": "auth0|denial-matrix",
		"exp": expiry.Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	if permissions != nil {
		claims["permissions"] = permissions
	}
	return claims
}

// assertDenialEnvelope decodes the fixed denial envelope and asserts its shape.
func assertDenialEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) (code, message string, details map[string]any) {
	t.Helper()

	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	if len(body) != 1 {
		t.Fatalf("response has top-level keys %v, want only \"error\"; body=%s", keysOf(body), recorder.Body.String())
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
		t.Fatalf("error.details is not an object; body=%s", recorder.Body.String())
	}

	return errObject.Code, errObject.Message, errObject.Details
}

func keysOf(body map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	return keys
}

// Every protected management route distinguishes invalid credentials (401) from
// insufficient permission (403), returns the documented envelope, and performs
// no protected operation.
func TestAdminRoutesRejectUnauthenticatedAndUnauthorizedRequests(t *testing.T) {
	app := newRouteTestApp(t)
	credentials := denialCredentials()

	for _, policy := range adminRoutePolicies {
		t.Run(policy.method+" "+policy.path, func(t *testing.T) {
			for _, credential := range credentials {
				t.Run(credential.name, func(t *testing.T) {
					before := app.probe.count()

					recorder := app.do(t, policy.method, materializePath(policy.path), "",
						credential.header(t, app, policy.permission))

					if recorder.Code != credential.wantStatus {
						t.Fatalf("status = %d, want %d; body=%s", recorder.Code, credential.wantStatus, recorder.Body.String())
					}

					code, message, _ := assertDenialEnvelope(t, recorder)
					if code != credential.wantCode {
						t.Errorf("error.code = %q, want %q", code, credential.wantCode)
					}
					if strings.TrimSpace(message) == "" {
						t.Error("error.message is empty, want a safe fixed message")
					}
					if app.probe.count() != before {
						t.Fatal("a protected business operation ran on a denied request")
					}
				})
			}
		})
	}
}

// A valid customer token is not an admin credential.
func TestCustomerTokenIsRejectedOnAdminRoutes(t *testing.T) {
	app := newRouteTestApp(t)
	headers := bearer(customerToken(t, routeTestUserID))

	for _, policy := range adminRoutePolicies {
		t.Run(policy.method+" "+policy.path, func(t *testing.T) {
			before := app.probe.count()

			recorder := app.do(t, policy.method, materializePath(policy.path), "", headers)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			code, _, _ := assertDenialEnvelope(t, recorder)
			if code != "UNAUTHENTICATED" {
				t.Errorf("error.code = %q, want UNAUTHENTICATED", code)
			}
			if app.probe.count() != before {
				t.Fatal("a protected business operation ran for a customer credential")
			}
		})
	}
}

// An unknown method or path must not inherit authorization from a neighboring
// route.
func TestUnregisteredRoutesGainNoAuthorization(t *testing.T) {
	app := newRouteTestApp(t)
	adminHeaders := app.adminHeaders(t, adminPermissions())

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "unregistered method on a protected path", method: http.MethodPut, path: "/api/v1/products/" + routeTestProductID.String()},
		{name: "unregistered method on a protected subpath", method: http.MethodPut, path: "/api/v1/products/" + routeTestProductID.String() + "/reviews"},
		{name: "unknown api path", method: http.MethodGet, path: "/api/v1/secrets"},
		{name: "unknown admin path", method: http.MethodPost, path: "/api/v1/admin/roles"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, headers := range []map[string]string{nil, adminHeaders} {
				before := app.probe.count()

				recorder := app.do(t, tc.method, tc.path, "", headers)
				if recorder.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404 for an unregistered route; body=%s", recorder.Code, recorder.Body.String())
				}
				if app.probe.count() != before {
					t.Fatal("a business operation ran for an unregistered route")
				}
			}
		})
	}
}
