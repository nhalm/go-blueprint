package service

import (
	"context"

	"github.com/yourorg/myapp/internal/models"
)

//go:generate mockgen -source=interfaces.go -destination=mock_interfaces_test.go -package=service

type ProductRepository interface {
	Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
	GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error)
	Update(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error)
	Delete(ctx context.Context, params models.DeleteProductParams) error
	ListWithFilters(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error)
}
