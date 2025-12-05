package models

import "time"

type Product struct {
	ID          string
	Name        string
	Description *string
	Active      bool
	Metadata    map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

type CreateProductRequest struct {
	Name        string
	Description *string
	Active      bool
	Metadata    map[string]string
}

type UpdateProductRequest struct {
	ID          string
	Name        *string
	Description *string
	Active      *bool
	Metadata    map[string]string
}

type ListProductsFilter struct {
	Active        *bool
	Limit         int
	StartingAfter *string
	EndingBefore  *string
}

type ListProductsResult struct {
	Products   []*Product
	HasMore    bool
	NextCursor *string
	PrevCursor *string
}
