package main

import (
	"github.com/rappa850/cd2-strm-gateway/internal/gateway"
	"log"
	"net/http"
	"os"
)

func main() {
	data := os.Getenv("DATA_DIR")
	if data == "" {
		data = "data"
	}
	app, token, err := gateway.New(data)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()
	if token != "" {
		log.Printf("Initial Admin Token (shown once): %s", token)
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("cd2-strm-gateway listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, app.Handler()))
}
