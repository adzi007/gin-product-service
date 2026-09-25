package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gin-product-service/internal/delivery/http/handler"
	"gin-product-service/internal/domain"
	"gin-product-service/internal/infrastructure/auth0"
	"gin-product-service/internal/infrastructure/logger"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// routeTestCustomerSecret is the HS256 secret used for the customer review
// routes in these tests. It is deliberately distinct from the Auth0 trust path.
const routeTestCustomerSecret = "route-test-customer-secret"

const routeTestAudience = "https://api.example.com/"

// TestMain installs a no-op base logger so the many expected denial warnings
// from the authorization matrices do not flood the test output. Explicit log
// assertions install their own sink.
func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Business dependency stubs
// ---------------------------------------------------------------------------

// routeProbe records business-dependency invocations. Any recorded call proves
// the authorization chain let the request through to the real handler.
type routeProbe struct {
	mu    sync.Mutex
	calls int
}

func (p *routeProbe) record() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
}

func (p *routeProbe) count() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type stubCategoryQuery struct{ *routeProbe }

func (s *stubCategoryQuery) FindAll(context.Context, domain.ListCategoryParams) (domain.PaginatedCategories, error) {
	s.record()
	return domain.PaginatedCategories{}, nil
}

func (s *stubCategoryQuery) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	s.record()
	return nil, nil
}

func (s *stubCategoryQuery) GetByID(_ context.Context, id int) (domain.Category, error) {
	s.record()
	return domain.Category{ID: id}, nil
}

type stubCategoryInsert struct{ *routeProbe }

func (s *stubCategoryInsert) Create(context.Context, domain.CreateCategoryInput) (domain.Category, error) {
	s.record()
	return domain.Category{}, nil
}

type stubCategoryUpdate struct{ *routeProbe }

func (s *stubCategoryUpdate) Update(_ context.Context, id int, _ domain.UpdateCategoryInput) (domain.Category, error) {
	s.record()
	return domain.Category{ID: id}, nil
}

type stubCategoryDelete struct{ *routeProbe }

func (s *stubCategoryDelete) Delete(context.Context, int) error {
	s.record()
	return nil
}

type stubProductInsert struct{ *routeProbe }

func (s *stubProductInsert) Create(context.Context, domain.CreateProductInput) (domain.Product, error) {
	s.record()
	return domain.Product{}, nil
}

type stubProductQuery struct{ *routeProbe }

func (s *stubProductQuery) FindAll(context.Context, domain.ListProductParams) (domain.PaginatedProducts, error) {
	s.record()
	return domain.PaginatedProducts{}, nil
}

func (s *stubProductQuery) GetByID(_ context.Context, id uuid.UUID) (domain.ProductDetail, error) {
	s.record()
	return domain.ProductDetail{Product: domain.Product{ID: id}}, nil
}

func (s *stubProductQuery) GetByHandle(_ context.Context, handle string) (domain.ProductDetail, error) {
	s.record()
	return domain.ProductDetail{Product: domain.Product{Handle: handle}}, nil
}

type stubProductUpdate struct{ *routeProbe }

func (s *stubProductUpdate) Update(_ context.Context, id uuid.UUID, _ domain.UpdateProductInput) (domain.Product, error) {
	s.record()
	return domain.Product{ID: id}, nil
}

func (s *stubProductUpdate) Archive(context.Context, uuid.UUID) error {
	s.record()
	return nil
}

func (s *stubProductUpdate) Restore(context.Context, uuid.UUID) error {
	s.record()
	return nil
}

type stubProductDelete struct{ *routeProbe }

func (s *stubProductDelete) Purge(context.Context, uuid.UUID) error {
	s.record()
	return nil
}

type stubProductOption struct{ *routeProbe }

func (s *stubProductOption) Create(context.Context, uuid.UUID, domain.CreateOptionInput) (domain.ProductOption, error) {
	s.record()
	return domain.ProductOption{}, nil
}

func (s *stubProductOption) Rename(context.Context, uuid.UUID, uuid.UUID, domain.UpdateOptionInput) (domain.ProductOption, error) {
	s.record()
	return domain.ProductOption{}, nil
}

func (s *stubProductOption) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	s.record()
	return nil
}

func (s *stubProductOption) Reorder(context.Context, uuid.UUID, domain.ReorderOptionsInput) error {
	s.record()
	return nil
}

func (s *stubProductOption) AddValue(context.Context, uuid.UUID, uuid.UUID, domain.CreateOptionValueInput) (domain.ProductOptionValue, error) {
	s.record()
	return domain.ProductOptionValue{}, nil
}

func (s *stubProductOption) UpdateValue(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, domain.UpdateOptionValueInput) (domain.ProductOptionValue, error) {
	s.record()
	return domain.ProductOptionValue{}, nil
}

func (s *stubProductOption) DeleteValue(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	s.record()
	return nil
}

type stubProductMedia struct{ *routeProbe }

func (s *stubProductMedia) Create(context.Context, uuid.UUID, domain.BulkCreateMediaInput) ([]domain.ProductMedia, error) {
	s.record()
	return nil, nil
}

func (s *stubProductMedia) Update(context.Context, uuid.UUID, uuid.UUID, domain.UpdateMediaInput) (domain.ProductMedia, error) {
	s.record()
	return domain.ProductMedia{}, nil
}

func (s *stubProductMedia) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	s.record()
	return nil
}

func (s *stubProductMedia) Reorder(context.Context, uuid.UUID, []domain.PositionUpdate) error {
	s.record()
	return nil
}

func (s *stubProductMedia) AttachToVariant(context.Context, uuid.UUID, domain.AttachVariantMediaInput) (domain.VariantMedia, error) {
	s.record()
	return domain.VariantMedia{}, nil
}

func (s *stubProductMedia) DetachFromVariant(context.Context, uuid.UUID, uuid.UUID) error {
	s.record()
	return nil
}

func (s *stubProductMedia) ReorderVariantMedia(context.Context, uuid.UUID, []domain.PositionUpdate) error {
	s.record()
	return nil
}

type stubProductVariant struct{ *routeProbe }

func (s *stubProductVariant) Create(context.Context, uuid.UUID, domain.CreateVariantInput) (domain.Variant, error) {
	s.record()
	return domain.Variant{}, nil
}

func (s *stubProductVariant) BulkCreate(context.Context, uuid.UUID, domain.BulkCreateVariantsInput) ([]domain.Variant, error) {
	s.record()
	return nil, nil
}

func (s *stubProductVariant) Update(_ context.Context, id uuid.UUID, _ domain.UpdateVariantInput) (domain.Variant, error) {
	s.record()
	return domain.Variant{ID: id}, nil
}

func (s *stubProductVariant) BulkUpdate(context.Context, uuid.UUID, domain.BulkUpdateVariantsInput) ([]domain.Variant, error) {
	s.record()
	return nil, nil
}

func (s *stubProductVariant) Delete(context.Context, uuid.UUID) error {
	s.record()
	return nil
}

func (s *stubProductVariant) BulkDelete(context.Context, uuid.UUID, domain.BulkDeleteVariantsInput) error {
	s.record()
	return nil
}

func (s *stubProductVariant) Restore(_ context.Context, id uuid.UUID) (domain.Variant, error) {
	s.record()
	return domain.Variant{ID: id}, nil
}

func (s *stubProductVariant) Reorder(context.Context, uuid.UUID, []domain.PositionUpdate) error {
	s.record()
	return nil
}

type stubReviewInsert struct{ *routeProbe }

func (s *stubReviewInsert) Create(_ context.Context, productID, userID uuid.UUID, _ string, _ domain.CreateReviewInput) (domain.Review, error) {
	s.record()
	return domain.Review{ID: uuid.New(), ProductID: productID, UserID: userID}, nil
}

type stubReviewQuery struct{ *routeProbe }

func (s *stubReviewQuery) ListByProduct(_ context.Context, _ uuid.UUID, _ domain.ReviewFilter) (domain.PaginatedReviews, error) {
	s.record()
	return domain.PaginatedReviews{}, nil
}

func (s *stubReviewQuery) GetByID(_ context.Context, id uuid.UUID) (domain.Review, error) {
	s.record()
	return domain.Review{ID: id}, nil
}

func (s *stubReviewQuery) ListByUser(_ context.Context, userID uuid.UUID, _, _ int) (domain.PaginatedUserReviews, error) {
	s.record()
	return domain.PaginatedUserReviews{}, nil
}

type stubReviewUpdate struct{ *routeProbe }

func (s *stubReviewUpdate) Update(_ context.Context, id, userID uuid.UUID, _ string, _ domain.UpdateReviewInput) (domain.Review, error) {
	s.record()
	return domain.Review{ID: id, UserID: userID}, nil
}

type stubReviewDelete struct{ *routeProbe }

func (s *stubReviewDelete) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	s.record()
	return nil
}

type stubReviewSummary struct{ *routeProbe }

func (s *stubReviewSummary) GetSummary(_ context.Context, productID uuid.UUID) (domain.RatingSummary, error) {
	s.record()
	return domain.RatingSummary{ProductID: productID, RatingBreakdown: map[int]int{}}, nil
}

type stubReservation struct{ *routeProbe }

func (s *stubReservation) Create(_ context.Context, orderID uuid.UUID, _ time.Time, items []domain.ReservationRequestItem) (domain.ReservationResult, error) {
	s.record()
	return domain.ReservationResult{OrderID: orderID}, nil
}

// ---------------------------------------------------------------------------
// Auth fixtures
// ---------------------------------------------------------------------------

// adminAuthFixture builds a real Auth0 verifier pointed at a local TLS JWKS
// endpoint, so route-level tests present genuine RS256 credentials without
// reaching Auth0.
type adminAuthFixture struct {
	verifier *auth0.Verifier
	key      *rsa.PrivateKey
	kid      string
	config   auth0.Config
}

func newAdminAuthFixture(t *testing.T) *adminAuthFixture {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	const kid = "kid-route-test"

	document, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": auth0.RSAKeyType,
			"kid": kid,
			"use": "sig",
			"alg": auth0.SigningAlgorithm,
			"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}},
	})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document)
	}))
	t.Cleanup(server.Close)

	config, err := auth0.NewConfig("tenant.us.auth0.com", routeTestAudience)
	if err != nil {
		t.Fatalf("auth0.NewConfig() error = %v", err)
	}

	verifier, err := auth0.NewVerifier(config,
		auth0.WithHTTPClient(server.Client()),
		auth0.WithJWKSURL(server.URL+"/.well-known/jwks.json"),
	)
	if err != nil {
		t.Fatalf("auth0.NewVerifier() error = %v", err)
	}

	return &adminAuthFixture{verifier: verifier, key: key, kid: kid, config: config}
}

// token signs an Auth0-shaped RS256 access token with the given permissions.
// Passing nil permissions omits the claim entirely.
func (f *adminAuthFixture) token(t *testing.T, permissions []string) string {
	t.Helper()

	claims := jwt.MapClaims{
		"iss": f.config.Issuer(),
		"aud": routeTestAudience,
		"sub": "auth0|route-test-staff",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	if permissions != nil {
		claims["permissions"] = permissions
	}

	return f.sign(t, claims)
}

// tokenWithClaims signs arbitrary claims for negative cases (forged issuer,
// expired validity, and so on).
func (f *adminAuthFixture) tokenWithClaims(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	return f.sign(t, claims)
}

func (f *adminAuthFixture) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = f.kid
	signed, err := token.SignedString(f.key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return signed
}

// customerToken signs the existing HS256 customer token the review routes use.
func customerToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   userID.String(),
		"name":  "Route Test Customer",
		"email": "customer@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})
	signed, err := token.SignedString([]byte(routeTestCustomerSecret))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return signed
}

// ---------------------------------------------------------------------------
// Router fixture
// ---------------------------------------------------------------------------

// routeTestApp wires the production router to stub business dependencies so
// route-level tests exercise the real middleware chains.
type routeTestApp struct {
	engine *gin.Engine
	probe  *routeProbe
	auth   *adminAuthFixture
}

func newRouteTestApp(t *testing.T) *routeTestApp {
	t.Helper()

	gin.SetMode(gin.TestMode)
	probe := &routeProbe{}
	auth := newAdminAuthFixture(t)

	categoryHandler := handler.NewCategoryHandler(
		&stubCategoryQuery{probe},
		&stubCategoryInsert{probe},
		&stubCategoryUpdate{probe},
		&stubCategoryDelete{probe},
	)
	productHandler := handler.NewProductHandler(
		&stubProductInsert{probe},
		&stubProductQuery{probe},
		&stubProductUpdate{probe},
		&stubProductDelete{probe},
		&stubProductOption{probe},
		&stubProductVariant{probe},
		&stubProductMedia{probe},
	)
	reviewHandler := handler.NewReviewHandler(
		&stubReviewInsert{probe},
		&stubReviewQuery{probe},
		&stubReviewUpdate{probe},
		&stubReviewDelete{probe},
		&stubReviewSummary{probe},
	)
	inventoryHandler := handler.NewInventoryHandler(&stubReservation{probe})

	appRouter := NewAppRouter(gin.New())
	engine := appRouter.SetupRouter(
		categoryHandler,
		productHandler,
		nil,
		reviewHandler,
		inventoryHandler,
		auth.verifier,
		routeTestCustomerSecret,
		nil,
	)

	return &routeTestApp{engine: engine, probe: probe, auth: auth}
}

// do issues a request with optional headers and body.
func (app *routeTestApp) do(t *testing.T, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	app.engine.ServeHTTP(recorder, request)
	return recorder
}

// adminHeaders builds the bearer header for an Auth0 token carrying permissions.
func (app *routeTestApp) adminHeaders(t *testing.T, permissions []string) map[string]string {
	t.Helper()
	return map[string]string{"Authorization": "Bearer " + app.auth.token(t, permissions)}
}

// ---------------------------------------------------------------------------
// Route access policy (mirrors contracts/admin-authorization.md)
// ---------------------------------------------------------------------------

// routePolicy is one registered business method/path and its access policy.
type routePolicy struct {
	method string
	path   string
	// permission is the exact grant an admin route requires. It is empty for
	// public and customer-authenticated routes.
	permission string
}

// adminRoutePolicies is the 34-route Auth0-protected management policy.
var adminRoutePolicies = []routePolicy{
	{http.MethodPost, "/api/v1/categories", "categories:create"},
	{http.MethodPut, "/api/v1/categories/:id", "categories:update"},
	{http.MethodDelete, "/api/v1/categories/:id", "categories:delete"},
	{http.MethodPost, "/api/v1/products", "products:create"},
	{http.MethodPatch, "/api/v1/products/:id", "products:update"},
	{http.MethodDelete, "/api/v1/products/:id", "products:delete"},
	{http.MethodPost, "/api/v1/products/:id/restore", "products:restore"},
	{http.MethodDelete, "/api/v1/products/:id/purge", "products:purge"},
	{http.MethodPost, "/api/v1/products/:id/options", "products:options:create"},
	{http.MethodPatch, "/api/v1/products/:id/options/reorder", "products:options:update"},
	{http.MethodPatch, "/api/v1/products/:id/options/:option_id", "products:options:update"},
	{http.MethodDelete, "/api/v1/products/:id/options/:option_id", "products:options:delete"},
	{http.MethodPost, "/api/v1/products/:id/options/:option_id/values", "products:options:create"},
	{http.MethodPatch, "/api/v1/products/:id/options/:option_id/values/:value_id", "products:options:update"},
	{http.MethodDelete, "/api/v1/products/:id/options/:option_id/values/:value_id", "products:options:delete"},
	{http.MethodPost, "/api/v1/products/:id/variants", "products:variants:create"},
	{http.MethodPost, "/api/v1/products/:id/variants/bulk", "products:variants:create"},
	{http.MethodPatch, "/api/v1/products/:id/variants/bulk", "products:variants:update"},
	{http.MethodPost, "/api/v1/products/:id/variants/bulk-delete", "products:variants:delete"},
	{http.MethodPatch, "/api/v1/products/:id/variants/reorder", "products:variants:update"},
	{http.MethodPost, "/api/v1/products/:id/media", "products:media:create"},
	{http.MethodPatch, "/api/v1/products/:id/media/reorder", "products:media:update"},
	{http.MethodPatch, "/api/v1/products/:id/media/:media_id", "products:media:update"},
	{http.MethodDelete, "/api/v1/products/:id/media/:media_id", "products:media:delete"},
	{http.MethodGet, "/api/v1/products/:id/reviews", "products:reviews:read"},
	{http.MethodGet, "/api/v1/products/:id/reviews/summary", "products:reviews:read"},
	{http.MethodGet, "/api/v1/reviews/:reviewId", "reviews:read"},
	{http.MethodPatch, "/api/v1/variants/:id", "variants:update"},
	{http.MethodDelete, "/api/v1/variants/:id", "variants:delete"},
	{http.MethodPost, "/api/v1/variants/:id/restore", "variants:restore"},
	{http.MethodPost, "/api/v1/variants/:id/media", "variants:media:create"},
	{http.MethodPatch, "/api/v1/variants/:id/media/reorder", "variants:media:update"},
	{http.MethodDelete, "/api/v1/variants/:id/media/:media_id", "variants:media:delete"},
	{http.MethodPost, "/api/v1/inventory/reservations", "inventory:reservations:create"},
}

// publicCatalogRoutePolicies is the five GET routes that stay public and never
// validate an Auth0 token.
var publicCatalogRoutePolicies = []routePolicy{
	{http.MethodGet, "/api/v1/categories", ""},
	{http.MethodGet, "/api/v1/categories/dropdown", ""},
	{http.MethodGet, "/api/v1/categories/:id", ""},
	{http.MethodGet, "/api/v1/products", ""},
	{http.MethodGet, "/api/v1/products/:id", ""},
}

// customerReviewRoutePolicies is the four routes that keep the existing HS256
// customer identity and ownership rules.
var customerReviewRoutePolicies = []routePolicy{
	{http.MethodPost, "/api/v1/products/:id/reviews", ""},
	{http.MethodPatch, "/api/v1/reviews/:reviewId", ""},
	{http.MethodDelete, "/api/v1/reviews/:reviewId", ""},
	{http.MethodGet, "/api/v1/users/me/reviews", ""},
}

// allRoutePolicies returns every planned business route policy.
func allRoutePolicies() []routePolicy {
	policies := make([]routePolicy, 0, len(adminRoutePolicies)+len(publicCatalogRoutePolicies)+len(customerReviewRoutePolicies))
	policies = append(policies, adminRoutePolicies...)
	policies = append(policies, publicCatalogRoutePolicies...)
	policies = append(policies, customerReviewRoutePolicies...)
	return policies
}

// policyKey identifies one registered route.
func policyKey(method, path string) string { return method + " " + path }

// materializePath substitutes concrete values for the Gin route parameters in a
// registered route template so a request can be issued against it.
func materializePath(path string) string {
	replacer := strings.NewReplacer(
		":id", routeTestProductID.String(),
		":option_id", routeTestOptionID.String(),
		":value_id", routeTestValueID.String(),
		":media_id", routeTestMediaID.String(),
		":reviewId", routeTestReviewID.String(),
	)
	return replacer.Replace(path)
}

// Concrete identifiers used to exercise parameterized routes.
var (
	routeTestProductID = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	routeTestOptionID  = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	routeTestValueID   = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	routeTestMediaID   = uuid.MustParse("55555555-5555-4555-8555-555555555555")
	routeTestReviewID  = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	routeTestUserID    = uuid.MustParse("77777777-7777-4777-8777-777777777777")
)
