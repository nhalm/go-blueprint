package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nhalm/canonlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/yourorg/myapp/internal/apperrors"
	"github.com/yourorg/myapp/internal/models"
)

func setupTestHandler(t *testing.T) (*Handler, *MockProductService, http.Handler) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mockSvc := NewMockProductService(ctrl)
	handler := NewHandler(mockSvc)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := canonlog.NewContext(req.Context())
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/products", handler.ListProducts)
		r.Get("/products/{id}", handler.GetProduct)
		r.Post("/products", handler.CreateProduct)
		r.Patch("/products/{id}", handler.UpdateProduct)
		r.Delete("/products/{id}", handler.DeleteProduct)
	})

	return handler, mockSvc, r
}

func TestHandler_CreateProduct(t *testing.T) {
	tests := []struct {
		name       string
		body       any
		mockSetup  func(*MockProductService)
		wantStatus int
		wantID     string
	}{
		{
			name: "successful creation",
			body: CreateProductRequest{Name: "test", Active: true},
			mockSetup: func(m *MockProductService) {
				m.EXPECT().
					CreateProduct(gomock.Any(), gomock.Any()).
					Return(&models.Product{
						ID:        "prod_abc",
						Name:      "test",
						Active:    true,
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					}, nil)
			},
			wantStatus: http.StatusCreated,
			wantID:     "prod_abc",
		},
		{
			name:       "validation failure on missing name returns 400",
			body:       CreateProductRequest{Active: true},
			mockSetup:  func(_ *MockProductService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "service NotFound returns 404",
			body: CreateProductRequest{Name: "test", Active: true},
			mockSetup: func(m *MockProductService) {
				m.EXPECT().
					CreateProduct(gomock.Any(), gomock.Any()).
					Return(nil, apperrors.NewNotFoundError("Product", ""))
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mockSvc, router := setupTestHandler(t)
			tt.mockSetup(mockSvc)

			body, err := json.Marshal(tt.body)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/products", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			router.ServeHTTP(rr, req)
			assert.Equal(t, tt.wantStatus, rr.Code)

			if tt.wantID != "" {
				var resp ProductResponse
				require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
				assert.Equal(t, tt.wantID, resp.ID)
			}
		})
	}
}

func TestHandler_GetProduct(t *testing.T) {
	t.Run("returns 200 with product", func(t *testing.T) {
		_, mockSvc, router := setupTestHandler(t)

		mockSvc.EXPECT().
			GetProduct(gomock.Any(), models.GetProductParams{ProductID: "prod_abc"}).
			Return(&models.Product{
				ID:        "prod_abc",
				Name:      "test",
				Active:    true,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/prod_abc", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		var resp ProductResponse
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
		assert.Equal(t, "prod_abc", resp.ID)
	})

	t.Run("returns 404 when service reports NotFound", func(t *testing.T) {
		_, mockSvc, router := setupTestHandler(t)

		mockSvc.EXPECT().
			GetProduct(gomock.Any(), models.GetProductParams{ProductID: "prod_missing"}).
			Return(nil, apperrors.NewNotFoundError("Product", "prod_missing"))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/prod_missing", nil)
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)

		var errResp ErrorResponse
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&errResp))
		assert.Equal(t, "invalid_request_error", errResp.Error.Type)
	})
}
