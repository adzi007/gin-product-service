package http

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRouterRegistersProductOptionRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	appRouter := NewAppRouter(gin.New())
	r := appRouter.SetupRouter(nil, nil, nil)

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
