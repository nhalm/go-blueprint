package api

// CreateProductRequest represents the request body for creating a product.
// @Description Request payload for creating a product
type CreateProductRequest struct {
	Name        string            `json:"name" validate:"required,max=255"`
	Description *string           `json:"description" validate:"omitempty,max=1000"`
	Active      bool              `json:"active"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ProductResponse represents a product resource in API responses.
// @Description Product resource
type ProductResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Active      bool              `json:"active"`
	Metadata    map[string]string `json:"metadata"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

// UpdateProductRequest represents the request body for updating a product.
// @Description Request payload for updating a product
type UpdateProductRequest struct {
	Name        *string           `json:"name" validate:"omitempty,max=255"`
	Description *string           `json:"description" validate:"omitempty,max=1000"`
	Active      *bool             `json:"active"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}
