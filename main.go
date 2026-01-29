package main

import (
	"log"
	"net/http"
)

func main() {
	sm := http.NewServeMux()

	// 1. 将 /app/ 映射到当前目录
	fileServer := http.FileServer(http.Dir("."))
	sm.Handle("/app/", http.StripPrefix("/app", fileServer))

	// 2. 注册接口
	sm.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: sm,
	}
	log.Fatal(server.ListenAndServe())
}
