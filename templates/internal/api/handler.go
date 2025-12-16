package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nhalm/canonlog"
	"github.com/yourorg/myapp/internal/models"
)

// ProductService defines only the methods the API layer needs from the product service.
type ProductService interface {
	CreateProduct(ctx context.Context, req *models.CreateProductRequest) (*models.Product, error)
	GetProduct(ctx context.Context, params models.GetProductParams) (*models.Product, error)
	UpdateProduct(ctx context.Context, req *models.UpdateProductRequest) (*models.Product, error)
	DeleteProduct(ctx context.Context, params models.DeleteProductParams) error
	ListProducts(ctx context.Context, filter models.ListProductsFilter) (*models.ListProductsResult, error)
}

type Handler struct {
	productSvc ProductService
}

func NewHandler(productSvc ProductService) *Handler {
	return &Handler{
		productSvc: productSvc,
	}
}

func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	var req CreateProductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		BadRequest(w, r, err, "invalid request body", "")
		return
	}

	if err := ValidateStruct(req); err != nil {
		BadRequest(w, r, err, err.Error(), "")
		return
	}

	canonlog.AddRequestFields(r.Context(), map[string]any{
		"product_name": req.Name,
	})

	serviceReq := models.CreateProductRequest{
		Name:        req.Name,
		Description: req.Description,
		Active:      req.Active,
		Metadata:    req.Metadata,
	}

	product, err := h.productSvc.CreateProduct(r.Context(), &serviceReq)
	if err != nil {
		handleServiceError(w, r, err)
		return
	}

	Created(w, convertToProductResponse(product))
}

func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	var active *bool
	if a := r.URL.Query().Get("active"); a != "" {
		b := a == "true"
		active = &b
	}

	filter := models.ListProductsFilter{
		Active:        active,
		Limit:         limit,
		StartingAfter: ptrOrNil(r.URL.Query().Get("starting_after")),
	}

	result, err := h.productSvc.ListProducts(r.Context(), filter)
	if err != nil {
		handleServiceError(w, r, err)
		return
	}

	responses := make([]ProductResponse, len(result.Products))
	for i, p := range result.Products {
		responses[i] = convertToProductResponse(p)
	}

	var nextCursor, prevCursor string
	if result.NextCursor != nil {
		nextCursor = *result.NextCursor
	}
	if result.PrevCursor != nil {
		prevCursor = *result.PrevCursor
	}

	List(w, responses, result.HasMore, nextCursor, prevCursor)
}

func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	product, err := h.productSvc.GetProduct(r.Context(), models.GetProductParams{
		ProductID: id,
	})
	if err != nil {
		handleServiceError(w, r, err)
		return
	}

	Success(w, convertToProductResponse(product))
}

func (h *Handler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req UpdateProductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		BadRequest(w, r, err, "invalid request body", "")
		return
	}

	if err := ValidateStruct(req); err != nil {
		BadRequest(w, r, err, err.Error(), "")
		return
	}

	serviceReq := models.UpdateProductRequest{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Active:      req.Active,
		Metadata:    req.Metadata,
	}

	product, err := h.productSvc.UpdateProduct(r.Context(), &serviceReq)
	if err != nil {
		handleServiceError(w, r, err)
		return
	}

	Success(w, convertToProductResponse(product))
}

func (h *Handler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.productSvc.DeleteProduct(r.Context(), models.DeleteProductParams{
		ProductID: id,
	}); err != nil {
		handleServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func convertToProductResponse(product *models.Product) ProductResponse {
	var description string
	if product.Description != nil {
		description = *product.Description
	}

	return ProductResponse{
		ID:          product.ID,
		Name:        product.Name,
		Description: description,
		Active:      product.Active,
		Metadata:    product.Metadata,
		CreatedAt:   product.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   product.UpdatedAt.Format(time.RFC3339),
	}
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
