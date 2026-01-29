package main

import (
	"log"
	"net/http"
)

func main() {
	sm := http.NewServeMux()
	sm.Handle("/", http.FileServer(http.Dir(".")))
	server := http.Server{
		Addr:    ":8080",
		Handler: sm,
	}
	log.Fatal(server.ListenAndServe())
}
