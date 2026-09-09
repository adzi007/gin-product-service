package inventory

import (
	"bytes"
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

// fakeReservationRepo records the input it received and returns a canned result.
type fakeReservationRepo struct {
	input domain.CreateReservationInput
	err   error
	calls int
}

func (f *fakeReservationRepo) CreateReservation(_ context.Context, input domain.CreateReservationInput) (domain.ReservationResult, error) {
	f.calls++
	f.input = input
	if f.err != nil {
		return domain.ReservationResult{}, f.err
	}
	// Build a result from the generated IDs so the success path is realistic.
	items := make([]domain.ReservationItemResult, 0, len(input.Items))
	for _, it := range input.Items {
		items = append(items, domain.ReservationItemResult{
			ReservationID: it.ReservationID,
			VariantID:     it.VariantID,
			Quantity:      it.Quantity,
			Status:        domain.ReservationActive,
		})
	}
	return domain.ReservationResult{OrderID: input.OrderID, Items: items}, nil
}

// fakeReservationLocker records acquire/release calls without any Redis.
type fakeReservationLocker struct {
	acquiredKeys []string
	releaseKeys  []string
	releaseToken string
	token        string
	acquireErr   error
	releaseErr   error
}

func (f *fakeReservationLocker) Acquire(_ context.Context, keys []string) (string, error) {
	f.acquiredKeys = append([]string(nil), keys...)
	if f.acquireErr != nil {
		return "", f.acquireErr
	}
	if f.token == "" {
		f.token = "owner-token"
	}
	return f.token, nil
}

func (f *fakeReservationLocker) Release(_ context.Context, keys []string, token string) error {
	f.releaseKeys = append([]string(nil), keys...)
	f.releaseToken = token
	return f.releaseErr
}

var (
	_ domain.ReservationRepository = (*fakeReservationRepo)(nil)
	_ domain.ReservationLocker     = (*fakeReservationLocker)(nil)
)

func TestCreateReservation_Success_CanonicalizesAndDelegates(t *testing.T) {
	repo := &fakeReservationRepo{}
	locker := &fakeReservationLocker{}
	uc := NewCreateReservationUseCase(repo, locker)

	// Deliberately out of order to prove canonicalization.
	vA := uuid.New()
	vB := uuid.New()
	if bytes.Compare(vA[:], vB[:]) > 0 {
		vA, vB = vB, vA
	}
	orderID := uuid.New()
	items := []domain.ReservationRequestItem{
		{VariantID: vB, Quantity: 1},
		{VariantID: vA, Quantity: 3},
	}

	result, err := uc.Create(context.Background(), orderID, items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.calls != 1 {
		t.Fatalf("repo calls = %d, want 1", repo.calls)
	}
	if len(result.Items) != 2 {
		t.Fatalf("result items = %d, want 2", len(result.Items))
	}

	// Items must arrive sorted by variant ID.
	if repo.input.Items[0].VariantID != vA || repo.input.Items[1].VariantID != vB {
		t.Fatalf("items not canonicalized: %v", repo.input.Items)
	}
	// UUIDv7 reservation and move IDs must be generated.
	for _, it := range repo.input.Items {
		if it.ReservationID == uuid.Nil || it.StockMoveID == uuid.Nil {
			t.Fatalf("generated IDs must be non-nil: %v", it)
		}
		if it.ReservationID.Version() != 7 || it.StockMoveID.Version() != 7 {
			t.Fatalf("generated IDs must be UUIDv7: %v", it)
		}
	}

	// Lease keys must be order key first, then variant keys in sorted order.
	if len(locker.acquiredKeys) != 3 {
		t.Fatalf("acquired keys = %v, want 3", locker.acquiredKeys)
	}
	if locker.acquiredKeys[0] != reservationOrderKeyPrefix+orderID.String() {
		t.Errorf("first key = %q, want order key", locker.acquiredKeys[0])
	}
	if locker.acquiredKeys[1] != reservationVariantKeyPrefix+vA.String() ||
		locker.acquiredKeys[2] != reservationVariantKeyPrefix+vB.String() {
		t.Errorf("variant keys not in canonical order: %v", locker.acquiredKeys)
	}

	// Release must be token-checked with the same canonical keys.
	if locker.releaseToken != locker.token {
		t.Errorf("release token = %q, want owner token %q", locker.releaseToken, locker.token)
	}
	if len(locker.releaseKeys) != len(locker.acquiredKeys) {
		t.Errorf("release keys = %v, want same as acquired %v", locker.releaseKeys, locker.acquiredKeys)
	}
}

func TestCreateReservation_FingerprintIsDeterministic(t *testing.T) {
	vA := uuid.New()
	vB := uuid.New()
	orderID := uuid.New()

	run := func(items []domain.ReservationRequestItem) []byte {
		repo := &fakeReservationRepo{}
		locker := &fakeReservationLocker{}
		uc := NewCreateReservationUseCase(repo, locker)
		if _, err := uc.Create(context.Background(), orderID, items); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return repo.input.Fingerprint
	}

	fp1 := run([]domain.ReservationRequestItem{
		{VariantID: vA, Quantity: 1}, {VariantID: vB, Quantity: 2},
	})
	fp2 := run([]domain.ReservationRequestItem{
		{VariantID: vB, Quantity: 2}, {VariantID: vA, Quantity: 1},
	})
	if !bytes.Equal(fp1, fp2) {
		t.Fatalf("fingerprint must be order-independent: %x != %x", fp1, fp2)
	}

	fp3 := run([]domain.ReservationRequestItem{
		{VariantID: vA, Quantity: 1}, {VariantID: vB, Quantity: 3},
	})
	if bytes.Equal(fp1, fp3) {
		t.Fatalf("fingerprint must change when a quantity changes")
	}
}

func TestCreateReservation_RetryReturnsOriginalResult(t *testing.T) {
	orderID := uuid.New()
	item := domain.ReservationRequestItem{VariantID: uuid.New(), Quantity: 2}
	repoResult := domain.ReservationResult{
		OrderID: orderID,
		Retried: true,
		Items: []domain.ReservationItemResult{
			{ReservationID: uuid.New(), VariantID: item.VariantID, Quantity: 2, Status: domain.ReservationActive},
		},
	}
	stub := &retryRepo{result: repoResult}
	locker := &fakeReservationLocker{}
	uc := NewCreateReservationUseCase(stub, locker)

	result, err := uc.Create(context.Background(), orderID, []domain.ReservationRequestItem{item})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Retried {
		t.Fatalf("result.Retried = false, want true")
	}
	if len(result.Items) != 1 || result.Items[0].ReservationID != repoResult.Items[0].ReservationID {
		t.Fatalf("retry must return the original reservation IDs: %v", result)
	}
	if locker.acquiredKeys == nil || locker.releaseToken == "" {
		t.Fatal("lease must still be acquired and released on the retry path")
	}
}

type retryRepo struct {
	result domain.ReservationResult
}

func (r *retryRepo) CreateReservation(_ context.Context, _ domain.CreateReservationInput) (domain.ReservationResult, error) {
	return r.result, nil
}

func TestCreateReservation_AcquireFailureFailsClosed(t *testing.T) {
	repo := &fakeReservationRepo{}
	locker := &fakeReservationLocker{acquireErr: domain.ErrReservationCoordinationUnavailable}
	uc := NewCreateReservationUseCase(repo, locker)

	_, err := uc.Create(context.Background(), uuid.New(), []domain.ReservationRequestItem{
		{VariantID: uuid.New(), Quantity: 1},
	})
	if err != domain.ErrReservationCoordinationUnavailable {
		t.Fatalf("got %v, want ErrReservationCoordinationUnavailable", err)
	}
	if repo.calls != 0 {
		t.Fatalf("repo must not be called when the lease fails (fail-closed), got %d calls", repo.calls)
	}
}

func TestCreateReservation_ValidationErrorsShortCircuit(t *testing.T) {
	repo := &fakeReservationRepo{}
	locker := &fakeReservationLocker{}
	uc := NewCreateReservationUseCase(repo, locker)

	dup := uuid.New()
	cases := []struct {
		name    string
		orderID uuid.UUID
		items   []domain.ReservationRequestItem
	}{
		{"nil order", uuid.Nil, []domain.ReservationRequestItem{{VariantID: uuid.New(), Quantity: 1}}},
		{"empty items", uuid.New(), nil},
		{"duplicate variants", uuid.New(), []domain.ReservationRequestItem{
			{VariantID: dup, Quantity: 1},
			{VariantID: dup, Quantity: 1},
		}},
		{"zero quantity", uuid.New(), []domain.ReservationRequestItem{{VariantID: uuid.New(), Quantity: 0}}},
	}

	for _, tc := range cases {
		if _, err := uc.Create(context.Background(), tc.orderID, tc.items); err != domain.ErrReservationValidation {
			t.Errorf("%s: got %v, want ErrReservationValidation", tc.name, err)
		}
	}
	if repo.calls != 0 || locker.acquiredKeys != nil {
		t.Fatalf("validation failures must not touch repo or locker")
	}
}

func TestCreateReservation_ConflictPropagates(t *testing.T) {
	repo := &fakeReservationRepo{err: domain.ErrReservationConflict}
	locker := &fakeReservationLocker{}
	uc := NewCreateReservationUseCase(repo, locker)

	_, err := uc.Create(context.Background(), uuid.New(), []domain.ReservationRequestItem{
		{VariantID: uuid.New(), Quantity: 1},
	})
	if err != domain.ErrReservationConflict {
		t.Fatalf("got %v, want ErrReservationConflict", err)
	}
	if locker.releaseToken == "" {
		t.Fatal("lease must be released even on failure")
	}
}
