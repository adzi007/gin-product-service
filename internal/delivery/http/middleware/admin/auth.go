// Package admin provides the Auth0 admin authentication and authorization
// middleware. It is deliberately separate from the customer HS256 middleware:
// the two identity systems serve different users and ownership rules.
package admin

import (
	"net/http"
	"strings"

	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/logger"
	"gin-product-service/internal/infrastructure/metrics"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Namespaced Gin context keys holding the verified admin identity. They never
// collide with the customer middleware's user_id/name/email keys, so an admin
// credential cannot become customer identity.
const (
	ContextAdminSubjectKey     = "admin_subject"
	ContextAdminPermissionsKey = "admin_permissions"
)

// Fixed, safe denial messages. They never echo token text, raw claims, or
// validation internals.
const (
	messageUnauthenticated = "invalid or missing access token"
	messageForbidden       = "insufficient permission"
)

// RequireAuth authenticates exactly one Auth0 admin bearer credential and
// stores the verified identity in the Gin context under namespaced keys. It
// aborts before the protected handler on any failure.
//
// A nil verifier fails closed: an unconfigured deployment denies rather than
// allowing management access.
func RequireAuth(verifier domain.AdminTokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		if verifier == nil {
			deny(c, http.StatusUnauthorized, "UNAUTHENTICATED", messageUnauthenticated)
			return
		}

		rawToken, ok := bearerCredential(c)
		if !ok {
			deny(c, http.StatusUnauthorized, "UNAUTHENTICATED", messageUnauthenticated)
			return
		}

		identity, err := verifier.Verify(c.Request.Context(), rawToken)
		if err != nil {
			// The cause is kept for diagnosis only; token text, raw claims, and
			// subjects are never logged.
			logger.L(c.Request.Context()).Warn("admin authentication failed",
				zap.String("outcome", string(metrics.AuthOutcomeUnauthenticated)),
				zap.String("method", c.Request.Method),
				zap.String("route", c.FullPath()),
				zap.Error(err),
			)
			deny(c, http.StatusUnauthorized, "UNAUTHENTICATED", messageUnauthenticated)
			return
		}

		c.Set(ContextAdminSubjectKey, identity.Subject)
		c.Set(ContextAdminPermissionsKey, identity.Permissions)

		c.Next()
	}
}

// RequirePermission requires the route's exact permission on the identity
// RequireAuth established. A valid credential without that exact grant is
// forbidden; a missing identity is unauthenticated. It aborts before the
// protected handler on failure.
func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := Identity(c)
		if !ok {
			deny(c, http.StatusUnauthorized, "UNAUTHENTICATED", messageUnauthenticated)
			return
		}

		if !identity.HasPermission(permission) {
			logger.L(c.Request.Context()).Warn("admin authorization denied",
				zap.String("outcome", string(metrics.AuthOutcomeForbidden)),
				zap.String("method", c.Request.Method),
				zap.String("route", c.FullPath()),
			)
			deny(c, http.StatusForbidden, "FORBIDDEN", messageForbidden)
			return
		}

		record(c, metrics.AuthOutcomeAuthenticated)
		c.Next()
	}
}

// Identity returns the verified admin identity established by RequireAuth.
func Identity(c *gin.Context) (domain.AdminIdentity, bool) {
	value, ok := c.Get(ContextAdminPermissionsKey)
	if !ok {
		return domain.AdminIdentity{}, false
	}
	permissions, ok := value.([]string)
	if !ok {
		return domain.AdminIdentity{}, false
	}

	subjectValue, _ := c.Get(ContextAdminSubjectKey)
	subject, _ := subjectValue.(string)

	return domain.AdminIdentity{Subject: subject, Permissions: permissions}, true
}

// bearerCredential extracts the single bearer credential from the request. An
// absent, duplicated, wrong-scheme, empty, or otherwise ambiguous header is
// rejected.
func bearerCredential(c *gin.Context) (string, bool) {
	values := c.Request.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}

	parts := strings.Fields(strings.TrimSpace(values[0]))
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	if parts[1] == "" {
		return "", false
	}

	return parts[1], true
}

// deny aborts the chain with the fixed error envelope. The protected handler is
// never invoked.
func deny(c *gin.Context, status int, code, message string) {
	record(c, outcomeFor(status))

	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
			"details": gin.H{},
		},
	})
}

// outcomeFor maps a denial status to its bounded outcome.
func outcomeFor(status int) metrics.AuthOutcome {
	if status == http.StatusForbidden {
		return metrics.AuthOutcomeForbidden
	}
	return metrics.AuthOutcomeUnauthenticated
}

// record counts exactly one bounded outcome per admin request, keyed by method
// and the registered route template only.
func record(c *gin.Context, outcome metrics.AuthOutcome) {
	metrics.ObserveAuthorizationOutcome(c.Request.Method, c.FullPath(), outcome)
}
