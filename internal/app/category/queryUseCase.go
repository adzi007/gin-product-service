package category

import (
	"context"
	"gin-product-service/internal/domain"
)

type insertCategoryUc struct {
	categoryRepo domain.CategoryRepository
}

func NewCategoryQueryUseCase(categoryRepo domain.CategoryRepository) domain.QueryCategoryUseCase {
	return &insertCategoryUc{
		categoryRepo: categoryRepo,
	}
}

func (uc *insertCategoryUc) FindAll(ctx context.Context) ([]domain.Category, error) {

	data, err := uc.categoryRepo.FindAll(ctx)

	if err != nil {
		return nil, err
	}

	// return domain.Category{
	// 	Pesan: "hello world.....",
	// }, nil

	return data, nil
}
