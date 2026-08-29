package review

import (
	"context"

	"gin-product-service/internal/domain"

	"github.com/google/uuid"
)

type fakeReviewRepo struct {
	insertErr error
	inserted  *domain.Review

	findByIDData *domain.Review
	findByIDErr  error

	findByProductData   []domain.Review
	findByProductTotal  int
	findByProductErr    error
	findByProductFilter domain.ReviewFilter

	findByUserData  []domain.ReviewWithProduct
	findByUserTotal int
	findByUserErr   error

	updateErr error
	updated   *domain.Review

	deleteErr error
	deletedID uuid.UUID

	summaryData *domain.RatingSummary
	summaryErr  error

	variantBelongs    bool
	variantBelongsErr error

	productExists    bool
	productExistsErr error
}

func (f *fakeReviewRepo) Insert(ctx context.Context, review *domain.Review) error {
	f.inserted = review
	return f.insertErr
}

func (f *fakeReviewRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Review, error) {
	if f.findByIDErr != nil {
		return nil, f.findByIDErr
	}
	if f.findByIDData != nil {
		return f.findByIDData, nil
	}
	return nil, domain.ErrReviewNotFound
}

func (f *fakeReviewRepo) FindByProduct(ctx context.Context, productID uuid.UUID, filter domain.ReviewFilter) ([]domain.Review, int, error) {
	f.findByProductFilter = filter
	return f.findByProductData, f.findByProductTotal, f.findByProductErr
}

func (f *fakeReviewRepo) FindByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]domain.ReviewWithProduct, int, error) {
	return f.findByUserData, f.findByUserTotal, f.findByUserErr
}

func (f *fakeReviewRepo) Update(ctx context.Context, review *domain.Review) error {
	f.updated = review
	return f.updateErr
}

func (f *fakeReviewRepo) Delete(ctx context.Context, id uuid.UUID) error {
	f.deletedID = id
	return f.deleteErr
}

func (f *fakeReviewRepo) GetSummary(ctx context.Context, productID uuid.UUID) (*domain.RatingSummary, error) {
	return f.summaryData, f.summaryErr
}

func (f *fakeReviewRepo) VariantBelongsToProduct(ctx context.Context, variantID, productID uuid.UUID) (bool, error) {
	return f.variantBelongs, f.variantBelongsErr
}

func (f *fakeReviewRepo) ProductExists(ctx context.Context, productID uuid.UUID) (bool, error) {
	return f.productExists, f.productExistsErr
}

var _ domain.ReviewRepository = (*fakeReviewRepo)(nil)
