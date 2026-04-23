package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/yourorg/myapp/internal/apperrors"
	"github.com/yourorg/myapp/internal/models"
)

func TestProductService_CreateProduct(t *testing.T) {
	tests := []struct {
		name      string
		req       *models.CreateProductRequest
		mockSetup func(*MockProductRepository)
		wantErr   bool
	}{
		{
			name: "successful creation",
			req:  &models.CreateProductRequest{Name: "test", Active: true},
			mockSetup: func(m *MockProductRepository) {
				m.EXPECT().
					Create(gomock.Any(), gomock.Any()).
					Return(&models.Product{ID: "prod_123", Name: "test", Active: true}, nil)
			},
		},
		{
			name: "repository error propagates",
			req:  &models.CreateProductRequest{Name: "test", Active: true},
			mockSetup: func(m *MockProductRepository) {
				m.EXPECT().
					Create(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("db down"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockRepo := NewMockProductRepository(ctrl)
			tt.mockSetup(mockRepo)

			svc := NewProductService(mockRepo)
			product, err := svc.CreateProduct(context.Background(), tt.req)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, product)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, product)
			assert.Equal(t, "prod_123", product.ID)
		})
	}
}

func TestProductService_UpdateProduct(t *testing.T) {
	newName := "updated"
	req := &models.UpdateProductRequest{ID: "prod_abc", Name: &newName}

	t.Run("returns NotFound when product missing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRepo := NewMockProductRepository(ctrl)

		mockRepo.EXPECT().
			GetByID(gomock.Any(), models.GetProductParams{ProductID: "prod_abc"}).
			Return(nil, apperrors.NewNotFoundError("Product", "prod_abc"))

		svc := NewProductService(mockRepo)
		_, err := svc.UpdateProduct(context.Background(), req)

		require.Error(t, err)
		var notFoundErr *apperrors.NotFoundError
		assert.ErrorAs(t, err, &notFoundErr)
	})

	t.Run("calls Update after successful GetByID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRepo := NewMockProductRepository(ctrl)

		existing := &models.Product{ID: "prod_abc", Name: "old"}
		updated := &models.Product{ID: "prod_abc", Name: "updated"}

		gomock.InOrder(
			mockRepo.EXPECT().
				GetByID(gomock.Any(), models.GetProductParams{ProductID: "prod_abc"}).
				Return(existing, nil),
			mockRepo.EXPECT().
				Update(gomock.Any(), req).
				Return(updated, nil),
		)

		svc := NewProductService(mockRepo)
		got, err := svc.UpdateProduct(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, "updated", got.Name)
	})
}
