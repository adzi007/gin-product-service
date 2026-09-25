package http

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// registeredBusinessRoutes returns every registered route under /api/v1 keyed by
// "<METHOD> <route template>". Operational endpoints (health, readiness,
// metrics, Swagger) and unmatched routes live outside this prefix.
func registeredBusinessRoutes(engine *gin.Engine) map[string]bool {
	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1") {
			routes[policyKey(route.Method, route.Path)] = true
		}
	}
	return routes
}

// The registered business routes and their access policies match the contract
// exactly: 34 admin, five public catalog reads, and four customer routes. A newly
// added business method/path without an explicit policy fails this test.
func TestRouterRegistersEveryBusinessRouteWithAnExplicitPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	appRouter := NewAppRouter(gin.New())
	r := appRouter.SetupRouter(nil, nil, nil, nil, nil, nil, "test-secret", nil)

	if got, want := len(adminRoutePolicies), 34; got != want {
		t.Fatalf("admin policy count = %d, want %d", got, want)
	}
	if got, want := len(publicCatalogRoutePolicies), 5; got != want {
		t.Fatalf("public catalog policy count = %d, want %d", got, want)
	}
	if got, want := len(customerReviewRoutePolicies), 4; got != want {
		t.Fatalf("customer review policy count = %d, want %d", got, want)
	}

	planned := make(map[string]routePolicy, len(allRoutePolicies()))
	for _, policy := range allRoutePolicies() {
		key := policyKey(policy.method, policy.path)
		if _, duplicate := planned[key]; duplicate {
			t.Fatalf("duplicate planned access policy for %s", key)
		}
		planned[key] = policy
	}
	if got, want := len(planned), 43; got != want {
		t.Fatalf("planned business route count = %d, want %d", got, want)
	}

	registered := registeredBusinessRoutes(r)

	for key := range planned {
		if !registered[key] {
			t.Errorf("planned business route %s is not registered", key)
		}
	}
	for key := range registered {
		policy, ok := planned[key]
		if !ok {
			t.Errorf("registered business route %s has no explicit access policy", key)
			continue
		}
		if policy.permission != "" && !hasAdminPermission(policy.permission) {
			t.Errorf("route %s maps to undeclared permission %q", key, policy.permission)
		}
	}

	if got, want := len(registered), 43; got != want {
		t.Fatalf("registered business route count = %d, want %d", got, want)
	}
}

// hasAdminPermission reports whether a permission string appears in the admin
// policy, so a typo in the policy table cannot pass unnoticed.
func hasAdminPermission(permission string) bool {
	for _, policy := range adminRoutePolicies {
		if policy.permission == permission {
			return true
		}
	}
	return false
}

// Every admin route maps to exactly one permission, and the permission set has no
// accidental duplicates across unrelated resources.
func TestAdminRoutePoliciesAssignExactPermissions(t *testing.T) {
	counts := map[string]int{}
	for _, policy := range adminRoutePolicies {
		if strings.TrimSpace(policy.permission) == "" {
			t.Fatalf("admin route %s %s has no permission", policy.method, policy.path)
		}
		counts[policy.permission]++
	}

	for permission, count := range counts {
		if count < 1 {
			t.Errorf("permission %q is assigned to %d routes", permission, count)
		}
		if strings.HasPrefix(permission, "admin:") || strings.HasPrefix(permission, "Bearer ") {
			t.Errorf("permission %q is not a plain grant name", permission)
		}
	}

	if len(counts) == 0 {
		t.Fatal("no admin permissions declared")
	}
}

func TestRouterRegistersProductOptionRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	appRouter := NewAppRouter(gin.New())
	r := appRouter.SetupRouter(nil, nil, nil, nil, nil, nil, "test-secret", nil)

	for _, route := range []struct {
		method string
		path   string
	}{
		{"PATCH", "/api/v1/products/:id"},
		{"DELETE", "/api/v1/products/:id"},
		{"POST", "/api/v1/products/:id/restore"},
		{"DELETE", "/api/v1/products/:id/purge"},
		{"POST", "/api/v1/products/:id/options"},
		{"PATCH", "/api/v1/products/:id/options/reorder"},
		{"PATCH", "/api/v1/products/:id/options/:option_id"},
		{"DELETE", "/api/v1/products/:id/options/:option_id"},
		{"POST", "/api/v1/products/:id/options/:option_id/values"},
		{"PATCH", "/api/v1/products/:id/options/:option_id/values/:value_id"},
		{"DELETE", "/api/v1/products/:id/options/:option_id/values/:value_id"},
		{"POST", "/api/v1/products/:id/variants"},
		{"POST", "/api/v1/products/:id/variants/bulk"},
		{"PATCH", "/api/v1/products/:id/variants/bulk"},
		{"POST", "/api/v1/products/:id/variants/bulk-delete"},
		{"PATCH", "/api/v1/products/:id/variants/reorder"},
		{"POST", "/api/v1/products/:id/media"},
		{"PATCH", "/api/v1/products/:id/media/reorder"},
		{"PATCH", "/api/v1/products/:id/media/:media_id"},
		{"DELETE", "/api/v1/products/:id/media/:media_id"},
		{"GET", "/api/v1/products/:id/reviews"},
		{"GET", "/api/v1/products/:id/reviews/summary"},
		{"POST", "/api/v1/products/:id/reviews"},
		{"GET", "/api/v1/reviews/:reviewId"},
		{"PATCH", "/api/v1/reviews/:reviewId"},
		{"DELETE", "/api/v1/reviews/:reviewId"},
		{"GET", "/api/v1/users/me/reviews"},
		{"PATCH", "/api/v1/variants/:id"},
		{"DELETE", "/api/v1/variants/:id"},
		{"POST", "/api/v1/variants/:id/restore"},
		{"POST", "/api/v1/variants/:id/media"},
		{"PATCH", "/api/v1/variants/:id/media/reorder"},
		{"DELETE", "/api/v1/variants/:id/media/:media_id"},
		{"POST", "/api/v1/inventory/reservations"},
	} {
		found := false
		for _, ri := range r.Routes() {
			if ri.Method == route.method && ri.Path == route.path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("route %s %s not registered", route.method, route.path)
		}
	}
}
