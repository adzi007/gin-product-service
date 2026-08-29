package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Context keys used to stash the authenticated user's claims in the Gin
// context. Handlers should read these via GetUserID / GetUserName /
// GetUserEmail rather than using raw string keys.
const (
	ContextUserIDKey    = "user_id"
	ContextUserNameKey  = "name"
	ContextUserEmailKey = "email"
)

// RequireAuth returns a Gin middleware that verifies the Authorization header
// (Bearer <token>) against the given HS256 secret. On success it stores the
// sub (as a uuid.UUID), name and email claims in the Gin context.
func RequireAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			abortUnauthorized(c, "missing authorization header")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			abortUnauthorized(c, "malformed authorization header")
			return
		}

		tokenString := strings.TrimSpace(parts[1])

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(secret), nil
		}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuedAt())
		if err != nil || !token.Valid {
			abortUnauthorized(c, "invalid token")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			abortUnauthorized(c, "invalid token claims")
			return
		}

		sub, _ := claims["sub"].(string)
		userID, err := uuid.Parse(sub)
		if err != nil {
			abortUnauthorized(c, "invalid user id in token")
			return
		}

		name, _ := claims["name"].(string)
		email, _ := claims["email"].(string)

		c.Set(ContextUserIDKey, userID)
		c.Set(ContextUserNameKey, name)
		c.Set(ContextUserEmailKey, email)

		c.Next()
	}
}

// abortUnauthorized writes the standard spec error shape and stops the chain.
func abortUnauthorized(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"code":    "UNAUTHENTICATED",
			"message": message,
			"details": gin.H{},
		},
	})
}

// GetUserID returns the authenticated user's id from the Gin context.
func GetUserID(c *gin.Context) (uuid.UUID, bool) {
	val, ok := c.Get(ContextUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := val.(uuid.UUID)
	return id, ok
}

// GetUserName returns the authenticated user's name claim from the Gin context.
func GetUserName(c *gin.Context) (string, bool) {
	val, ok := c.Get(ContextUserNameKey)
	if !ok {
		return "", false
	}
	name, ok := val.(string)
	return name, ok
}

// GetUserEmail returns the authenticated user's email claim from the Gin context.
func GetUserEmail(c *gin.Context) (string, bool) {
	val, ok := c.Get(ContextUserEmailKey)
	if !ok {
		return "", false
	}
	email, ok := val.(string)
	return email, ok
}
