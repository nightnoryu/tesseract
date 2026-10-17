package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	address := os.Getenv("TESSERACT_HTTP_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Tesseract development server\n"))
	})
	server := &http.Server{
		Addr: address, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("listening on %s", address)
	log.Fatal(server.ListenAndServe())
}
