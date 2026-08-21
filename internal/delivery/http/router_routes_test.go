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
		{"POST", "/api/v1/products/:id/variants"},
		{"POST", "/api/v1/products/:id/variants/bulk"},
		{"PATCH", "/api/v1/products/:id/variants/bulk"},
		{"POST", "/api/v1/products/:id/variants/bulk-delete"},
		{"PATCH", "/api/v1/products/:id/variants/reorder"},
		{"POST", "/api/v1/products/:id/media"},
		{"PATCH", "/api/v1/products/:id/media/reorder"},
		{"PATCH", "/api/v1/products/:id/media/:media_id"},
		{"DELETE", "/api/v1/products/:id/media/:media_id"},
		{"PATCH", "/api/v1/variants/:id"},
		{"DELETE", "/api/v1/variants/:id"},
		{"POST", "/api/v1/variants/:id/restore"},
		{"POST", "/api/v1/variants/:id/media"},
		{"PATCH", "/api/v1/variants/:id/media/reorder"},
		{"DELETE", "/api/v1/variants/:id/media/:media_id"},
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
