package service

import (
	"context"
	"errors"

	"github.com/yourorg/myapp/internal/apperrors"
	"github.com/yourorg/myapp/internal/models"
	"github.com/yourorg/myapp/internal/repository"
)

type ProductRepository interface {
	Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
	GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error)
	Update(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error)
	Delete(ctx context.Context, params models.DeleteProductParams) error
	ListWithFilters(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error)
}

type ProductService struct {
	repo ProductRepository
}

func NewProductService(repo ProductRepository) *ProductService {
	return &ProductService{repo: repo}
}

func (s *ProductService) CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error) {
	// Business validation goes here (e.g., check for duplicates, verify references)
	// Structural validation (required fields, lengths) is handled by API layer
	return s.repo.Create(ctx, req)
}

func (s *ProductService) GetProduct(ctx context.Context, params models.GetProductParams) (*models.Product, error) {
	product, err := s.repo.GetByID(ctx, params)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperrors.NewNotFoundError("product", params.ProductID)
		}
		return nil, err
	}

	return product, nil
}

func (s *ProductService) UpdateProduct(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error) {
	if _, err := s.repo.GetByID(ctx, models.GetProductParams{ProductID: req.ID}); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperrors.NewNotFoundError("product", req.ID)
		}
		return nil, err
	}

	return s.repo.Update(ctx, req)
}

func (s *ProductService) ListProducts(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error) {
	return s.repo.ListWithFilters(ctx, filter)
}

func (s *ProductService) DeleteProduct(ctx context.Context, params models.DeleteProductParams) error {
	if _, err := s.repo.GetByID(ctx, models.GetProductParams{ProductID: params.ProductID}); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperrors.NewNotFoundError("product", params.ProductID)
		}
		return err
	}

	return s.repo.Delete(ctx, params)
}
