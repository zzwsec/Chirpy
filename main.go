package main

import (
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
)

type apiConfig struct {
	fileserverHits atomic.Int32
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Hits: %d", cfg.fileserverHits.Load())))
}

func (cfg *apiConfig) handlerReset(w http.ResponseWriter, r *http.Request) {
	cfg.fileserverHits.Store(0)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

func handlerHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func main() {
	cfg := &apiConfig{fileserverHits: atomic.Int32{}}
	mainSM := http.NewServeMux()
	mainSM.Handle("/app/", cfg.middlewareMetricsInc(
		http.StripPrefix("/app", http.FileServer(http.Dir(".")))),
	)

	apiSM := http.NewServeMux()
	apiSM.HandleFunc("GET /healthz", handlerHealth)
	apiSM.HandleFunc("GET /metrics", cfg.handlerMetrics)
	apiSM.HandleFunc("POST /reset", cfg.handlerReset)

	mainSM.Handle("/api/", http.StripPrefix("/api", apiSM))

	server := &http.Server{
		Addr:    ":8080",
		Handler: mainSM,
	}
	log.Fatal(server.ListenAndServe())
}
