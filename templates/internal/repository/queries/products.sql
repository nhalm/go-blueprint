-- name: GetProductByID :one
SELECT id, name, description, active, metadata, created_at, updated_at, deleted_at
FROM products
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListProducts :many
-- Parameters: $1=active filter, $2=limit, $3=starting_after cursor, $4=ending_before cursor
SELECT id, name, description, active, metadata, created_at, updated_at, deleted_at
FROM products
WHERE deleted_at IS NULL
  AND ($1::boolean IS NULL OR active = $1)
  AND ($3::text IS NULL OR id > $3)
  AND ($4::text IS NULL OR id < $4)
ORDER BY id ASC
LIMIT $2;
