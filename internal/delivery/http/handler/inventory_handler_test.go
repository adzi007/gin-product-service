package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeCreateReservationUC struct {
	result     domain.ReservationResult
	err        error
	gotOrderID uuid.UUID
	gotItems   []domain.ReservationRequestItem
}

func (f *fakeCreateReservationUC) Create(_ context.Context, orderID uuid.UUID, items []domain.ReservationRequestItem) (domain.ReservationResult, error) {
	f.gotOrderID = orderID
	f.gotItems = items
	return f.result, f.err
}

func performReservationRequest(t *testing.T, uc domain.CreateReservationUseCase, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewInventoryHandler(uc)
	r.POST("/api/v1/inventory/reservations", h.CreateReservation)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/reservations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInventoryHandler_CreateReservation_201(t *testing.T) {
	orderID := uuid.New()
	variantID := uuid.New()
	reservationID := uuid.New()
	expires := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	uc := &fakeCreateReservationUC{result: domain.ReservationResult{
		OrderID: orderID,
		Items: []domain.ReservationItemResult{
			{ReservationID: reservationID, VariantID: variantID, Quantity: 2, Status: domain.ReservationActive, ExpiresAt: expires},
		},
	}}

	body := fmt.Sprintf(`{"orderId":%q,"items":[{"id":%q,"qty":2}]}`, orderID.String(), variantID.String())
	w := performReservationRequest(t, uc, body)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			OrderID string `json:"orderId"`
			Items   []struct {
				ReservationID string `json:"reservationId"`
				VariantID     string `json:"variantId"`
				Qty           int    `json:"qty"`
				Status        string `json:"status"`
				ExpiresAt     string `json:"expiresAt"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "success" {
		t.Errorf("status = %q, want success", resp.Status)
	}
	if resp.Data.OrderID != orderID.String() {
		t.Errorf("orderId = %q, want %q", resp.Data.OrderID, orderID.String())
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Data.Items))
	}
	item := resp.Data.Items[0]
	if item.ReservationID != reservationID.String() || item.VariantID != variantID.String() ||
		item.Qty != 2 || item.Status != "ACTIVE" || item.ExpiresAt != expires.Format("2006-01-02T15:04:05Z07:00") {
		t.Errorf("item mismatch: %+v", item)
	}
}

func TestInventoryHandler_CreateReservation_200Retry(t *testing.T) {
	orderID := uuid.New()
	variantID := uuid.New()
	uc := &fakeCreateReservationUC{result: domain.ReservationResult{
		OrderID: orderID,
		Retried: true,
		Items: []domain.ReservationItemResult{
			{ReservationID: uuid.New(), VariantID: variantID, Quantity: 1, Status: domain.ReservationActive, ExpiresAt: time.Now().Add(time.Hour)},
		},
	}}

	body := fmt.Sprintf(`{"orderId":%q,"items":[{"id":%q,"qty":1}]}`, orderID.String(), variantID.String())
	w := performReservationRequest(t, uc, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestInventoryHandler_422InsuffientStockIdentifiesOnlyVariant(t *testing.T) {
	orderID := uuid.New()
	variantID := uuid.New()
	uc := &fakeCreateReservationUC{err: &domain.VariantError{
		VariantID: variantID,
		Err:       domain.ErrInsufficientStock,
	}}

	body := fmt.Sprintf(`{"orderId":%q,"items":[{"id":%q,"qty":99}]}`, orderID.String(), variantID.String())
	w := performReservationRequest(t, uc, body)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	if !strings.Contains(raw, "ERR_INSUFFICIENT_STOCK") {
		t.Errorf("body missing ERR_INSUFFICIENT_STOCK: %s", raw)
	}
	if !strings.Contains(raw, variantID.String()) {
		t.Errorf("body missing public variantId: %s", raw)
	}
	// Internal identifiers must not leak.
	if strings.Contains(raw, "inventory_item") || strings.Contains(raw, "location") {
		t.Errorf("body leaked internal identifiers: %s", raw)
	}
}

func TestInventoryHandler_503CoordinationWithoutSecrets(t *testing.T) {
	orderID := uuid.New()
	variantID := uuid.New()
	uc := &fakeCreateReservationUC{err: domain.ErrReservationCoordinationUnavailable}

	body := fmt.Sprintf(`{"orderId":%q,"items":[{"id":%q,"qty":1}]}`, orderID.String(), variantID.String())
	w := performReservationRequest(t, uc, body)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	if !strings.Contains(raw, "ERR_COORDINATION_UNAVAILABLE") {
		t.Errorf("body missing ERR_COORDINATION_UNAVAILABLE: %s", raw)
	}
	if strings.Contains(raw, "REDIS") || strings.Contains(raw, "Bearer") || strings.Contains(raw, "upstash") {
		t.Errorf("body leaked coordination internals: %s", raw)
	}
}

func TestInventoryHandler_400MalformedOrderID(t *testing.T) {
	uc := &fakeCreateReservationUC{}
	body := `{"orderId":"not-a-uuid","items":[{"id":"00000000-0000-4000-8000-000000000000","qty":1}]}`
	w := performReservationRequest(t, uc, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ERR_VALIDATION") {
		t.Errorf("body missing ERR_VALIDATION: %s", w.Body.String())
	}
}

func TestMapReservationError_StatusCodesAndDetails(t *testing.T) {
	vid := uuid.New()
	cases := []struct {
		name       string
		err        error
		status     int
		code       string
		hasVariant bool
	}{
		{"validation", domain.ErrReservationValidation, http.StatusBadRequest, "ERR_VALIDATION", false},
		{"conflict", domain.ErrReservationConflict, http.StatusConflict, "ERR_RESERVATION_CONFLICT", false},
		{"coordination", domain.ErrReservationCoordinationUnavailable, http.StatusServiceUnavailable, "ERR_COORDINATION_UNAVAILABLE", false},
		{"insufficient", &domain.VariantError{VariantID: vid, Err: domain.ErrInsufficientStock}, http.StatusUnprocessableEntity, "ERR_INSUFFICIENT_STOCK", true},
		{"variant not found", &domain.VariantError{VariantID: vid, Err: domain.ErrReservationVariantNotFound}, http.StatusNotFound, "ERR_VARIANT_NOT_FOUND", true},
		{"not reservable", &domain.VariantError{VariantID: vid, Err: domain.ErrVariantNotReservable}, http.StatusConflict, "ERR_VARIANT_NOT_RESERVABLE", true},
		{"inventory not found", &domain.VariantError{VariantID: vid, Err: domain.ErrReservationInventoryNotFound}, http.StatusNotFound, "ERR_INVENTORY_NOT_FOUND", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code, _, details := mapReservationError(tc.err)
			if status != tc.status || code != tc.code {
				t.Errorf("status/code = %d/%q, want %d/%q", status, code, tc.status, tc.code)
			}
			if tc.hasVariant {
				if got := details["variantId"]; got != vid.String() {
					t.Errorf("details.variantId = %v, want %q", got, vid.String())
				}
			} else if len(details) != 0 {
				t.Errorf("details should be empty, got %v", details)
			}
		})
	}
}
