package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/zzwsec/Chirpy/internal/database"
)

var badWords = []string{"kerfuffle", "sharbert", "fornax"}

type apiConfig struct {
	fileserverHits atomic.Int32
	DB             *database.Queries
	platform       string
}

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
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
	}

	mainSM := http.NewServeMux()

	// App 路由：使用中间件记录点击量
	mainSM.Handle("/app/", cfg.middlewareMetricsInc(
		http.StripPrefix("/app", http.FileServer(http.Dir(".")))),
	)

	// API 路由
	apiSM := http.NewServeMux()
	apiSM.HandleFunc("GET /healthz", handlerHealth)
	// apiSM.HandleFunc("POST /validate_chirp", cfg.handlerValidateChirp)
	apiSM.HandleFunc("POST /users", cfg.handlerUsers)
	apiSM.HandleFunc("POST /chirps", cfg.handlerChirps)
	apiSM.HandleFunc("GET /chirps", cfg.handlerGetChirps)

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
	type email struct {
		Email string `json:"email"`
	}
	e := &email{}
	err := json.NewDecoder(r.Body).Decode(e)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Something went wrong")
		return
	}

	// 验证邮箱格式
	_, err = mail.ParseAddress(e.Email)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid email format")
		return
	}

	dbUser, err := cfg.DB.CreateUser(r.Context(), e.Email)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong")
		return
	}

	respondWithJSON(w, http.StatusCreated, User{
		ID:        dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
	})
}

func (cfg *apiConfig) handlerChirps(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body   string    `json:"body"`
		UserID uuid.UUID `json:"user_id"`
	}

	type returnVals struct {
		CleanedBody string `json:"cleaned_body"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Something went wrong")
		return
	}

	const maxChirpLength = 140
	if len(params.Body) > maxChirpLength || len(params.Body) == 0 {
		respondWithError(w, http.StatusBadRequest, "Chirp not valid")
		return
	}

	params.Body = checkWords(params.Body)

	// 外键约束，可以不用查询，直接插入
	// _, err = cfg.DB.QueryUserByUserID(r.Context(), params.UserID)
	// if err != nil {
	// 	respondWithError(w, http.StatusBadRequest, "User not find")
	// 	return
	// }

	c, err := cfg.DB.CreateChirps(r.Context(), database.CreateChirpsParams{
		Body:   params.Body,
		UserID: params.UserID,
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
	chirps, err := cfg.DB.GetChirpsAscByCreateAt(r.Context())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Something went wrong")
		return
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
	respondWithJSON(w, http.StatusOK, cs)
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
