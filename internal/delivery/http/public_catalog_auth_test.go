package http

import (
	"net/http"
	"testing"
)

// publicCatalogRequestPaths lists each public route with the query values its
// handler needs so the request can run to its use case.
var publicCatalogRequestPaths = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/categories?page=1"},
	{http.MethodGet, "/api/v1/categories/dropdown"},
	{http.MethodGet, "/api/v1/categories/1"},
	{http.MethodGet, "/api/v1/products?page=1"},
	{http.MethodGet, "/api/v1/products/" + "22222222-2222-4222-8222-222222222222"},
}

// The five catalog GET routes stay public: no header, a customer token, an Auth0
// token without read grants, and an invalid bearer token all reach the existing
// catalog handler without an admin denial.
func TestPublicCatalogRoutesIgnoreAdminAuthorization(t *testing.T) {
	app := newRouteTestApp(t)

	customer := customerToken(t, routeTestUserID)
	adminWithoutReadGrants := app.auth.token(t, nil)
	adminWithUnrelatedGrants := app.auth.token(t, []string{"products:update"})

	headerStates := []struct {
		name    string
		headers map[string]string
	}{
		{name: "no header", headers: nil},
		{name: "invalid bearer token", headers: bearer("not-a-jwt")},
		{name: "bearer without a token", headers: map[string]string{"Authorization": "Bearer    "}},
		{name: "customer token", headers: bearer(customer)},
		{name: "auth0 token without read grants", headers: bearer(adminWithoutReadGrants)},
		{name: "auth0 token with unrelated grants", headers: bearer(adminWithUnrelatedGrants)},
	}

	for _, route := range publicCatalogRequestPaths {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			for _, state := range headerStates {
				t.Run(state.name, func(t *testing.T) {
					before := app.probe.count()

					recorder := app.do(t, route.method, route.path, "", state.headers)

					assertHandlerReached(t, recorder.Code, "public catalog read")
					if recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
						t.Fatalf("status = %d, want no admin denial on a public read; body=%s",
							recorder.Code, recorder.Body.String())
					}
					if app.probe.count() <= before {
						t.Fatal("the public catalog read did not reach its use case")
					}
				})
			}
		})
	}
}
