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
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	// 直接写入 w，不需要先转成字符串再转成 []byte
	fmt.Fprintf(w, `<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`, cfg.fileserverHits.Load())
}

//// 等效
// func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, r *http.Request) {
// 	resp := fmt.Sprintf(`<html>
//   <body>
//     <h1>Welcome, Chirpy Admin</h1>
//     <p>Chirpy has been visited %d times!</p>
//   </body>
// </html>`, cfg.fileserverHits.Load())
// 	w.Header().Set("Content-Type", "text/html")
// 	w.WriteHeader(http.StatusOK)
// 	w.Write([]byte(resp))
// }

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
