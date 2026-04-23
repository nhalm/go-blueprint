package repository

import (
	"context"
	"testing"

	"github.com/nhalm/pgxkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yourorg/myapp/internal/apperrors"
	"github.com/yourorg/myapp/internal/models"
)

func TestProductRepository_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	db := pgxkit.RequireDB(t)
	defer db.Shutdown(ctx)

	repo := NewProductRepository(db)

	t.Run("Create and GetByID round-trip", func(t *testing.T) {
		desc := "integration test product"
		created, err := repo.Create(ctx, &models.CreateProductRequest{
			Name:        "test-product",
			Description: &desc,
			Active:      true,
		})
		require.NoError(t, err)
		require.NotNil(t, created)
		assert.NotEmpty(t, created.ID)
		assert.Equal(t, "test-product", created.Name)

		t.Cleanup(func() {
			_ = repo.Delete(ctx, models.DeleteProductParams{ProductID: created.ID})
		})

		fetched, err := repo.GetByID(ctx, models.GetProductParams{ProductID: created.ID})
		require.NoError(t, err)
		assert.Equal(t, created.ID, fetched.ID)
		assert.Equal(t, "test-product", fetched.Name)
		require.NotNil(t, fetched.Description)
		assert.Equal(t, desc, *fetched.Description)
	})

	t.Run("GetByID returns NotFoundError for unknown id", func(t *testing.T) {
		_, err := repo.GetByID(ctx, models.GetProductParams{ProductID: "prod_does_not_exist"})
		require.Error(t, err)
		var notFoundErr *apperrors.NotFoundError
		assert.ErrorAs(t, err, &notFoundErr)
	})

	t.Run("Update applies changes", func(t *testing.T) {
		created, err := repo.Create(ctx, &models.CreateProductRequest{
			Name:   "to-be-updated",
			Active: true,
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = repo.Delete(ctx, models.DeleteProductParams{ProductID: created.ID})
		})

		newName := "updated-name"
		updated, err := repo.Update(ctx, &models.UpdateProductRequest{
			ID:   created.ID,
			Name: &newName,
		})
		require.NoError(t, err)
		assert.Equal(t, "updated-name", updated.Name)
	})

	t.Run("ListWithFilters paginates", func(t *testing.T) {
		const total = 3
		ids := make([]string, 0, total)
		for i := 0; i < total; i++ {
			p, err := repo.Create(ctx, &models.CreateProductRequest{
				Name:   "list-test",
				Active: true,
			})
			require.NoError(t, err)
			ids = append(ids, p.ID)
		}
		t.Cleanup(func() {
			for _, id := range ids {
				_ = repo.Delete(ctx, models.DeleteProductParams{ProductID: id})
			}
		})

		page, err := repo.ListWithFilters(ctx, models.ListProductsFilter{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, page.Products, 2)
		assert.True(t, page.HasMore)
		require.NotNil(t, page.NextCursor)
	})
}
