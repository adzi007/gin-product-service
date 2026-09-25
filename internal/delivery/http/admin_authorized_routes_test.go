package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// assertHandlerReached fails when a status means the request never reached a
// registered business handler.
func assertHandlerReached(t *testing.T, status int, context string) {
	t.Helper()

	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		t.Fatalf("%s: status = %d, want the admin chain to authorize the request", context, status)
	case http.StatusNotFound:
		t.Fatalf("%s: status = %d, want a registered business route", context, status)
	}
}

// adminPermissions returns every distinct permission the admin policy requires.
func adminPermissions() []string {
	seen := make(map[string]struct{}, len(adminRoutePolicies))
	permissions := make([]string, 0, len(adminRoutePolicies))
	for _, policy := range adminRoutePolicies {
		if _, ok := seen[policy.permission]; ok {
			continue
		}
		seen[policy.permission] = struct{}{}
		permissions = append(permissions, policy.permission)
	}
	return permissions
}

// Every protected management route accepts a valid Auth0 token that carries the
// route's one assigned permission and lets the existing handler run.
func TestAdminRoutesAcceptTheirExactPermission(t *testing.T) {
	app := newRouteTestApp(t)

	for _, policy := range adminRoutePolicies {
		t.Run(policy.method+" "+policy.path, func(t *testing.T) {
			recorder := app.do(t, policy.method, materializePath(policy.path), "",
				app.adminHeaders(t, []string{policy.permission}))

			assertHandlerReached(t, recorder.Code, "authorized request")
		})
	}
}

// A token with several grants succeeds when the route's one required permission
// is among them.
func TestAdminRoutesAcceptTokenWithSeveralPermissions(t *testing.T) {
	app := newRouteTestApp(t)
	headers := app.adminHeaders(t, adminPermissions())

	for _, policy := range adminRoutePolicies {
		t.Run(policy.method+" "+policy.path, func(t *testing.T) {
			recorder := app.do(t, policy.method, materializePath(policy.path), "", headers)

			assertHandlerReached(t, recorder.Code, "multi-grant request")
		})
	}
}

// After authorization the existing business behavior is unchanged: the product
// update, review reads, and reservation creation reach their use cases.
func TestAuthorizedRequestsPreserveBusinessOutcomes(t *testing.T) {
	t.Run("product update reaches the update use case", func(t *testing.T) {
		app := newRouteTestApp(t)
		before := app.probe.count()

		recorder := app.do(t, http.MethodPatch, "/api/v1/products/"+routeTestProductID.String(),
			`{"title":"Updated title"}`,
			app.adminHeaders(t, []string{"products:update"}))

		assertHandlerReached(t, recorder.Code, "product update")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if app.probe.count() <= before {
			t.Fatal("product update use case was not invoked")
		}
	})

	t.Run("product review listing reaches the query use case", func(t *testing.T) {
		app := newRouteTestApp(t)
		before := app.probe.count()

		recorder := app.do(t, http.MethodGet, "/api/v1/products/"+routeTestProductID.String()+"/reviews", "",
			app.adminHeaders(t, []string{"products:reviews:read"}))

		assertHandlerReached(t, recorder.Code, "review listing")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if app.probe.count() <= before {
			t.Fatal("review listing use case was not invoked")
		}
	})

	t.Run("review summary reaches the summary use case", func(t *testing.T) {
		app := newRouteTestApp(t)
		before := app.probe.count()

		recorder := app.do(t, http.MethodGet, "/api/v1/products/"+routeTestProductID.String()+"/reviews/summary", "",
			app.adminHeaders(t, []string{"products:reviews:read"}))

		assertHandlerReached(t, recorder.Code, "review summary")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if app.probe.count() <= before {
			t.Fatal("review summary use case was not invoked")
		}
	})

	t.Run("review by id reaches the query use case", func(t *testing.T) {
		app := newRouteTestApp(t)
		before := app.probe.count()

		recorder := app.do(t, http.MethodGet, "/api/v1/reviews/"+routeTestReviewID.String(), "",
			app.adminHeaders(t, []string{"reviews:read"}))

		assertHandlerReached(t, recorder.Code, "review by id")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if app.probe.count() <= before {
			t.Fatal("review by id use case was not invoked")
		}
	})

	t.Run("reservation creation reaches the reservation use case", func(t *testing.T) {
		app := newRouteTestApp(t)
		before := app.probe.count()

		orderID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
		body := `{"orderId":"` + orderID.String() +
			`","expiresAt":"2030-01-02T03:04:05.123456+07:00","items":[{"id":"` +
			routeTestProductID.String() + `","qty":2}]}`

		recorder := app.do(t, http.MethodPost, "/api/v1/inventory/reservations", body,
			app.adminHeaders(t, []string{"inventory:reservations:create"}))

		assertHandlerReached(t, recorder.Code, "reservation creation")
		if recorder.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
		}
		if app.probe.count() <= before {
			t.Fatal("reservation use case was not invoked")
		}
	})
}

// A published, current key is reused across requests, so authorization does not
// depend on the identity provider being reachable per request.
func TestAuthorizedRoutesReuseTheKeyCache(t *testing.T) {
	app := newRouteTestApp(t)
	headers := app.adminHeaders(t, []string{"products:reviews:read"})

	for i := 0; i < 3; i++ {
		recorder := app.do(t, http.MethodGet, "/api/v1/products/"+routeTestProductID.String()+"/reviews", "", headers)
		assertHandlerReached(t, recorder.Code, "cached-key request")
	}
}
