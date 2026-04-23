package api

import (
	"context"

	"github.com/yourorg/myapp/internal/models"
)

//go:generate mockgen -source=interfaces.go -destination=mock_interfaces_test.go -package=api

type ProductService interface {
	CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
	GetProduct(ctx context.Context, params models.GetProductParams) (*models.Product, error)
	UpdateProduct(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error)
	DeleteProduct(ctx context.Context, params models.DeleteProductParams) error
	ListProducts(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error)
}
