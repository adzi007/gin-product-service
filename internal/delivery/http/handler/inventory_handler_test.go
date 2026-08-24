package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gin-product-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// mockStockMoveUseCase is a stub domain.StockMoveUseCase.
type mockStockMoveUseCase struct {
	createFn func(ctx context.Context, input domain.CreateStockMoveInput) (domain.StockMove, error)
}

func (m *mockStockMoveUseCase) Create(ctx context.Context, input domain.CreateStockMoveInput) (domain.StockMove, error) {
	return m.createFn(ctx, input)
}

// mockReservationUseCase is a stub domain.ReservationUseCase.
type mockReservationUseCase struct {
	createFn   func(ctx context.Context, input domain.CreateReservationInput) (domain.ReservationResult, error)
	completeFn func(ctx context.Context, orderID uuid.UUID) (domain.ReservationResult, error)
	cancelFn   func(ctx context.Context, orderID uuid.UUID) (domain.ReservationResult, error)
}

func (m *mockReservationUseCase) Create(ctx context.Context, input domain.CreateReservationInput) (domain.ReservationResult, error) {
	return m.createFn(ctx, input)
}
func (m *mockReservationUseCase) Complete(ctx context.Context, orderID uuid.UUID) (domain.ReservationResult, error) {
	return m.completeFn(ctx, orderID)
}
func (m *mockReservationUseCase) Cancel(ctx context.Context, orderID uuid.UUID) (domain.ReservationResult, error) {
	return m.cancelFn(ctx, orderID)
}

// newInventoryTestRouter builds a gin engine with only the inventory routes.
func newInventoryTestRouter(stockUC domain.StockMoveUseCase, resUC domain.ReservationUseCase) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewInventoryHandler(stockUC, resUC)
	inv := r.Group("/api/v1/inventory")
	{
		inv.POST("/stock-moves", h.CreateStockMove)
		inv.POST("/reservations", h.CreateReservation)
		inv.PUT("/reservations/:orderId/complete", h.CompleteReservation)
		inv.PUT("/reservations/:orderId/cancel", h.CancelReservation)
	}
	return r
}

func doRequest(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Status string         `json:"status"`
		Data   map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	return payload.Data
}

func TestCreateStockMove_Success(t *testing.T) {
	variantID, loc := uuid.New(), uuid.New()
	moveID := uuid.New()
	stockUC := &mockStockMoveUseCase{
		createFn: func(ctx context.Context, input domain.CreateStockMoveInput) (domain.StockMove, error) {
			return domain.StockMove{
				ID:              moveID,
				InventoryItemID: uuid.New(),
				ToLocationID:    &loc,
				MoveType:        domain.StockMoveIn,
				Quantity:        domain.Quantity(5),
				CreatedAt:       time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
			}, nil
		},
	}
	r := newInventoryTestRouter(stockUC, nil)

	body := `{"variant_id":"` + variantID.String() + `","move_type":"IN","quantity":5,"to_location_id":"` + loc.String() + `"}`
	w := doRequest(t, r, http.MethodPost, "/api/v1/inventory/stock-moves", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	data := decodeData(t, w)
	if data["move_id"] != moveID.String() {
		t.Fatalf("move_id = %v, want %s", data["move_id"], moveID.String())
	}
	if data["move_type"] != "IN" || data["quantity"] != float64(5) {
		t.Fatalf("unexpected move data: %v", data)
	}
}

func TestCreateStockMove_ValidationError(t *testing.T) {
	stockUC := &mockStockMoveUseCase{}
	r := newInventoryTestRouter(stockUC, nil)

	// Missing variant_id, move_type IN, quantity 0.
	w := doRequest(t, r, http.MethodPost, "/api/v1/inventory/stock-moves",
		`{"move_type":"IN","quantity":0,"to_location_id":"`+uuid.New().String()+`"}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateStockMove_ConflictInsufficientStock(t *testing.T) {
	stockUC := &mockStockMoveUseCase{
		createFn: func(ctx context.Context, input domain.CreateStockMoveInput) (domain.StockMove, error) {
			return domain.StockMove{}, domain.ErrInsufficientStock
		},
	}
	r := newInventoryTestRouter(stockUC, nil)

	body := `{"variant_id":"` + uuid.New().String() + `","move_type":"OUT","quantity":10,"from_location_id":"` + uuid.New().String() + `"}`
	w := doRequest(t, r, http.MethodPost, "/api/v1/inventory/stock-moves", body)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateStockMove_NotFoundVariant(t *testing.T) {
	stockUC := &mockStockMoveUseCase{
		createFn: func(ctx context.Context, input domain.CreateStockMoveInput) (domain.StockMove, error) {
			return domain.StockMove{}, domain.ErrVariantNotFound
		},
	}
	r := newInventoryTestRouter(stockUC, nil)

	body := `{"variant_id":"` + uuid.New().String() + `","move_type":"IN","quantity":5,"to_location_id":"` + uuid.New().String() + `"}`
	w := doRequest(t, r, http.MethodPost, "/api/v1/inventory/stock-moves", body)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateReservation_Success(t *testing.T) {
	orderID, variantID, loc := uuid.New(), uuid.New(), uuid.New()
	reservationID := uuid.New()
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	resUC := &mockReservationUseCase{
		createFn: func(ctx context.Context, input domain.CreateReservationInput) (domain.ReservationResult, error) {
			return domain.ReservationResult{
				OrderID: orderID,
				Status:  domain.ReservationActive,
				Reservations: []domain.Reservation{
					{
						ID:         reservationID,
						VariantID:  variantID,
						LocationID: loc,
						Quantity:   domain.Quantity(2),
						ReservedAt: now,
						Status:     domain.ReservationActive,
					},
				},
			}, nil
		},
	}
	r := newInventoryTestRouter(nil, resUC)

	body := `{"order_id":"` + orderID.String() + `","items":[{"variant_id":"` + variantID.String() + `","location_id":"` + loc.String() + `","quantity":2}]}`
	w := doRequest(t, r, http.MethodPost, "/api/v1/inventory/reservations", body)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	data := decodeData(t, w)
	if data["order_id"] != orderID.String() || data["status"] != "ACTIVE" {
		t.Fatalf("unexpected result data: %v", data)
	}
	reservations, ok := data["reservations"].([]any)
	if !ok || len(reservations) != 1 {
		t.Fatalf("expected 1 reservation in response, got %v", data["reservations"])
	}
	first := reservations[0].(map[string]any)
	if first["reservation_id"] != reservationID.String() {
		t.Fatalf("reservation_id = %v, want %s", first["reservation_id"], reservationID.String())
	}
}

func TestCreateReservation_ValidationError(t *testing.T) {
	resUC := &mockReservationUseCase{}
	r := newInventoryTestRouter(nil, resUC)

	// Empty items.
	w := doRequest(t, r, http.MethodPost, "/api/v1/inventory/reservations",
		`{"order_id":"`+uuid.New().String()+`","items":[]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateReservation_ConflictInsufficient(t *testing.T) {
	resUC := &mockReservationUseCase{
		createFn: func(ctx context.Context, input domain.CreateReservationInput) (domain.ReservationResult, error) {
			return domain.ReservationResult{}, domain.ErrInsufficientStock
		},
	}
	r := newInventoryTestRouter(nil, resUC)

	body := `{"order_id":"` + uuid.New().String() + `","items":[{"variant_id":"` + uuid.New().String() + `","location_id":"` + uuid.New().String() + `","quantity":2}]}`
	w := doRequest(t, r, http.MethodPost, "/api/v1/inventory/reservations", body)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestCompleteReservation_Success(t *testing.T) {
	orderID, variantID := uuid.New(), uuid.New()
	resUC := &mockReservationUseCase{
		completeFn: func(ctx context.Context, id uuid.UUID) (domain.ReservationResult, error) {
			return domain.ReservationResult{
				OrderID: orderID,
				Status:  domain.ReservationCompleted,
				Reservations: []domain.Reservation{
					{ID: uuid.New(), VariantID: variantID, Quantity: domain.Quantity(2), Status: domain.ReservationCompleted},
				},
			}, nil
		},
	}
	r := newInventoryTestRouter(nil, resUC)

	w := doRequest(t, r, http.MethodPut, "/api/v1/inventory/reservations/"+orderID.String()+"/complete", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	data := decodeData(t, w)
	if data["status"] != "COMPLETED" {
		t.Fatalf("status = %v, want COMPLETED", data["status"])
	}
}

func TestCompleteReservation_InvalidOrderID(t *testing.T) {
	r := newInventoryTestRouter(nil, &mockReservationUseCase{})
	w := doRequest(t, r, http.MethodPut, "/api/v1/inventory/reservations/not-a-uuid/complete", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestCompleteReservation_NotFound(t *testing.T) {
	resUC := &mockReservationUseCase{
		completeFn: func(ctx context.Context, id uuid.UUID) (domain.ReservationResult, error) {
			return domain.ReservationResult{}, domain.ErrReservationNotFound
		},
	}
	r := newInventoryTestRouter(nil, resUC)

	w := doRequest(t, r, http.MethodPut, "/api/v1/inventory/reservations/"+uuid.New().String()+"/complete", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestCancelReservation_Success(t *testing.T) {
	orderID, variantID := uuid.New(), uuid.New()
	resUC := &mockReservationUseCase{
		cancelFn: func(ctx context.Context, id uuid.UUID) (domain.ReservationResult, error) {
			return domain.ReservationResult{
				OrderID: orderID,
				Status:  domain.ReservationCancelled,
				Reservations: []domain.Reservation{
					{ID: uuid.New(), VariantID: variantID, Quantity: domain.Quantity(2), Status: domain.ReservationCancelled},
				},
			}, nil
		},
	}
	r := newInventoryTestRouter(nil, resUC)

	w := doRequest(t, r, http.MethodPut, "/api/v1/inventory/reservations/"+orderID.String()+"/cancel", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	data := decodeData(t, w)
	if data["status"] != "CANCELLED" {
		t.Fatalf("status = %v, want CANCELLED", data["status"])
	}
}

func TestCancelReservation_InvalidOrderID(t *testing.T) {
	r := newInventoryTestRouter(nil, &mockReservationUseCase{})
	w := doRequest(t, r, http.MethodPut, "/api/v1/inventory/reservations/not-a-uuid/cancel", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
