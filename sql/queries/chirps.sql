-- name: CreateChirps :one
INSERT INTO chirps (id, created_at, updated_at, body, user_id)
VALUES (
    gen_random_uuid(),
    NOW(),
    NOW(),
    $1,
    $2
)
RETURNING *;

-- name: GetChirpsAscByCreateAt :many
SELECT * FROM chirps ORDER BY created_at ASC;

-- name: GetChirpsDescByCreateAt :many
SELECT * FROM chirps ORDER BY created_at DESC;

-- name: GetChirpByID :one
SELECT * FROM chirps WHERE id = $1;