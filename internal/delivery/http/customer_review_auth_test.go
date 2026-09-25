package http

import (
	"net/http"
	"testing"
)

// The four customer review routes keep their existing HS256 identity path: a
// valid customer token reaches the handler, and no token or an Auth0 token is
// rejected with the customer middleware's 401.
func TestCustomerReviewRoutesKeepCustomerAuthentication(t *testing.T) {
	app := newRouteTestApp(t)

	adminToken := app.auth.token(t, adminPermissions())
	customer := customerToken(t, routeTestUserID)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "create review",
			method: http.MethodPost,
			path:   "/api/v1/products/" + routeTestProductID.String() + "/reviews",
			body:   `{"rating":5,"title":"Great","comment":"Loved it"}`,
		},
		{
			name:   "update own review",
			method: http.MethodPatch,
			path:   "/api/v1/reviews/" + routeTestReviewID.String(),
			body:   `{"rating":4}`,
		},
		{
			name:   "delete own review",
			method: http.MethodDelete,
			path:   "/api/v1/reviews/" + routeTestReviewID.String(),
		},
		{
			name:   "list own reviews",
			method: http.MethodGet,
			path:   "/api/v1/users/me/reviews",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("customer token reaches the handler", func(t *testing.T) {
				before := app.probe.count()

				recorder := app.do(t, tc.method, tc.path, tc.body, bearer(customer))

				assertHandlerReached(t, recorder.Code, "customer credential")
				if app.probe.count() <= before {
					t.Fatal("the customer journey did not reach its use case")
				}
			})

			t.Run("no credential is unauthenticated", func(t *testing.T) {
				before := app.probe.count()

				recorder := app.do(t, tc.method, tc.path, tc.body, nil)

				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
				}
				code, message, details := assertDenialEnvelope(t, recorder)
				if code != "UNAUTHENTICATED" {
					t.Errorf("error.code = %q, want UNAUTHENTICATED", code)
				}
				if message == "" || details == nil {
					t.Errorf("envelope = %q / %v, want a message and an object details", message, details)
				}
				if app.probe.count() != before {
					t.Fatal("the customer journey ran without a credential")
				}
			})

			t.Run("auth0 token does not grant customer identity", func(t *testing.T) {
				before := app.probe.count()

				recorder := app.do(t, tc.method, tc.path, tc.body, bearer(adminToken))

				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
				}
				if app.probe.count() != before {
					t.Fatal("an Auth0 token completed a customer journey")
				}
			})

			t.Run("invalid bearer token is unauthenticated", func(t *testing.T) {
				recorder := app.do(t, tc.method, tc.path, tc.body, bearer("not-a-jwt"))

				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
				}
			})
		})
	}
}

// A customer token is not an admin credential, so an admin route rejects it.
func TestCustomerTokenDoesNotAuthorizeAdminRoutes(t *testing.T) {
	app := newRouteTestApp(t)
	customer := customerToken(t, routeTestUserID)

	// A representative management route for each protected module.
	adminProbes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/categories"},
		{http.MethodPatch, "/api/v1/products/" + routeTestProductID.String()},
		{http.MethodGet, "/api/v1/products/" + routeTestProductID.String() + "/reviews"},
		{http.MethodDelete, "/api/v1/variants/" + routeTestProductID.String()},
		{http.MethodPost, "/api/v1/inventory/reservations"},
	}

	for _, probe := range adminProbes {
		t.Run(probe.method+" "+probe.path, func(t *testing.T) {
			before := app.probe.count()

			recorder := app.do(t, probe.method, probe.path, "", bearer(customer))

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if app.probe.count() != before {
				t.Fatal("a customer token reached a protected management operation")
			}
		})
	}
}
