package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

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

	// 0 by default, so a check without at_least always reflects every committed change,
	// including a revoke a moment ago. Raising it trades that for cache hits under writes.
	var maxStaleness time.Duration
	if v := os.Getenv("MAX_STALENESS"); v != "" {
		if maxStaleness, err = time.ParseDuration(v); err != nil {
			log.Fatalf("MAX_STALENESS: %v", err)
		}
	}

	rulesHash := sha256.Sum256(rulesYAML)
	checker := cache.Checker{
		Uncached:  check.Checker{Reader: s, Rules: rs},
		Revisions: &cache.Revisions{Read: s.Revision, MaxStaleness: maxStaleness},
		Prefix:    fmt.Sprintf("check:%x:", rulesHash[:8]),
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

	log.Printf("listening on %s (cache: %t, max staleness: %s)", addr, checker.Redis != nil, maxStaleness)
	log.Fatal(http.ListenAndServe(addr, api.New(s, rs, checker)))
}
