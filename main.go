package main

import (
	"net/http"
)

func main() {
	sm := http.NewServeMux()
	server := http.Server{
		Addr:    ":8080",
		Handler: sm,
	}
	server.ListenAndServe()
}
