package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// errFromImplementation is returned by every fake so tests can prove the
// decorator preserves the wrapped result without swallowing or wrapping it.
var errFromImplementation = errors.New("implementation error")

type decoratorCase struct {
	method   string
	wantSpan string
	invoke   func(ctx context.Context) error
}

func testDecoratorConfig(t *testing.T) (DecoratorConfig, *tracetest.SpanRecorder, *int) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	enrichCalls := 0
	cfg := DecoratorConfig{
		TracerProvider: provider,
		EnrichContext: func(ctx context.Context) context.Context {
			enrichCalls++
			return ctx
		},
	}
	return cfg, recorder, &enrichCalls
}

// runDecoratorCases asserts the span contract shared by every decorator: one
// internal child span named with the app.<module>.<operation> vocabulary,
// parented on the active context, with the wrapped result preserved.
func runDecoratorCases(t *testing.T, cfg DecoratorConfig, recorder *tracetest.SpanRecorder, cases []decoratorCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			before := len(recorder.Ended())

			parentCtx, parent := cfg.TracerProvider.Tracer("test-parent").Start(context.Background(), "parent")

			err := tc.invoke(parentCtx)
			if !errors.Is(err, errFromImplementation) {
				t.Fatalf("returned error = %v, want the implementation error preserved", err)
			}

			spans := recorder.Ended()[before:]
			parent.End()

			if len(spans) != 1 {
				t.Fatalf("recorded spans = %d, want exactly 1", len(spans))
			}
			span := spans[0]

			if got := span.Name(); got != tc.wantSpan {
				t.Fatalf("span name = %q, want %q", got, tc.wantSpan)
			}
			if got := span.SpanKind(); got != trace.SpanKindInternal {
				t.Fatalf("span kind = %v, want internal", got)
			}
			if got := span.Parent().SpanID(); got != parent.SpanContext().SpanID() {
				t.Fatalf("parent span ID = %q, want the active context parent %q", got, parent.SpanContext().SpanID())
			}
			if got := span.Parent().TraceID(); got != parent.SpanContext().TraceID() {
				t.Fatalf("parent trace ID = %q, want %q", got, parent.SpanContext().TraceID())
			}
		})
	}
}

func TestCategoryDecorators(t *testing.T) {
	cfg, recorder, enrichCalls := testDecoratorConfig(t)

	query := NewCategoryQueryDecorator(fakeCategoryQuery{}, cfg)
	insert := NewCategoryInsertDecorator(fakeCategoryInsert{}, cfg)
	update := NewCategoryUpdateDecorator(fakeCategoryUpdate{}, cfg)
	remove := NewCategoryDeleteDecorator(fakeCategoryDelete{}, cfg)

	runDecoratorCases(t, cfg, recorder, []decoratorCase{
		{"FindAll", "app.category.query.find_all", func(ctx context.Context) error {
			_, err := query.FindAll(ctx, domain.ListCategoryParams{})
			return err
		}},
		{"FindAllForDropdown", "app.category.query.find_all_for_dropdown", func(ctx context.Context) error {
			_, err := query.FindAllForDropdown(ctx, "name")
			return err
		}},
		{"GetByID", "app.category.query.get_by_id", func(ctx context.Context) error {
			_, err := query.GetByID(ctx, 1)
			return err
		}},
		{"Create", "app.category.insert.create", func(ctx context.Context) error {
			_, err := insert.Create(ctx, domain.CreateCategoryInput{})
			return err
		}},
		{"Update", "app.category.update.update", func(ctx context.Context) error {
			_, err := update.Update(ctx, 1, domain.UpdateCategoryInput{})
			return err
		}},
		{"Delete", "app.category.delete.delete", func(ctx context.Context) error {
			return remove.Delete(ctx, 1)
		}},
	})

	if *enrichCalls != 6 {
		t.Fatalf("context enrichment calls = %d, want one per decorator span", *enrichCalls)
	}
}

func TestProductDecorators(t *testing.T) {
	cfg, recorder, _ := testDecoratorConfig(t)

	insert := NewProductInsertDecorator(fakeProductInsert{}, cfg)
	query := NewProductQueryDecorator(fakeProductQuery{}, cfg)
	update := NewProductUpdateDecorator(fakeProductUpdate{}, cfg)
	remove := NewProductDeleteDecorator(fakeProductDelete{}, cfg)
	option := NewOptionDecorator(fakeOption{}, cfg)
	variant := NewVariantDecorator(fakeVariant{}, cfg)
	media := NewMediaDecorator(fakeMedia{}, cfg)

	id := uuid.New()

	runDecoratorCases(t, cfg, recorder, []decoratorCase{
		{"Insert.Create", "app.product.insert.create", func(ctx context.Context) error {
			_, err := insert.Create(ctx, domain.CreateProductInput{})
			return err
		}},
		{"Query.FindAll", "app.product.query.find_all", func(ctx context.Context) error {
			_, err := query.FindAll(ctx, domain.ListProductParams{})
			return err
		}},
		{"Query.GetByID", "app.product.query.get_by_id", func(ctx context.Context) error {
			_, err := query.GetByID(ctx, id)
			return err
		}},
		{"Query.GetByHandle", "app.product.query.get_by_handle", func(ctx context.Context) error {
			_, err := query.GetByHandle(ctx, "handle")
			return err
		}},
		{"Update.Update", "app.product.update.update", func(ctx context.Context) error {
			_, err := update.Update(ctx, id, domain.UpdateProductInput{})
			return err
		}},
		{"Update.Archive", "app.product.update.archive", func(ctx context.Context) error {
			return update.Archive(ctx, id)
		}},
		{"Update.Restore", "app.product.update.restore", func(ctx context.Context) error {
			return update.Restore(ctx, id)
		}},
		{"Delete.Purge", "app.product.delete.purge", func(ctx context.Context) error {
			return remove.Purge(ctx, id)
		}},
		{"Option.Create", "app.product.option.create", func(ctx context.Context) error {
			_, err := option.Create(ctx, id, domain.CreateOptionInput{})
			return err
		}},
		{"Option.Rename", "app.product.option.rename", func(ctx context.Context) error {
			_, err := option.Rename(ctx, id, id, domain.UpdateOptionInput{})
			return err
		}},
		{"Option.Delete", "app.product.option.delete", func(ctx context.Context) error {
			return option.Delete(ctx, id, id)
		}},
		{"Option.Reorder", "app.product.option.reorder", func(ctx context.Context) error {
			return option.Reorder(ctx, id, domain.ReorderOptionsInput{})
		}},
		{"Option.AddValue", "app.product.option.add_value", func(ctx context.Context) error {
			_, err := option.AddValue(ctx, id, id, domain.CreateOptionValueInput{})
			return err
		}},
		{"Option.UpdateValue", "app.product.option.update_value", func(ctx context.Context) error {
			_, err := option.UpdateValue(ctx, id, id, id, domain.UpdateOptionValueInput{})
			return err
		}},
		{"Option.DeleteValue", "app.product.option.delete_value", func(ctx context.Context) error {
			return option.DeleteValue(ctx, id, id, id)
		}},
		{"Variant.Create", "app.product.variant.create", func(ctx context.Context) error {
			_, err := variant.Create(ctx, id, domain.CreateVariantInput{})
			return err
		}},
		{"Variant.BulkCreate", "app.product.variant.bulk_create", func(ctx context.Context) error {
			_, err := variant.BulkCreate(ctx, id, domain.BulkCreateVariantsInput{})
			return err
		}},
		{"Variant.Update", "app.product.variant.update", func(ctx context.Context) error {
			_, err := variant.Update(ctx, id, domain.UpdateVariantInput{})
			return err
		}},
		{"Variant.BulkUpdate", "app.product.variant.bulk_update", func(ctx context.Context) error {
			_, err := variant.BulkUpdate(ctx, id, domain.BulkUpdateVariantsInput{})
			return err
		}},
		{"Variant.Delete", "app.product.variant.delete", func(ctx context.Context) error {
			return variant.Delete(ctx, id)
		}},
		{"Variant.BulkDelete", "app.product.variant.bulk_delete", func(ctx context.Context) error {
			return variant.BulkDelete(ctx, id, domain.BulkDeleteVariantsInput{})
		}},
		{"Variant.Restore", "app.product.variant.restore", func(ctx context.Context) error {
			_, err := variant.Restore(ctx, id)
			return err
		}},
		{"Variant.Reorder", "app.product.variant.reorder", func(ctx context.Context) error {
			return variant.Reorder(ctx, id, nil)
		}},
		{"Media.Create", "app.product.media.create", func(ctx context.Context) error {
			_, err := media.Create(ctx, id, domain.BulkCreateMediaInput{})
			return err
		}},
		{"Media.Update", "app.product.media.update", func(ctx context.Context) error {
			_, err := media.Update(ctx, id, id, domain.UpdateMediaInput{})
			return err
		}},
		{"Media.Delete", "app.product.media.delete", func(ctx context.Context) error {
			return media.Delete(ctx, id, id)
		}},
		{"Media.Reorder", "app.product.media.reorder", func(ctx context.Context) error {
			return media.Reorder(ctx, id, nil)
		}},
		{"Media.AttachToVariant", "app.product.media.attach_to_variant", func(ctx context.Context) error {
			_, err := media.AttachToVariant(ctx, id, domain.AttachVariantMediaInput{})
			return err
		}},
		{"Media.DetachFromVariant", "app.product.media.detach_from_variant", func(ctx context.Context) error {
			return media.DetachFromVariant(ctx, id, id)
		}},
		{"Media.ReorderVariantMedia", "app.product.media.reorder_variant_media", func(ctx context.Context) error {
			return media.ReorderVariantMedia(ctx, id, nil)
		}},
	})
}

func TestReviewAndInventoryDecorators(t *testing.T) {
	cfg, recorder, _ := testDecoratorConfig(t)

	query := NewReviewQueryDecorator(fakeReviewQuery{}, cfg)
	insert := NewReviewInsertDecorator(fakeReviewInsert{}, cfg)
	update := NewReviewUpdateDecorator(fakeReviewUpdate{}, cfg)
	remove := NewReviewDeleteDecorator(fakeReviewDelete{}, cfg)
	summary := NewReviewSummaryDecorator(fakeReviewSummary{}, cfg)
	reservation := NewReservationDecorator(fakeReservation{}, cfg)
	infra := NewInfraCheckDecorator(fakeInfraCheck{}, cfg)

	id := uuid.New()

	runDecoratorCases(t, cfg, recorder, []decoratorCase{
		{"Review.Query.ListByProduct", "app.review.query.list_by_product", func(ctx context.Context) error {
			_, err := query.ListByProduct(ctx, id, domain.ReviewFilter{})
			return err
		}},
		{"Review.Query.GetByID", "app.review.query.get_by_id", func(ctx context.Context) error {
			_, err := query.GetByID(ctx, id)
			return err
		}},
		{"Review.Query.ListByUser", "app.review.query.list_by_user", func(ctx context.Context) error {
			_, err := query.ListByUser(ctx, id, 1, 10)
			return err
		}},
		{"Review.Insert.Create", "app.review.insert.create", func(ctx context.Context) error {
			_, err := insert.Create(ctx, id, id, "name", domain.CreateReviewInput{})
			return err
		}},
		{"Review.Update.Update", "app.review.update.update", func(ctx context.Context) error {
			_, err := update.Update(ctx, id, id, "name", domain.UpdateReviewInput{})
			return err
		}},
		{"Review.Delete.Delete", "app.review.delete.delete", func(ctx context.Context) error {
			return remove.Delete(ctx, id, id)
		}},
		{"Review.Summary.GetSummary", "app.review.summary.get_summary", func(ctx context.Context) error {
			_, err := summary.GetSummary(ctx, id)
			return err
		}},
		{"Inventory.Reservation.Create", "app.inventory.reservation.create", func(ctx context.Context) error {
			_, err := reservation.Create(ctx, id, time.Now().Add(time.Hour), nil)
			return err
		}},
		{"InfraCheck.CheckDatabase", "app.infrachecker.check_database", func(ctx context.Context) error {
			return infra.CheckDatabase(ctx)
		}},
	})
}

// Value preservation: a successful result must pass through untouched.
func TestDecoratorsPreserveSuccessfulResults(t *testing.T) {
	cfg, _, _ := testDecoratorConfig(t)

	want := domain.Category{ID: 42, Name: "kept"}
	query := NewCategoryQueryDecorator(fakeCategoryQuerySuccess{category: want}, cfg)

	got, err := query.GetByID(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v, want nil", err)
	}
	if got.ID != want.ID || got.Name != want.Name {
		t.Fatalf("GetByID() = %+v, want the wrapped result %+v", got, want)
	}
}

// Interface conformance: every handler-facing use case stays substitutable.
func TestDecoratorInterfaceConformance(t *testing.T) {
	cfg := DecoratorConfig{TracerProvider: trace.NewNoopTracerProvider()}

	var (
		categoryQuery      domain.QueryCategoryUseCase     = NewCategoryQueryDecorator(fakeCategoryQuery{}, cfg)
		categoryInsert     domain.InsertCategoryUseCase    = NewCategoryInsertDecorator(fakeCategoryInsert{}, cfg)
		categoryUpdate     domain.UpdateCategoryUseCase    = NewCategoryUpdateDecorator(fakeCategoryUpdate{}, cfg)
		categoryDelete     domain.DeleteCategoryUseCase    = NewCategoryDeleteDecorator(fakeCategoryDelete{}, cfg)
		productInsert      domain.InsertProductUseCase     = NewProductInsertDecorator(fakeProductInsert{}, cfg)
		productQuery       domain.QueryProductUseCase      = NewProductQueryDecorator(fakeProductQuery{}, cfg)
		productUpdate      domain.UpdateProductUseCase     = NewProductUpdateDecorator(fakeProductUpdate{}, cfg)
		productDelete      domain.DeleteProductUseCase     = NewProductDeleteDecorator(fakeProductDelete{}, cfg)
		productOption      domain.OptionUseCase            = NewOptionDecorator(fakeOption{}, cfg)
		productVariant     domain.VariantUseCase           = NewVariantDecorator(fakeVariant{}, cfg)
		productMedia       domain.MediaUseCase             = NewMediaDecorator(fakeMedia{}, cfg)
		reviewQuery        domain.QueryReviewUseCase       = NewReviewQueryDecorator(fakeReviewQuery{}, cfg)
		reviewInsert       domain.InsertReviewUseCase      = NewReviewInsertDecorator(fakeReviewInsert{}, cfg)
		reviewUpdate       domain.UpdateReviewUseCase      = NewReviewUpdateDecorator(fakeReviewUpdate{}, cfg)
		reviewDelete       domain.DeleteReviewUseCase      = NewReviewDeleteDecorator(fakeReviewDelete{}, cfg)
		reviewSummary      domain.SummaryReviewUseCase     = NewReviewSummaryDecorator(fakeReviewSummary{}, cfg)
		inventoryReserving domain.CreateReservationUseCase = NewReservationDecorator(fakeReservation{}, cfg)
		infraChecker       domain.InfraCheckUseCase        = NewInfraCheckDecorator(fakeInfraCheck{}, cfg)
	)

	for name, value := range map[string]any{
		"category query":        categoryQuery,
		"category insert":       categoryInsert,
		"category update":       categoryUpdate,
		"category delete":       categoryDelete,
		"product insert":        productInsert,
		"product query":         productQuery,
		"product update":        productUpdate,
		"product delete":        productDelete,
		"product option":        productOption,
		"product variant":       productVariant,
		"product media":         productMedia,
		"review query":          reviewQuery,
		"review insert":         reviewInsert,
		"review update":         reviewUpdate,
		"review delete":         reviewDelete,
		"review summary":        reviewSummary,
		"inventory reservation": inventoryReserving,
		"infra checker":         infraChecker,
	} {
		if value == nil {
			t.Fatalf("%s decorator is nil", name)
		}
	}
}

// --- fakes -----------------------------------------------------------------

type fakeCategoryQuery struct{}

func (fakeCategoryQuery) FindAll(context.Context, domain.ListCategoryParams) (domain.PaginatedCategories, error) {
	return domain.PaginatedCategories{}, errFromImplementation
}

func (fakeCategoryQuery) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	return nil, errFromImplementation
}

func (fakeCategoryQuery) GetByID(context.Context, int) (domain.Category, error) {
	return domain.Category{}, errFromImplementation
}

// fakeCategoryQuerySuccess returns a concrete value to prove pass-through.
type fakeCategoryQuerySuccess struct {
	category domain.Category
}

func (fakeCategoryQuerySuccess) FindAll(context.Context, domain.ListCategoryParams) (domain.PaginatedCategories, error) {
	return domain.PaginatedCategories{}, nil
}

func (fakeCategoryQuerySuccess) FindAllForDropdown(context.Context, string) ([]domain.CategoryOption, error) {
	return nil, nil
}

func (f fakeCategoryQuerySuccess) GetByID(context.Context, int) (domain.Category, error) {
	return f.category, nil
}

type fakeCategoryInsert struct{}

func (fakeCategoryInsert) Create(context.Context, domain.CreateCategoryInput) (domain.Category, error) {
	return domain.Category{}, errFromImplementation
}

type fakeCategoryUpdate struct{}

func (fakeCategoryUpdate) Update(context.Context, int, domain.UpdateCategoryInput) (domain.Category, error) {
	return domain.Category{}, errFromImplementation
}

type fakeCategoryDelete struct{}

func (fakeCategoryDelete) Delete(context.Context, int) error { return errFromImplementation }

type fakeProductInsert struct{}

func (fakeProductInsert) Create(context.Context, domain.CreateProductInput) (domain.Product, error) {
	return domain.Product{}, errFromImplementation
}

type fakeProductQuery struct{}

func (fakeProductQuery) FindAll(context.Context, domain.ListProductParams) (domain.PaginatedProducts, error) {
	return domain.PaginatedProducts{}, errFromImplementation
}

func (fakeProductQuery) GetByID(context.Context, uuid.UUID) (domain.ProductDetail, error) {
	return domain.ProductDetail{}, errFromImplementation
}

func (fakeProductQuery) GetByHandle(context.Context, string) (domain.ProductDetail, error) {
	return domain.ProductDetail{}, errFromImplementation
}

type fakeProductUpdate struct{}

func (fakeProductUpdate) Update(context.Context, uuid.UUID, domain.UpdateProductInput) (domain.Product, error) {
	return domain.Product{}, errFromImplementation
}

func (fakeProductUpdate) Archive(context.Context, uuid.UUID) error { return errFromImplementation }

func (fakeProductUpdate) Restore(context.Context, uuid.UUID) error { return errFromImplementation }

type fakeProductDelete struct{}

func (fakeProductDelete) Purge(context.Context, uuid.UUID) error { return errFromImplementation }

type fakeOption struct{}

func (fakeOption) Create(context.Context, uuid.UUID, domain.CreateOptionInput) (domain.ProductOption, error) {
	return domain.ProductOption{}, errFromImplementation
}

func (fakeOption) Rename(context.Context, uuid.UUID, uuid.UUID, domain.UpdateOptionInput) (domain.ProductOption, error) {
	return domain.ProductOption{}, errFromImplementation
}

func (fakeOption) Delete(context.Context, uuid.UUID, uuid.UUID) error { return errFromImplementation }

func (fakeOption) Reorder(context.Context, uuid.UUID, domain.ReorderOptionsInput) error {
	return errFromImplementation
}

func (fakeOption) AddValue(context.Context, uuid.UUID, uuid.UUID, domain.CreateOptionValueInput) (domain.ProductOptionValue, error) {
	return domain.ProductOptionValue{}, errFromImplementation
}

func (fakeOption) UpdateValue(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, domain.UpdateOptionValueInput) (domain.ProductOptionValue, error) {
	return domain.ProductOptionValue{}, errFromImplementation
}

func (fakeOption) DeleteValue(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return errFromImplementation
}

type fakeVariant struct{}

func (fakeVariant) Create(context.Context, uuid.UUID, domain.CreateVariantInput) (domain.Variant, error) {
	return domain.Variant{}, errFromImplementation
}

func (fakeVariant) BulkCreate(context.Context, uuid.UUID, domain.BulkCreateVariantsInput) ([]domain.Variant, error) {
	return nil, errFromImplementation
}

func (fakeVariant) Update(context.Context, uuid.UUID, domain.UpdateVariantInput) (domain.Variant, error) {
	return domain.Variant{}, errFromImplementation
}

func (fakeVariant) BulkUpdate(context.Context, uuid.UUID, domain.BulkUpdateVariantsInput) ([]domain.Variant, error) {
	return nil, errFromImplementation
}

func (fakeVariant) Delete(context.Context, uuid.UUID) error { return errFromImplementation }

func (fakeVariant) BulkDelete(context.Context, uuid.UUID, domain.BulkDeleteVariantsInput) error {
	return errFromImplementation
}

func (fakeVariant) Restore(context.Context, uuid.UUID) (domain.Variant, error) {
	return domain.Variant{}, errFromImplementation
}

func (fakeVariant) Reorder(context.Context, uuid.UUID, []domain.PositionUpdate) error {
	return errFromImplementation
}

type fakeMedia struct{}

func (fakeMedia) Create(context.Context, uuid.UUID, domain.BulkCreateMediaInput) ([]domain.ProductMedia, error) {
	return nil, errFromImplementation
}

func (fakeMedia) Update(context.Context, uuid.UUID, uuid.UUID, domain.UpdateMediaInput) (domain.ProductMedia, error) {
	return domain.ProductMedia{}, errFromImplementation
}

func (fakeMedia) Delete(context.Context, uuid.UUID, uuid.UUID) error { return errFromImplementation }

func (fakeMedia) Reorder(context.Context, uuid.UUID, []domain.PositionUpdate) error {
	return errFromImplementation
}

func (fakeMedia) AttachToVariant(context.Context, uuid.UUID, domain.AttachVariantMediaInput) (domain.VariantMedia, error) {
	return domain.VariantMedia{}, errFromImplementation
}

func (fakeMedia) DetachFromVariant(context.Context, uuid.UUID, uuid.UUID) error {
	return errFromImplementation
}

func (fakeMedia) ReorderVariantMedia(context.Context, uuid.UUID, []domain.PositionUpdate) error {
	return errFromImplementation
}

type fakeReviewQuery struct{}

func (fakeReviewQuery) ListByProduct(context.Context, uuid.UUID, domain.ReviewFilter) (domain.PaginatedReviews, error) {
	return domain.PaginatedReviews{}, errFromImplementation
}

func (fakeReviewQuery) GetByID(context.Context, uuid.UUID) (domain.Review, error) {
	return domain.Review{}, errFromImplementation
}

func (fakeReviewQuery) ListByUser(context.Context, uuid.UUID, int, int) (domain.PaginatedUserReviews, error) {
	return domain.PaginatedUserReviews{}, errFromImplementation
}

type fakeReviewInsert struct{}

func (fakeReviewInsert) Create(context.Context, uuid.UUID, uuid.UUID, string, domain.CreateReviewInput) (domain.Review, error) {
	return domain.Review{}, errFromImplementation
}

type fakeReviewUpdate struct{}

func (fakeReviewUpdate) Update(context.Context, uuid.UUID, uuid.UUID, string, domain.UpdateReviewInput) (domain.Review, error) {
	return domain.Review{}, errFromImplementation
}

type fakeReviewDelete struct{}

func (fakeReviewDelete) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return errFromImplementation
}

type fakeReviewSummary struct{}

func (fakeReviewSummary) GetSummary(context.Context, uuid.UUID) (domain.RatingSummary, error) {
	return domain.RatingSummary{}, errFromImplementation
}

type fakeReservation struct{}

func (fakeReservation) Create(context.Context, uuid.UUID, time.Time, []domain.ReservationRequestItem) (domain.ReservationResult, error) {
	return domain.ReservationResult{}, errFromImplementation
}

type fakeInfraCheck struct{}

func (fakeInfraCheck) CheckDatabase(context.Context) error { return errFromImplementation }

// A failing application operation must be reported with a bounded, constant
// description, and must not leak the implementation error text.
func TestDecoratorsRecordBoundedFailures(t *testing.T) {
	cfg, recorder, _ := testDecoratorConfig(t)
	decorated := NewCategoryQueryDecorator(fakeCategoryQuery{}, cfg)

	parentCtx, parent := cfg.TracerProvider.Tracer("test").Start(context.Background(), "HTTP GET /api/v1/categories")
	_, err := decorated.GetByID(parentCtx, 1)
	parent.End()

	if !errors.Is(err, errFromImplementation) {
		t.Fatalf("GetByID() error = %v, want the implementation error preserved", err)
	}

	spans := recorder.Ended()
	child := findRecordedSpan(t, spans, "app.category.query.get_by_id")

	if child.Status().Code != codes.Error {
		t.Fatalf("child status = %v, want error", child.Status().Code)
	}
	if strings.Contains(child.Status().Description, errFromImplementation.Error()) {
		t.Fatalf("child status description %q leaks the raw error text", child.Status().Description)
	}
	for _, event := range child.Events() {
		if strings.Contains(event.Name, errFromImplementation.Error()) {
			t.Fatalf("child event %q leaks the raw error text", event.Name)
		}
	}

	// The recovered child failure must not mark the surrounding request as failed.
	parentSpan := findRecordedSpan(t, spans, "HTTP GET /api/v1/categories")
	if parentSpan.Status().Code == codes.Error {
		t.Fatalf("parent span was marked as an error by a recovered child failure")
	}
}

// A cancelled request must still close every started application span.
func TestDecoratorsCloseSpansOnCancellation(t *testing.T) {
	cfg, recorder, _ := testDecoratorConfig(t)
	decorated := NewCategoryQueryDecorator(fakeCategoryQuery{}, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := decorated.GetByID(ctx, 1)
	if !errors.Is(err, errFromImplementation) {
		t.Fatalf("GetByID() error = %v, want the implementation error preserved", err)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1 closed span", len(spans))
	}
	span := spans[0]

	if span.EndTime().IsZero() {
		t.Fatalf("span was not closed after cancellation")
	}
	if span.Status().Code != codes.Error {
		t.Fatalf("span status = %v, want error", span.Status().Code)
	}
}

// Slow application operations must be attributable by span duration.
func TestDecoratorsTimeSlowOperations(t *testing.T) {
	const delay = 30 * time.Millisecond

	cfg, recorder, _ := testDecoratorConfig(t)
	decorated := NewCategoryInsertDecorator(slowCategoryInsert{delay: delay}, cfg)

	_, _ = decorated.Create(context.Background(), domain.CreateCategoryInput{})

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	span := spans[0]

	if got := span.EndTime().Sub(span.StartTime()); got < delay {
		t.Fatalf("span duration = %v, want at least the slow dependency delay %v", got, delay)
	}
}

// The contextual logger must be refreshed with the child span, not the parent.
func TestDecoratorsRefreshLoggerContextForChildSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	var enriched []trace.SpanContext
	cfg := DecoratorConfig{
		TracerProvider: provider,
		EnrichContext: func(ctx context.Context) context.Context {
			enriched = append(enriched, trace.SpanContextFromContext(ctx))
			return ctx
		},
	}
	decorated := NewCategoryQueryDecorator(fakeCategoryQuery{}, cfg)

	parentCtx, parent := provider.Tracer("test").Start(context.Background(), "HTTP GET /api/v1/categories")
	_, _ = decorated.GetByID(parentCtx, 1)
	parent.End()

	if len(enriched) != 1 {
		t.Fatalf("enrichment calls = %d, want exactly one per application span", len(enriched))
	}
	if got := enriched[0].TraceID(); got != parent.SpanContext().TraceID() {
		t.Fatalf("enriched trace ID = %q, want %q", got, parent.SpanContext().TraceID())
	}
	if got := enriched[0].SpanID(); got == parent.SpanContext().SpanID() {
		t.Fatalf("enriched context still carries the parent span ID; the child span must be current")
	}
}

func findRecordedSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	t.Fatalf("span %q not found; recorded: %v", name, names)
	return nil
}

// slowCategoryInsert emulates a slow dependency.
type slowCategoryInsert struct {
	delay time.Duration
}

func (f slowCategoryInsert) Create(context.Context, domain.CreateCategoryInput) (domain.Category, error) {
	time.Sleep(f.delay)
	return domain.Category{}, errFromImplementation
}
