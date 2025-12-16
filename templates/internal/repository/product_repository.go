package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nhalm/pgxkit"
	"github.com/yourorg/myapp/internal/apperrors"
	"github.com/yourorg/myapp/internal/id"
	"github.com/yourorg/myapp/internal/models"
	"github.com/yourorg/myapp/internal/repository/generated"
)

type ProductRepository struct {
	*generated.ProductsRepository
	queries *generated.ProductsQueries
	db      *pgxkit.DB
}

func NewProductRepository(db *pgxkit.DB) *ProductRepository {
	idGen := func() string {
		return id.GenerateIDWithPrefix("prod_")
	}

	return &ProductRepository{
		ProductsRepository: generated.NewProductsRepository(db, idGen),
		queries:            generated.NewProductsQueries(db),
		db:                 db,
	}
}

func (r *ProductRepository) Create(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error) {
	metadataJSON, err := marshalToRawMessage(req.Metadata)
	if err != nil {
		return nil, err
	}

	createParams := generated.CreateProductsParams{
		Name:        req.Name,
		Description: req.Description,
		Active:      req.Active,
		Metadata:    metadataJSON,
	}

	product, err := r.ProductsRepository.Create(ctx, createParams)
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, models.GetProductParams{
		ProductID: product.Id,
	})
}

func (r *ProductRepository) GetByID(ctx context.Context, params models.GetProductParams) (*models.Product, error) {
	result, err := r.queries.GetProductByID(ctx, params.ProductID)
	if err != nil {
		return nil, translateError(err)
	}

	var metadata map[string]string
	if result.Metadata != nil && len(*result.Metadata) > 0 {
		if err := json.Unmarshal(*result.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return &models.Product{
		ID:          result.Id,
		Name:        result.Name,
		Description: result.Description,
		Active:      result.Active,
		Metadata:    metadata,
		CreatedAt:   result.CreatedAt,
		UpdatedAt:   result.UpdatedAt,
		DeletedAt:   result.DeletedAt,
	}, nil
}

func (r *ProductRepository) Update(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error) {
	metadataJSON, err := marshalToRawMessage(req.Metadata)
	if err != nil {
		return nil, err
	}

	updateParams := generated.UpdateProductsParams{
		Name:        req.Name,
		Description: req.Description,
		Active:      req.Active,
		Metadata:    metadataJSON,
	}

	if _, err := r.ProductsRepository.Update(ctx, req.ID, updateParams); err != nil {
		return nil, err
	}

	return r.GetByID(ctx, models.GetProductParams{ProductID: req.ID})
}

func (r *ProductRepository) ListWithFilters(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error) {
	var cursor *string
	if filter.StartingAfter != nil {
		cursor = filter.StartingAfter
	}

	results, nextCursor, err := r.queries.ListProductsPaginated(ctx, filter.Active, filter.Limit, cursor)
	if err != nil {
		return nil, err
	}

	products := make([]*models.Product, len(results))
	for i, result := range results {
		var metadata map[string]string
		if result.Metadata != nil && len(*result.Metadata) > 0 {
			if err := json.Unmarshal(*result.Metadata, &metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		products[i] = &models.Product{
			ID:          result.Id,
			Name:        result.Name,
			Description: result.Description,
			Active:      result.Active,
			Metadata:    metadata,
			CreatedAt:   result.CreatedAt,
			UpdatedAt:   result.UpdatedAt,
			DeletedAt:   result.DeletedAt,
		}
	}

	hasMore := nextCursor != nil
	var prevCursor *string
	if cursor != nil && len(products) > 0 {
		first := products[0].ID
		prevCursor = &first
	}

	return &models.ListProductsResult{
		Products:   products,
		HasMore:    hasMore,
		NextCursor: nextCursor,
		PrevCursor: prevCursor,
	}, nil
}

func (r *ProductRepository) Delete(ctx context.Context, params models.DeleteProductParams) error {
	return r.ProductsRepository.Delete(ctx, params.ProductID)
}

func translateError(err error) error {
	if err == nil {
		return nil
	}

	var dbErr *generated.DatabaseError
	errors.As(err, &dbErr)

	switch {
	case generated.IsNotFound(err):
		return apperrors.NewNotFoundError(dbErr.Entity, "")
	case generated.IsAlreadyExists(err):
		return apperrors.NewConflictError(dbErr.Entity, "already exists")
	case generated.IsInvalidReference(err):
		return apperrors.NewConflictError(dbErr.Entity, "referenced resource does not exist")
	case generated.IsValidationError(err):
		return apperrors.NewValidationError("", dbErr.Detail)
	case generated.IsConnectionError(err):
		return apperrors.NewServiceUnavailableError("database connection error")
	case generated.IsTimeout(err):
		return apperrors.NewTimeoutError(dbErr.Operation)
	case generated.IsDatabaseError(err):
		return apperrors.NewServiceUnavailableError("database error")
	default:
		return apperrors.NewServiceUnavailableError("unexpected error")
	}
}
