package main

import (
	"encoding/json"
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
	cfg.fileserverHits.Store(0)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

func (cfg *apiConfig) handlerValidateChirp(w http.ResponseWriter, r *http.Request) {
	type reqBody struct {
		Body string `json:"body"`
	}

	type respErrBody struct {
		Error string `json:"error"`
	}

	type respBody struct {
		Valid bool `json:"valid"`
	}

	var req reqBody
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		tmpResp, _ := json.Marshal(respErrBody{
			Error: "Something went wrong",
		})
		w.Write(tmpResp)
		return
	}

	if len(req.Body) > 140 || len(req.Body) == 0 {
		log.Print("Chirp is not a valid data")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		tmpResp, _ := json.Marshal(respErrBody{
			Error: "Chirp is not a valid data",
		})
		w.Write(tmpResp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	tmpResp, _ := json.Marshal(respBody{
		Valid: true,
	})
	w.Write(tmpResp)
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
	apiSM.HandleFunc("POST /validate_chirp", cfg.handlerValidateChirp)

	adminSM := http.NewServeMux()
	adminSM.HandleFunc("GET /metrics", cfg.handlerMetrics)
	adminSM.HandleFunc("POST /reset", cfg.handlerReset)

	mainSM.Handle("/api/", http.StripPrefix("/api", apiSM))
	mainSM.Handle("/admin/", http.StripPrefix("/admin", adminSM))

	server := &http.Server{
		Addr:    ":8080",
		Handler: mainSM,
	}
	log.Fatal(server.ListenAndServe())
}
