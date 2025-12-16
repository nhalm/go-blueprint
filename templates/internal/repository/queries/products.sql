-- name: GetProductByID :one
SELECT id, name, description, active, metadata, created_at, updated_at, deleted_at
FROM products
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListProducts :paginated
SELECT id, name, description, active, metadata, created_at, updated_at, deleted_at
FROM products
WHERE deleted_at IS NULL
  AND ($1::boolean IS NULL OR active = $1)
ORDER BY id ASC;
