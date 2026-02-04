package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/zzwsec/Chirpy/internal/auth"
	"github.com/zzwsec/Chirpy/internal/database"
)

var badWords = []string{"kerfuffle", "sharbert", "fornax"}

type apiConfig struct {
	fileserverHits atomic.Int32
	DB             *database.Queries
	platform       string
	secret         string
	apiKey         string
}

type User struct {
	ID           uuid.UUID `json:"id"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
	Email        string    `json:"email"`
	Token        string    `json:"token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IsChirpyRed  bool      `json:"is_chirpy_red"`
}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserID    uuid.UUID `json:"user_id"`
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	secret := os.Getenv("SECRET")
	apiKey := os.Getenv("POLKA_KEY")

	if secret == "" {
		log.Fatal("Secret is not set")
	}

	if apiKey == "" {
		log.Fatal("apiKey is not set")
	}

	if dbURL == "" {
		log.Fatal("DB_URL is not set")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Error opening database: %s", err)
	}

	if err := db.Ping(); err != nil {
		log.Fatalf("Database connection failed: %s", err)
	}

	defer db.Close()

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	dbQueries := database.New(db)

	cfg := &apiConfig{
		fileserverHits: atomic.Int32{},
		DB:             dbQueries,
		platform:       platform,
		secret:         secret,
		apiKey:         apiKey,
	}

	mainSM := http.NewServeMux()

	// App 路由：使用中间件记录点击量
	mainSM.Handle("/app/", cfg.middlewareMetricsInc(
		http.StripPrefix("/app", http.FileServer(http.Dir(".")))),
	)

	// API 路由
	apiSM := http.NewServeMux()
	apiSM.HandleFunc("GET /healthz", handlerHealth)
	apiSM.HandleFunc("POST /users", cfg.handlerUsers)
	apiSM.HandleFunc("PUT /users", cfg.handlerUsersInfo)
	apiSM.HandleFunc("POST /login", cfg.handlerLogin)
	apiSM.HandleFunc("POST /refresh", cfg.handlerRefresh)
	apiSM.HandleFunc("POST /revoke", cfg.handlerRevoke)
	apiSM.HandleFunc("POST /chirps", cfg.handlerChirps)
	apiSM.HandleFunc("GET /chirps", cfg.handlerGetChirps)
	apiSM.HandleFunc("GET /chirps/{chirpID}", cfg.handlerGetChirpByID)
	apiSM.HandleFunc("DELETE /chirps/{chirpID}", cfg.handlerDeleteChirp)
	apiSM.HandleFunc("POST /polka/webhooks", cfg.handlerSetPremium)

	// Admin 路由
	adminSM := http.NewServeMux()
	adminSM.HandleFunc("GET /metrics", cfg.handlerMetrics)
	adminSM.HandleFunc("POST /reset", cfg.handlerReset)

	// 挂载子路由
	mainSM.Handle("/api/", http.StripPrefix("/api", apiSM))
	mainSM.Handle("/admin/", http.StripPrefix("/admin", adminSM))

	server := &http.Server{
		Addr:    ":8080",
		Handler: mainSM,
	}

	log.Printf("Serving on :8080")
	log.Fatal(server.ListenAndServe())
}

// --- 中间件 ---

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`, cfg.fileserverHits.Load())
}

func (cfg *apiConfig) handlerReset(w http.ResponseWriter, r *http.Request) {
	if cfg.platform != "dev" {
		respondWithError(w, http.StatusForbidden, "Only allowed in dev environment")
		return
	}
	if err := cfg.DB.DeleteUsers(r.Context()); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong")
		return
	}
	cfg.fileserverHits.Store(0)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

func (cfg *apiConfig) handlerUsers(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	params := &parameters{}
	err := json.NewDecoder(r.Body).Decode(params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Something went wrong")
		return
	}

	// 验证邮箱格/users式
	_, err = mail.ParseAddress(params.Email)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid email format")
		return
	}

	// 密码加盐
	hashPassword, err := auth.HashPassword(params.Password)
	if err != nil {
		respondWithError(w, http.StatusBadGateway, "Backend exception")
		return
	}

	dbUser, err := cfg.DB.CreateUser(r.Context(), database.CreateUserParams{
		HashedPassword: hashPassword,
		Email:          params.Email,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong")
		return
	}

	respondWithJSON(w, http.StatusCreated, User{
		ID:          dbUser.ID,
		CreatedAt:   dbUser.CreatedAt,
		UpdatedAt:   dbUser.UpdatedAt,
		Email:       dbUser.Email,
		IsChirpyRed: dbUser.IsChirpyRed,
	})
}

func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	params := parameters{}
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Something went wrong")
		return
	}

	u, err := cfg.DB.QueryUserByEmail(r.Context(), params.Email)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Incorrect email or password")
		return
	}

	ok, err := auth.CheckPasswordHash(params.Password, u.HashedPassword)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Incorrect email or password")
		return
	}
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Incorrect email or password")
		return
	}

	expiresIn := time.Duration(time.Second * 3600)
	token, err := auth.MakeJWT(u.ID, cfg.secret, expiresIn)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong")
		return
	}

	refreshToken, err := auth.MakeRefreshToken()
	if err != nil {
		log.Printf("MakeRefreshToken error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Couldn't create refresh token")
		return
	}

	_, err = cfg.DB.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{
		Token:     refreshToken,
		UserID:    u.ID,
		ExpiresAt: time.Now().Add(time.Duration(time.Hour * 24 * 60)),
	})
	if err != nil {
		log.Printf("CreateRefreshToken failed: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Couldn't save refresh token")
		return
	}

	respondWithJSON(w, http.StatusOK, User{
		ID:           u.ID,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
		Email:        u.Email,
		Token:        token,
		RefreshToken: refreshToken,
		IsChirpyRed:  u.IsChirpyRed,
	})
}

func (cfg *apiConfig) handlerUsersInfo(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	params := parameters{}
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters")
		return
	}

	_, err = mail.ParseAddress(params.Email)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid email format")
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid header")
		return
	}

	uid, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid header")
		return
	}

	u, err := cfg.DB.QueryUserByUserID(r.Context(), uid)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user")
		return
	}

	pass, err := auth.HashPassword(params.Password)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something wrong")
		return
	}

	res, err := cfg.DB.SetUserInfo(r.Context(), database.SetUserInfoParams{
		HashedPassword: pass,
		Email:          params.Email,
		ID:             u.ID,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something wrong")
		return
	}

	respondWithJSON(w, http.StatusOK, User{
		ID:          res.ID,
		CreatedAt:   res.CreatedAt,
		UpdatedAt:   res.UpdatedAt,
		Email:       res.Email,
		IsChirpyRed: res.IsChirpyRed,
	})
}

func (cfg *apiConfig) handlerChirps(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body string `json:"body"`
	}

	type returnVals struct {
		CleanedBody string `json:"cleaned_body"`
	}

	params := parameters{}
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters")
		return
	}
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT")
		return
	}

	uid, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT")
		return
	}

	const maxChirpLength = 140
	if len(params.Body) > maxChirpLength || len(params.Body) == 0 {
		respondWithError(w, http.StatusBadRequest, "Chirp not valid")
		return
	}

	params.Body = checkWords(params.Body)

	c, err := cfg.DB.CreateChirps(r.Context(), database.CreateChirpsParams{
		Body:   params.Body,
		UserID: uid,
	})
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Something went wrong")
		return
	}

	respondWithJSON(w, http.StatusCreated, Chirp{
		ID:        c.ID,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
		Body:      c.Body,
		UserID:    c.UserID,
	})
}

func (cfg *apiConfig) handlerGetChirps(w http.ResponseWriter, r *http.Request) {
	authorID := r.URL.Query().Get("author_id")
	seq := r.URL.Query().Get("sort")
	var chirps []database.Chirp
	var err error
	var uid uuid.UUID
	switch seq {
	case "", "asc":
		seq = "asc"
	case "desc":
		seq = "desc"
	default:
		respondWithError(w, http.StatusBadRequest, "Invalid params")
		return
	}

	if authorID == "" {
		chirps, err = cfg.DB.GetChirpsAscByCreateAt(r.Context())
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Something went wrong")
			return
		}
	} else {
		uid, err = uuid.Parse(authorID)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Invalid author id")
			return
		}

		chirps, err = cfg.DB.GetChirpsAscByUserID(r.Context(), uid)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Something went wrong")
			return
		}
	}

	cs := make([]Chirp, 0, len(chirps))
	for _, chirp := range chirps {
		cs = append(cs, Chirp{
			ID:        chirp.ID,
			CreatedAt: chirp.CreatedAt,
			UpdatedAt: chirp.UpdatedAt,
			Body:      chirp.Body,
			UserID:    chirp.UserID,
		})
	}

	if seq == "desc" {
		// i, j 为切片中任意两元素下标
		// 如果返回为true,则下标i的元素应该排在下标y的元素前面
		// 如果返回为false,则下标i的元素应该排在下标y的元素后面
		sort.Slice(cs, func(i, j int) bool {
			return cs[i].CreatedAt.After(cs[j].CreatedAt)
		})
	}
	respondWithJSON(w, http.StatusOK, cs)
}

func (cfg *apiConfig) handlerRefresh(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := cfg.DB.GetRefreshToken(r.Context(), token)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	if res.RevokedAt.Valid {
		respondWithError(w, http.StatusUnauthorized, "Invalid token")
		return
	}

	if res.ExpiresAt.Before(time.Now()) {
		respondWithError(w, http.StatusUnauthorized, "Refresh token expired")
		return
	}

	accToken, err := auth.MakeJWT(res.UserID, cfg.secret, time.Duration(time.Hour))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondWithJSON(w, http.StatusOK, struct {
		Token string
	}{
		Token: accToken,
	})
}

func (cfg *apiConfig) handlerRevoke(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := cfg.DB.RevokeToken(r.Context(), token)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	rows, err := result.RowsAffected()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if rows == 0 {
		// 如果影响行数为 0，说明要么 Token 不存在，或者已经吊销过了
		respondWithError(w, http.StatusNotFound, "Token not found or already revoked")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (cfg *apiConfig) handlerGetChirpByID(w http.ResponseWriter, r *http.Request) {
	chirpID, err := uuid.Parse(r.PathValue("chirpID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Something went wrong")
		return
	}

	chirp, err := cfg.DB.GetChirpByID(r.Context(), chirpID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Something went wrong")
		return
	}

	respondWithJSON(w, http.StatusOK, Chirp{
		ID:        chirp.ID,
		CreatedAt: chirp.CreatedAt,
		UpdatedAt: chirp.UpdatedAt,
		Body:      chirp.Body,
		UserID:    chirp.UserID,
	})
}

func (cfg *apiConfig) handlerDeleteChirp(w http.ResponseWriter, r *http.Request) {
	chirpID := r.PathValue("chirpID")
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	uid, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT")
		return
	}

	cid, err := uuid.Parse(chirpID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid chirpID")
		return
	}
	c, err := cfg.DB.GetChirpByID(r.Context(), cid)
	if err != nil {
		if err == sql.ErrNoRows {
			respondWithError(w, http.StatusNotFound, "Chirp not found")
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if c.UserID != uid {
		respondWithError(w, http.StatusForbidden, "Invalid option")
		return
	}

	if err = cfg.DB.DeleteChirpByID(r.Context(), database.DeleteChirpByIDParams{
		ID:     cid,
		UserID: uid,
	}); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Service exception")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (cfg *apiConfig) handlerSetPremium(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Event string `json:"event"`
		Data  struct {
			UserID uuid.UUID `json:"user_id"`
		} `json:"data"`
	}

	apiHeader, err := auth.GetAPIKey(r.Header)
	if err != nil || apiHeader != cfg.apiKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var params parameters
	err = json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if params.Event != "user.upgraded" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	_, err = cfg.DB.SetPremiumUser(r.Context(), params.Data.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handlerHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// --- 辅助函数 (Helpers) ---

func respondWithError(w http.ResponseWriter, code int, msg string) {
	if code > 499 {
		log.Printf("Responding with 5XX error: %s", msg)
	}
	type errorResponse struct {
		Error string `json:"error"`
	}
	respondWithJSON(w, code, errorResponse{
		Error: msg,
	})
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	dat, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshalling JSON: %s", err)
		w.WriteHeader(500)
		return
	}
	w.WriteHeader(code)
	w.Write(dat)
}

func checkWords(words string) string {
	// 1. 空格分割
	splitWords := strings.Split(words, " ")

	for i, word := range splitWords {
		// 2. 转换小写进行匹配
		lowered := strings.ToLower(word)
		for _, bad := range badWords {
			if lowered == bad {
				splitWords[i] = "****"
				break
			}
		}
	}

	// 3. 空格连接
	return strings.Join(splitWords, " ")
}
