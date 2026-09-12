// Application-operation decorators live in infrastructure so tracing stays out
// of domain entities and business implementations. internal/wire wraps every
// handler-facing use case with these decorators before handlers receive them.
package telemetry

import (
	"context"
	"time"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// decoratorInstrumentation is the scope name reported for application spans.
const decoratorInstrumentation = "gin-product-service/application"

// operationFailedDescription is a constant, safe status description. Raw error
// text is never attached because it can carry SQL, credentials, or customer data.
const operationFailedDescription = "operation failed"

// DecoratorConfig carries the explicitly composed tracing dependencies for the
// application-operation decorators.
type DecoratorConfig struct {
	// TracerProvider creates application spans. A no-op provider is used when nil.
	TracerProvider trace.TracerProvider
	// EnrichContext optionally derives the context after each child span starts,
	// used to refresh the request-scoped logger's span_id.
	EnrichContext func(ctx context.Context) context.Context
}

// instrumenter starts bounded internal spans for application operations.
type instrumenter struct {
	tracer trace.Tracer
	enrich func(context.Context) context.Context
}

func newInstrumenter(cfg DecoratorConfig) instrumenter {
	provider := cfg.TracerProvider
	if provider == nil {
		provider = trace.NewNoopTracerProvider()
	}
	return instrumenter{
		tracer: provider.Tracer(decoratorInstrumentation),
		enrich: cfg.EnrichContext,
	}
}

// start begins one internal application span named with the
// app.<module>.<operation> vocabulary and refreshes the contextual logger so
// subsequent application logs carry the child span identifier.
func (i instrumenter) start(ctx context.Context, name string) (context.Context, trace.Span) {
	ctx, span := i.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
	if i.enrich != nil {
		ctx = i.enrich(ctx)
	}
	return ctx, span
}

// finish records a bounded outcome for a failing operation and always ends the
// span, including when the wrapped call is unwinding from a panic.
func finish(span trace.Span, err error) {
	if err != nil {
		span.SetStatus(codes.Error, operationFailedDescription)
	}
	span.End()
}

// Operation name vocabulary. Names and grouping attributes never contain UUIDs,
// handles, ids, or user input.
const (
	opCategoryQueryFindAll         = "app.category.query.find_all"
	opCategoryQueryFindAllDropdown = "app.category.query.find_all_for_dropdown"
	opCategoryQueryGetByID         = "app.category.query.get_by_id"
	opCategoryInsertCreate         = "app.category.insert.create"
	opCategoryUpdateUpdate         = "app.category.update.update"
	opCategoryDeleteDelete         = "app.category.delete.delete"

	opProductInsertCreate  = "app.product.insert.create"
	opProductQueryFindAll  = "app.product.query.find_all"
	opProductQueryGetByID  = "app.product.query.get_by_id"
	opProductQueryHandle   = "app.product.query.get_by_handle"
	opProductUpdateUpdate  = "app.product.update.update"
	opProductUpdateArchive = "app.product.update.archive"
	opProductUpdateRestore = "app.product.update.restore"
	opProductDeletePurge   = "app.product.delete.purge"

	opOptionCreate      = "app.product.option.create"
	opOptionRename      = "app.product.option.rename"
	opOptionDelete      = "app.product.option.delete"
	opOptionReorder     = "app.product.option.reorder"
	opOptionAddValue    = "app.product.option.add_value"
	opOptionUpdateValue = "app.product.option.update_value"
	opOptionDeleteValue = "app.product.option.delete_value"

	opVariantCreate     = "app.product.variant.create"
	opVariantBulkCreate = "app.product.variant.bulk_create"
	opVariantUpdate     = "app.product.variant.update"
	opVariantBulkUpdate = "app.product.variant.bulk_update"
	opVariantDelete     = "app.product.variant.delete"
	opVariantBulkDelete = "app.product.variant.bulk_delete"
	opVariantRestore    = "app.product.variant.restore"
	opVariantReorder    = "app.product.variant.reorder"

	opMediaCreate              = "app.product.media.create"
	opMediaUpdate              = "app.product.media.update"
	opMediaDelete              = "app.product.media.delete"
	opMediaReorder             = "app.product.media.reorder"
	opMediaAttachToVariant     = "app.product.media.attach_to_variant"
	opMediaDetachFromVariant   = "app.product.media.detach_from_variant"
	opMediaReorderVariantMedia = "app.product.media.reorder_variant_media"

	opReviewQueryListByProduct = "app.review.query.list_by_product"
	opReviewQueryGetByID       = "app.review.query.get_by_id"
	opReviewQueryListByUser    = "app.review.query.list_by_user"
	opReviewInsertCreate       = "app.review.insert.create"
	opReviewUpdateUpdate       = "app.review.update.update"
	opReviewDeleteDelete       = "app.review.delete.delete"
	opReviewSummaryGetSummary  = "app.review.summary.get_summary"

	opReservationCreate = "app.inventory.reservation.create"

	opInfraCheckerCheckDatabase = "app.infrachecker.check_database"
)

// --- category ---------------------------------------------------------------

type categoryQueryDecorator struct {
	instrumenter
	next domain.QueryCategoryUseCase
}

// NewCategoryQueryDecorator wraps the query use case with application spans.
func NewCategoryQueryDecorator(next domain.QueryCategoryUseCase, cfg DecoratorConfig) domain.QueryCategoryUseCase {
	return categoryQueryDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d categoryQueryDecorator) FindAll(ctx context.Context, params domain.ListCategoryParams) (result domain.PaginatedCategories, err error) {
	ctx, span := d.start(ctx, opCategoryQueryFindAll)
	defer func() { finish(span, err) }()
	return d.next.FindAll(ctx, params)
}

func (d categoryQueryDecorator) FindAllForDropdown(ctx context.Context, name string) (result []domain.CategoryOption, err error) {
	ctx, span := d.start(ctx, opCategoryQueryFindAllDropdown)
	defer func() { finish(span, err) }()
	return d.next.FindAllForDropdown(ctx, name)
}

func (d categoryQueryDecorator) GetByID(ctx context.Context, id int) (result domain.Category, err error) {
	ctx, span := d.start(ctx, opCategoryQueryGetByID)
	defer func() { finish(span, err) }()
	return d.next.GetByID(ctx, id)
}

type categoryInsertDecorator struct {
	instrumenter
	next domain.InsertCategoryUseCase
}

// NewCategoryInsertDecorator wraps the insert use case with application spans.
func NewCategoryInsertDecorator(next domain.InsertCategoryUseCase, cfg DecoratorConfig) domain.InsertCategoryUseCase {
	return categoryInsertDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d categoryInsertDecorator) Create(ctx context.Context, input domain.CreateCategoryInput) (result domain.Category, err error) {
	ctx, span := d.start(ctx, opCategoryInsertCreate)
	defer func() { finish(span, err) }()
	return d.next.Create(ctx, input)
}

type categoryUpdateDecorator struct {
	instrumenter
	next domain.UpdateCategoryUseCase
}

// NewCategoryUpdateDecorator wraps the update use case with application spans.
func NewCategoryUpdateDecorator(next domain.UpdateCategoryUseCase, cfg DecoratorConfig) domain.UpdateCategoryUseCase {
	return categoryUpdateDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d categoryUpdateDecorator) Update(ctx context.Context, id int, input domain.UpdateCategoryInput) (result domain.Category, err error) {
	ctx, span := d.start(ctx, opCategoryUpdateUpdate)
	defer func() { finish(span, err) }()
	return d.next.Update(ctx, id, input)
}

type categoryDeleteDecorator struct {
	instrumenter
	next domain.DeleteCategoryUseCase
}

// NewCategoryDeleteDecorator wraps the delete use case with application spans.
func NewCategoryDeleteDecorator(next domain.DeleteCategoryUseCase, cfg DecoratorConfig) domain.DeleteCategoryUseCase {
	return categoryDeleteDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d categoryDeleteDecorator) Delete(ctx context.Context, id int) (err error) {
	ctx, span := d.start(ctx, opCategoryDeleteDelete)
	defer func() { finish(span, err) }()
	return d.next.Delete(ctx, id)
}

// --- product ---------------------------------------------------------------

type productInsertDecorator struct {
	instrumenter
	next domain.InsertProductUseCase
}

// NewProductInsertDecorator wraps the product insert use case.
func NewProductInsertDecorator(next domain.InsertProductUseCase, cfg DecoratorConfig) domain.InsertProductUseCase {
	return productInsertDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d productInsertDecorator) Create(ctx context.Context, input domain.CreateProductInput) (result domain.Product, err error) {
	ctx, span := d.start(ctx, opProductInsertCreate)
	defer func() { finish(span, err) }()
	return d.next.Create(ctx, input)
}

type productQueryDecorator struct {
	instrumenter
	next domain.QueryProductUseCase
}

// NewProductQueryDecorator wraps the product query use case.
func NewProductQueryDecorator(next domain.QueryProductUseCase, cfg DecoratorConfig) domain.QueryProductUseCase {
	return productQueryDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d productQueryDecorator) FindAll(ctx context.Context, params domain.ListProductParams) (result domain.PaginatedProducts, err error) {
	ctx, span := d.start(ctx, opProductQueryFindAll)
	defer func() { finish(span, err) }()
	return d.next.FindAll(ctx, params)
}

func (d productQueryDecorator) GetByID(ctx context.Context, id uuid.UUID) (result domain.ProductDetail, err error) {
	ctx, span := d.start(ctx, opProductQueryGetByID)
	defer func() { finish(span, err) }()
	return d.next.GetByID(ctx, id)
}

func (d productQueryDecorator) GetByHandle(ctx context.Context, handle string) (result domain.ProductDetail, err error) {
	ctx, span := d.start(ctx, opProductQueryHandle)
	defer func() { finish(span, err) }()
	return d.next.GetByHandle(ctx, handle)
}

type productUpdateDecorator struct {
	instrumenter
	next domain.UpdateProductUseCase
}

// NewProductUpdateDecorator wraps the product update use case.
func NewProductUpdateDecorator(next domain.UpdateProductUseCase, cfg DecoratorConfig) domain.UpdateProductUseCase {
	return productUpdateDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d productUpdateDecorator) Update(ctx context.Context, id uuid.UUID, input domain.UpdateProductInput) (result domain.Product, err error) {
	ctx, span := d.start(ctx, opProductUpdateUpdate)
	defer func() { finish(span, err) }()
	return d.next.Update(ctx, id, input)
}

func (d productUpdateDecorator) Archive(ctx context.Context, id uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opProductUpdateArchive)
	defer func() { finish(span, err) }()
	return d.next.Archive(ctx, id)
}

func (d productUpdateDecorator) Restore(ctx context.Context, id uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opProductUpdateRestore)
	defer func() { finish(span, err) }()
	return d.next.Restore(ctx, id)
}

type productDeleteDecorator struct {
	instrumenter
	next domain.DeleteProductUseCase
}

// NewProductDeleteDecorator wraps the product delete use case.
func NewProductDeleteDecorator(next domain.DeleteProductUseCase, cfg DecoratorConfig) domain.DeleteProductUseCase {
	return productDeleteDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d productDeleteDecorator) Purge(ctx context.Context, id uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opProductDeletePurge)
	defer func() { finish(span, err) }()
	return d.next.Purge(ctx, id)
}

type optionDecorator struct {
	instrumenter
	next domain.OptionUseCase
}

// NewOptionDecorator wraps the product option use case.
func NewOptionDecorator(next domain.OptionUseCase, cfg DecoratorConfig) domain.OptionUseCase {
	return optionDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d optionDecorator) Create(ctx context.Context, productID uuid.UUID, input domain.CreateOptionInput) (result domain.ProductOption, err error) {
	ctx, span := d.start(ctx, opOptionCreate)
	defer func() { finish(span, err) }()
	return d.next.Create(ctx, productID, input)
}

func (d optionDecorator) Rename(ctx context.Context, productID, optionID uuid.UUID, input domain.UpdateOptionInput) (result domain.ProductOption, err error) {
	ctx, span := d.start(ctx, opOptionRename)
	defer func() { finish(span, err) }()
	return d.next.Rename(ctx, productID, optionID, input)
}

func (d optionDecorator) Delete(ctx context.Context, productID, optionID uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opOptionDelete)
	defer func() { finish(span, err) }()
	return d.next.Delete(ctx, productID, optionID)
}

func (d optionDecorator) Reorder(ctx context.Context, productID uuid.UUID, input domain.ReorderOptionsInput) (err error) {
	ctx, span := d.start(ctx, opOptionReorder)
	defer func() { finish(span, err) }()
	return d.next.Reorder(ctx, productID, input)
}

func (d optionDecorator) AddValue(ctx context.Context, productID, optionID uuid.UUID, input domain.CreateOptionValueInput) (result domain.ProductOptionValue, err error) {
	ctx, span := d.start(ctx, opOptionAddValue)
	defer func() { finish(span, err) }()
	return d.next.AddValue(ctx, productID, optionID, input)
}

func (d optionDecorator) UpdateValue(ctx context.Context, productID, optionID, valueID uuid.UUID, input domain.UpdateOptionValueInput) (result domain.ProductOptionValue, err error) {
	ctx, span := d.start(ctx, opOptionUpdateValue)
	defer func() { finish(span, err) }()
	return d.next.UpdateValue(ctx, productID, optionID, valueID, input)
}

func (d optionDecorator) DeleteValue(ctx context.Context, productID, optionID, valueID uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opOptionDeleteValue)
	defer func() { finish(span, err) }()
	return d.next.DeleteValue(ctx, productID, optionID, valueID)
}

type variantDecorator struct {
	instrumenter
	next domain.VariantUseCase
}

// NewVariantDecorator wraps the product variant use case.
func NewVariantDecorator(next domain.VariantUseCase, cfg DecoratorConfig) domain.VariantUseCase {
	return variantDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d variantDecorator) Create(ctx context.Context, productID uuid.UUID, input domain.CreateVariantInput) (result domain.Variant, err error) {
	ctx, span := d.start(ctx, opVariantCreate)
	defer func() { finish(span, err) }()
	return d.next.Create(ctx, productID, input)
}

func (d variantDecorator) BulkCreate(ctx context.Context, productID uuid.UUID, input domain.BulkCreateVariantsInput) (result []domain.Variant, err error) {
	ctx, span := d.start(ctx, opVariantBulkCreate)
	defer func() { finish(span, err) }()
	return d.next.BulkCreate(ctx, productID, input)
}

func (d variantDecorator) Update(ctx context.Context, variantID uuid.UUID, input domain.UpdateVariantInput) (result domain.Variant, err error) {
	ctx, span := d.start(ctx, opVariantUpdate)
	defer func() { finish(span, err) }()
	return d.next.Update(ctx, variantID, input)
}

func (d variantDecorator) BulkUpdate(ctx context.Context, productID uuid.UUID, input domain.BulkUpdateVariantsInput) (result []domain.Variant, err error) {
	ctx, span := d.start(ctx, opVariantBulkUpdate)
	defer func() { finish(span, err) }()
	return d.next.BulkUpdate(ctx, productID, input)
}

func (d variantDecorator) Delete(ctx context.Context, variantID uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opVariantDelete)
	defer func() { finish(span, err) }()
	return d.next.Delete(ctx, variantID)
}

func (d variantDecorator) BulkDelete(ctx context.Context, productID uuid.UUID, input domain.BulkDeleteVariantsInput) (err error) {
	ctx, span := d.start(ctx, opVariantBulkDelete)
	defer func() { finish(span, err) }()
	return d.next.BulkDelete(ctx, productID, input)
}

func (d variantDecorator) Restore(ctx context.Context, variantID uuid.UUID) (result domain.Variant, err error) {
	ctx, span := d.start(ctx, opVariantRestore)
	defer func() { finish(span, err) }()
	return d.next.Restore(ctx, variantID)
}

func (d variantDecorator) Reorder(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) (err error) {
	ctx, span := d.start(ctx, opVariantReorder)
	defer func() { finish(span, err) }()
	return d.next.Reorder(ctx, productID, positions)
}

type mediaDecorator struct {
	instrumenter
	next domain.MediaUseCase
}

// NewMediaDecorator wraps the product media use case.
func NewMediaDecorator(next domain.MediaUseCase, cfg DecoratorConfig) domain.MediaUseCase {
	return mediaDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d mediaDecorator) Create(ctx context.Context, productID uuid.UUID, input domain.BulkCreateMediaInput) (result []domain.ProductMedia, err error) {
	ctx, span := d.start(ctx, opMediaCreate)
	defer func() { finish(span, err) }()
	return d.next.Create(ctx, productID, input)
}

func (d mediaDecorator) Update(ctx context.Context, productID, mediaID uuid.UUID, input domain.UpdateMediaInput) (result domain.ProductMedia, err error) {
	ctx, span := d.start(ctx, opMediaUpdate)
	defer func() { finish(span, err) }()
	return d.next.Update(ctx, productID, mediaID, input)
}

func (d mediaDecorator) Delete(ctx context.Context, productID, mediaID uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opMediaDelete)
	defer func() { finish(span, err) }()
	return d.next.Delete(ctx, productID, mediaID)
}

func (d mediaDecorator) Reorder(ctx context.Context, productID uuid.UUID, positions []domain.PositionUpdate) (err error) {
	ctx, span := d.start(ctx, opMediaReorder)
	defer func() { finish(span, err) }()
	return d.next.Reorder(ctx, productID, positions)
}

func (d mediaDecorator) AttachToVariant(ctx context.Context, variantID uuid.UUID, input domain.AttachVariantMediaInput) (result domain.VariantMedia, err error) {
	ctx, span := d.start(ctx, opMediaAttachToVariant)
	defer func() { finish(span, err) }()
	return d.next.AttachToVariant(ctx, variantID, input)
}

func (d mediaDecorator) DetachFromVariant(ctx context.Context, variantID, mediaID uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opMediaDetachFromVariant)
	defer func() { finish(span, err) }()
	return d.next.DetachFromVariant(ctx, variantID, mediaID)
}

func (d mediaDecorator) ReorderVariantMedia(ctx context.Context, variantID uuid.UUID, positions []domain.PositionUpdate) (err error) {
	ctx, span := d.start(ctx, opMediaReorderVariantMedia)
	defer func() { finish(span, err) }()
	return d.next.ReorderVariantMedia(ctx, variantID, positions)
}

// --- review ----------------------------------------------------------------

type reviewQueryDecorator struct {
	instrumenter
	next domain.QueryReviewUseCase
}

// NewReviewQueryDecorator wraps the review query use case.
func NewReviewQueryDecorator(next domain.QueryReviewUseCase, cfg DecoratorConfig) domain.QueryReviewUseCase {
	return reviewQueryDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d reviewQueryDecorator) ListByProduct(ctx context.Context, productID uuid.UUID, filter domain.ReviewFilter) (result domain.PaginatedReviews, err error) {
	ctx, span := d.start(ctx, opReviewQueryListByProduct)
	defer func() { finish(span, err) }()
	return d.next.ListByProduct(ctx, productID, filter)
}

func (d reviewQueryDecorator) GetByID(ctx context.Context, id uuid.UUID) (result domain.Review, err error) {
	ctx, span := d.start(ctx, opReviewQueryGetByID)
	defer func() { finish(span, err) }()
	return d.next.GetByID(ctx, id)
}

func (d reviewQueryDecorator) ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) (result domain.PaginatedUserReviews, err error) {
	ctx, span := d.start(ctx, opReviewQueryListByUser)
	defer func() { finish(span, err) }()
	return d.next.ListByUser(ctx, userID, page, limit)
}

type reviewInsertDecorator struct {
	instrumenter
	next domain.InsertReviewUseCase
}

// NewReviewInsertDecorator wraps the review insert use case.
func NewReviewInsertDecorator(next domain.InsertReviewUseCase, cfg DecoratorConfig) domain.InsertReviewUseCase {
	return reviewInsertDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d reviewInsertDecorator) Create(ctx context.Context, productID, userID uuid.UUID, displayName string, input domain.CreateReviewInput) (result domain.Review, err error) {
	ctx, span := d.start(ctx, opReviewInsertCreate)
	defer func() { finish(span, err) }()
	return d.next.Create(ctx, productID, userID, displayName, input)
}

type reviewUpdateDecorator struct {
	instrumenter
	next domain.UpdateReviewUseCase
}

// NewReviewUpdateDecorator wraps the review update use case.
func NewReviewUpdateDecorator(next domain.UpdateReviewUseCase, cfg DecoratorConfig) domain.UpdateReviewUseCase {
	return reviewUpdateDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d reviewUpdateDecorator) Update(ctx context.Context, id, userID uuid.UUID, displayName string, input domain.UpdateReviewInput) (result domain.Review, err error) {
	ctx, span := d.start(ctx, opReviewUpdateUpdate)
	defer func() { finish(span, err) }()
	return d.next.Update(ctx, id, userID, displayName, input)
}

type reviewDeleteDecorator struct {
	instrumenter
	next domain.DeleteReviewUseCase
}

// NewReviewDeleteDecorator wraps the review delete use case.
func NewReviewDeleteDecorator(next domain.DeleteReviewUseCase, cfg DecoratorConfig) domain.DeleteReviewUseCase {
	return reviewDeleteDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d reviewDeleteDecorator) Delete(ctx context.Context, id, userID uuid.UUID) (err error) {
	ctx, span := d.start(ctx, opReviewDeleteDelete)
	defer func() { finish(span, err) }()
	return d.next.Delete(ctx, id, userID)
}

type reviewSummaryDecorator struct {
	instrumenter
	next domain.SummaryReviewUseCase
}

// NewReviewSummaryDecorator wraps the review summary use case.
func NewReviewSummaryDecorator(next domain.SummaryReviewUseCase, cfg DecoratorConfig) domain.SummaryReviewUseCase {
	return reviewSummaryDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d reviewSummaryDecorator) GetSummary(ctx context.Context, productID uuid.UUID) (result domain.RatingSummary, err error) {
	ctx, span := d.start(ctx, opReviewSummaryGetSummary)
	defer func() { finish(span, err) }()
	return d.next.GetSummary(ctx, productID)
}

// --- inventory -------------------------------------------------------------

type reservationDecorator struct {
	instrumenter
	next domain.CreateReservationUseCase
}

// NewReservationDecorator wraps the checkout reservation use case.
func NewReservationDecorator(next domain.CreateReservationUseCase, cfg DecoratorConfig) domain.CreateReservationUseCase {
	return reservationDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d reservationDecorator) Create(ctx context.Context, orderID uuid.UUID, expiresAt time.Time, items []domain.ReservationRequestItem) (result domain.ReservationResult, err error) {
	ctx, span := d.start(ctx, opReservationCreate)
	defer func() { finish(span, err) }()
	return d.next.Create(ctx, orderID, expiresAt, items)
}

// --- readiness -------------------------------------------------------------

type infraCheckDecorator struct {
	instrumenter
	next domain.InfraCheckUseCase
}

// NewInfraCheckDecorator wraps the readiness check use case.
func NewInfraCheckDecorator(next domain.InfraCheckUseCase, cfg DecoratorConfig) domain.InfraCheckUseCase {
	return infraCheckDecorator{instrumenter: newInstrumenter(cfg), next: next}
}

func (d infraCheckDecorator) CheckDatabase(ctx context.Context) (err error) {
	ctx, span := d.start(ctx, opInfraCheckerCheckDatabase)
	defer func() { finish(span, err) }()
	return d.next.CheckDatabase(ctx)
}

// Compile-time conformance: every decorator remains substitutable for the
// domain contract it wraps.
var (
	_ domain.QueryCategoryUseCase     = categoryQueryDecorator{}
	_ domain.InsertCategoryUseCase    = categoryInsertDecorator{}
	_ domain.UpdateCategoryUseCase    = categoryUpdateDecorator{}
	_ domain.DeleteCategoryUseCase    = categoryDeleteDecorator{}
	_ domain.InsertProductUseCase     = productInsertDecorator{}
	_ domain.QueryProductUseCase      = productQueryDecorator{}
	_ domain.UpdateProductUseCase     = productUpdateDecorator{}
	_ domain.DeleteProductUseCase     = productDeleteDecorator{}
	_ domain.OptionUseCase            = optionDecorator{}
	_ domain.VariantUseCase           = variantDecorator{}
	_ domain.MediaUseCase             = mediaDecorator{}
	_ domain.QueryReviewUseCase       = reviewQueryDecorator{}
	_ domain.InsertReviewUseCase      = reviewInsertDecorator{}
	_ domain.UpdateReviewUseCase      = reviewUpdateDecorator{}
	_ domain.DeleteReviewUseCase      = reviewDeleteDecorator{}
	_ domain.SummaryReviewUseCase     = reviewSummaryDecorator{}
	_ domain.CreateReservationUseCase = reservationDecorator{}
	_ domain.InfraCheckUseCase        = infraCheckDecorator{}
)
