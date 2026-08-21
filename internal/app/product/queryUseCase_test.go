package product

import (
	"context"
	"testing"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type fakeProductRepo struct {
	findAllParams domain.ListProductParams
	findAllData   []domain.Product
	findAllTotal  int
	findAllErr    error

	findByIDErr     error
	findByHandleErr error

	createParams domain.CreateProductParams
}

func (f *fakeProductRepo) Create(ctx context.Context, params domain.CreateProductParams) (domain.Product, error) {
	f.createParams = params
	return domain.Product{}, nil
}

func (f *fakeProductRepo) FindAll(ctx context.Context, params domain.ListProductParams) ([]domain.Product, int, error) {
	f.findAllParams = params
	return f.findAllData, f.findAllTotal, f.findAllErr
}

func (f *fakeProductRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	if f.findByIDErr != nil {
		return domain.Product{}, f.findByIDErr
	}
	return domain.Product{ID: id}, nil
}

func (f *fakeProductRepo) FindByHandle(ctx context.Context, handle string) (domain.Product, error) {
	if f.findByHandleErr != nil {
		return domain.Product{}, f.findByHandleErr
	}
	return domain.Product{Handle: handle}, nil
}

var _ domain.ProductRepository = (*fakeProductRepo)(nil)
var _ domain.QueryProductUseCase = (*queryProductUc)(nil)

func TestQueryProductUseCase_FindAll_AppliesDefaults(t *testing.T) {
	repo := &fakeProductRepo{
		findAllData:  []domain.Product{{}},
		findAllTotal: 1,
	}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.FindAll(context.Background(), domain.ListProductParams{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if repo.findAllParams.Page != 1 {
		t.Errorf("expected default Page 1, got %d", repo.findAllParams.Page)
	}
	if repo.findAllParams.PerPage != 10 {
		t.Errorf("expected default PerPage 10, got %d", repo.findAllParams.PerPage)
	}
	if repo.findAllParams.SortBy != "created_at" {
		t.Errorf("expected default SortBy created_at, got %q", repo.findAllParams.SortBy)
	}
	if repo.findAllParams.SortDir != "desc" {
		t.Errorf("expected default SortDir desc, got %q", repo.findAllParams.SortDir)
	}
}

func TestQueryProductUseCase_FindAll_ComputesTotalPages(t *testing.T) {
	repo := &fakeProductRepo{
		findAllData:  []domain.Product{{}, {}},
		findAllTotal: 21,
	}
	uc := NewProductQueryUseCase(repo)

	result, err := uc.FindAll(context.Background(), domain.ListProductParams{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.TotalPages != 3 {
		t.Errorf("expected TotalPages 3, got %d", result.TotalPages)
	}
	if len(result.Data) != 2 {
		t.Errorf("expected 2 data items, got %d", len(result.Data))
	}
}

func TestQueryProductUseCase_GetByID_NotFound(t *testing.T) {
	repo := &fakeProductRepo{findByIDErr: domain.ErrProductNotFound}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.GetByID(context.Background(), uuid.New())
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestQueryProductUseCase_GetByHandle_NotFound(t *testing.T) {
	repo := &fakeProductRepo{findByHandleErr: domain.ErrProductNotFound}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.GetByHandle(context.Background(), "does-not-exist")
	if err != domain.ErrProductNotFound {
		t.Fatalf("expected ErrProductNotFound, got %v", err)
	}
}

func TestQueryProductUseCase_GetByID_ReturnsProduct(t *testing.T) {
	id := uuid.New()
	repo := &fakeProductRepo{}
	uc := NewProductQueryUseCase(repo)

	got, err := uc.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.ID != id {
		t.Errorf("expected product ID %s, got %s", id, got.ID)
	}
}

func TestQueryProductUseCase_GetByHandle_ReturnsProduct(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductQueryUseCase(repo)

	got, err := uc.GetByHandle(context.Background(), "tshirt-basic")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.Handle != "tshirt-basic" {
		t.Errorf("expected handle tshirt-basic, got %q", got.Handle)
	}
}

func TestQueryProductUseCase_FindAll_PassesFiltersThrough(t *testing.T) {
	repo := &fakeProductRepo{findAllData: []domain.Product{{}}}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.FindAll(context.Background(), domain.ListProductParams{
		Search:     "hood",
		CategoryID: 7,
		Status:     "active",
		Page:       2,
		PerPage:    5,
		SortBy:     "category_name",
		SortDir:    "asc",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	p := repo.findAllParams
	if p.Search != "hood" {
		t.Errorf("expected Search hood, got %q", p.Search)
	}
	if p.CategoryID != 7 {
		t.Errorf("expected CategoryID 7, got %d", p.CategoryID)
	}
	if p.Status != "active" {
		t.Errorf("expected Status active, got %q", p.Status)
	}
	if p.Page != 2 {
		t.Errorf("expected Page 2, got %d", p.Page)
	}
	if p.PerPage != 5 {
		t.Errorf("expected PerPage 5, got %d", p.PerPage)
	}
	if p.SortBy != "category_name" {
		t.Errorf("expected SortBy category_name, got %q", p.SortBy)
	}
	if p.SortDir != "asc" {
		t.Errorf("expected SortDir asc, got %q", p.SortDir)
	}
}

func TestQueryProductUseCase_FindAll_RejectsInvalidStatus(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductQueryUseCase(repo)

	_, err := uc.FindAll(context.Background(), domain.ListProductParams{Status: "bogus"})
	if err != domain.ErrProductInvalidStatus {
		t.Fatalf("expected ErrProductInvalidStatus, got %v", err)
	}
}

func TestProductInsertUseCase_Create_DefaultsStatusToDraft(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductInsertUseCase(repo)

	price := decimal.NewFromInt(100)
	weight := decimal.NewFromInt(1)

	_, err := uc.Create(context.Background(), domain.CreateProductInput{
		Handle:     "test-product",
		Title:      "Test Product",
		CategoryID: 1,
		Variants: []domain.VariantInput{
			{Price: &price, Weight: &weight, Stock: 0},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.createParams.Product.Status != domain.ProductStatusDraft {
		t.Errorf("expected default status draft, got %q", repo.createParams.Product.Status)
	}
}

func TestProductInsertUseCase_Create_PassesThroughProvidedStatus(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductInsertUseCase(repo)

	price := decimal.NewFromInt(100)
	weight := decimal.NewFromInt(1)
	status := domain.ProductStatusActive

	_, err := uc.Create(context.Background(), domain.CreateProductInput{
		Handle:     "test-product",
		Title:      "Test Product",
		CategoryID: 1,
		Status:     &status,
		Variants: []domain.VariantInput{
			{Price: &price, Weight: &weight, Stock: 0},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if repo.createParams.Product.Status != domain.ProductStatusActive {
		t.Errorf("expected status active, got %q", repo.createParams.Product.Status)
	}
}

func TestProductInsertUseCase_Create_RejectsInvalidStatus(t *testing.T) {
	repo := &fakeProductRepo{}
	uc := NewProductInsertUseCase(repo)

	price := decimal.NewFromInt(100)
	weight := decimal.NewFromInt(1)
	badStatus := domain.ProductStatus("bogus")

	_, err := uc.Create(context.Background(), domain.CreateProductInput{
		Handle:     "test-product",
		Title:      "Test Product",
		CategoryID: 1,
		Status:     &badStatus,
		Variants: []domain.VariantInput{
			{Price: &price, Weight: &weight, Stock: 0},
		},
	})
	if err != domain.ErrProductInvalidStatus {
		t.Fatalf("expected ErrProductInvalidStatus, got %v", err)
	}
}
