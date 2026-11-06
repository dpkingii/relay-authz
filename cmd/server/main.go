package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/dpkingii/relay-authz/internal/api"
	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/store"
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
	rulesPath := os.Getenv("RULES")
	if rulesPath == "" {
		rulesPath = "rules.yaml"
	}

	rs, err := rules.Load(rulesPath)
	if err != nil {
		log.Fatal(err)
	}
	s, err := store.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, api.New(s, rs)))
}
