package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-secret"

func authTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", RequireAuth(testSecret), func(c *gin.Context) {
		userID, _ := GetUserID(c)
		name, _ := GetUserName(c)
		email, _ := GetUserEmail(c)
		c.JSON(http.StatusOK, gin.H{"user_id": userID.String(), "name": name, "email": email})
	})
	return r
}

func performAuthRequest(r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	r.ServeHTTP(w, req)
	return w
}

func signToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return s
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":   uuid.New().String(),
		"name":  "Jordan K.",
		"email": "jordan@example.com",
		"iat":   time.Now().Add(-time.Minute).Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
}

func TestRequireAuth_ValidTokenPopulatesContext(t *testing.T) {
	r := authTestRouter()
	claims := validClaims()

	w := performAuthRequest(r, "Bearer "+signToken(t, testSecret, claims))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		UserID string `json:"user_id"`
		Name   string `json:"name"`
		Email  string `json:"email"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body.UserID != claims["sub"] {
		t.Fatalf("expected user id %v, got %s", claims["sub"], body.UserID)
	}
	if body.Name != "Jordan K." || body.Email != "jordan@example.com" {
		t.Fatalf("unexpected name/email: %+v", body)
	}
}

func TestRequireAuth_ExpiredToken(t *testing.T) {
	r := authTestRouter()
	claims := jwt.MapClaims{
		"sub": uuid.New().String(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-time.Hour).Unix(),
	}

	w := performAuthRequest(r, "Bearer "+signToken(t, testSecret, claims))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRequireAuth_MalformedHeader(t *testing.T) {
	r := authTestRouter()

	for name, header := range map[string]string{
		"missing":   "",
		"basic":     "Basic abc",
		"no token":  "Bearer ",
		"no bearer": "notabearer token",
	} {
		t.Run(name, func(t *testing.T) {
			w := performAuthRequest(r, header)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", w.Code)
			}
		})
	}
}

func TestRequireAuth_BadSignature(t *testing.T) {
	r := authTestRouter()

	w := performAuthRequest(r, "Bearer "+signToken(t, "wrong-secret", validClaims()))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
