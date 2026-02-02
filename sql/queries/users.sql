-- name: CreateUser :one
INSERT INTO users (id, created_at, updated_at, hashed_password, email)
VALUES (
    gen_random_uuid(),
    NOW(),
    NOW(),
    $1,
    $2
)
RETURNING *;

-- name: QueryUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: QueryUserByUserID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users;

-- name: DeleteUsers :exec
DELETE FROM users;