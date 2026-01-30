-- name: CreateUser :one
INSERT INTO users (id, created_at, updated_at, email)
VALUES (
    gen_random_uuid(),
    NOW(),
    NOW(),
    $1
)
RETURNING *;

-- name: QueryUser :one
SELECT * FROM users WHERE email = $1;

-- name: ListUsers :many
SELECT * FROM users;

-- name: DeleteUsers :exec
DELETE FROM users;