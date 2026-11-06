package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/dpkingii/zanzibar-authz/internal/api"
	"github.com/dpkingii/zanzibar-authz/internal/store"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	s, err := store.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, api.New(s)))
}
