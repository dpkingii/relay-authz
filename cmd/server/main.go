package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/redis/go-redis/v9"

	"github.com/dpkingii/relay-authz/internal/api"
	"github.com/dpkingii/relay-authz/internal/cache"
	"github.com/dpkingii/relay-authz/internal/check"
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

	rulesYAML, err := os.ReadFile(rulesPath)
	if err != nil {
		log.Fatal(err)
	}
	rs, err := rules.Parse(rulesYAML)
	if err != nil {
		log.Fatal(err)
	}
	s, err := store.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	rulesHash := sha256.Sum256(rulesYAML)
	checker := cache.Checker{
		Uncached: check.Checker{Reader: s, Rules: rs},
		Revision: s.Revision,
		Prefix:   fmt.Sprintf("check:%x:", rulesHash[:8]),
	}
	// Without REDIS_URL the server runs uncached, which is the baseline for benchmarks.
	if url := os.Getenv("REDIS_URL"); url != "" {
		opts, err := redis.ParseURL(url)
		if err != nil {
			log.Fatal(err)
		}
		checker.Redis = redis.NewClient(opts)
		defer checker.Redis.Close()
	}

	log.Printf("listening on %s (cache: %t)", addr, checker.Redis != nil)
	log.Fatal(http.ListenAndServe(addr, api.New(s, rs, checker)))
}
