package cache

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/dpkingii/relay-authz/internal/check"
	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/store"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

// countingReader counts store queries so benchmarks can report queries per check.
type countingReader struct {
	check.Reader
	n int
}

func (c *countingReader) ReadSubjects(ctx context.Context, object tuple.Object, relation string) ([]tuple.Subject, error) {
	c.n++
	return c.Reader.ReadSubjects(ctx, object, relation)
}

// BenchmarkCheck measures checks against real Postgres and Redis for graphs of different
// depths, uncached and cached. Run with:
//
//	go test ./internal/cache -run '^$' -bench . -benchtime 2000x
func BenchmarkCheck(b *testing.B) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		b.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := store.New(ctx, databaseURL)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(s.Close)
	rdb := redisClient(b)
	rs, err := rules.Load("../../rules.yaml")
	if err != nil {
		b.Fatal(err)
	}

	id := fmt.Sprintf("b%x", rand.Uint64())
	write := func(format string, args ...any) {
		t, err := tuple.Parse(fmt.Sprintf(format, args...))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Write(ctx, t); err != nil {
			b.Fatal(err)
		}
	}
	// direct: a tuple names the user.
	write("document:%s-direct#viewer@user:%s", id, id)
	// groups: the user is in the 5th of 5 nested groups.
	write("document:%s-groups#viewer@group:%s-g0#member", id, id)
	for i := range 4 {
		write("group:%s-g%d#member@group:%s-g%d#member", id, i, id, i+1)
	}
	write("group:%s-g4#member@user:%s", id, id)
	// folders: the user views the 5th of 5 nested parent folders.
	write("document:%s-folders#parent@folder:%s-f0", id, id)
	for i := range 4 {
		write("folder:%s-f%d#parent@folder:%s-f%d", id, i, id, i+1)
	}
	write("folder:%s-f4#viewer@user:%s", id, id)
	setup, err := s.Revision(ctx)
	if err != nil {
		b.Fatal(err)
	}

	cases := []struct{ name, doc, user string }{
		{"direct", "direct", id},
		{"groups-5", "groups", id},
		{"folders-5", "folders", id},
		// A deny has to walk every path before giving up, so it's the slowest case.
		{"folders-5-denied", "folders", id + "-other"},
	}
	for _, tc := range cases {
		object := tuple.Object{Type: "document", ID: id + "-" + tc.doc}
		user := tuple.Object{Type: "user", ID: tc.user}
		reader := &countingReader{Reader: s}
		variants := []struct {
			name    string
			c       Checker
			atLeast int64
		}{
			{"uncached", Checker{Uncached: check.Checker{Reader: reader, Rules: rs}, Revision: s.Revision}, Latest},
			{"cached", Checker{Uncached: check.Checker{Reader: reader, Rules: rs}, Revision: s.Revision, Redis: rdb, Prefix: id + ":"}, Latest},
			{"cached-at-least", Checker{Uncached: check.Checker{Reader: reader, Rules: rs}, Revision: s.Revision, Redis: rdb, Prefix: id + ":"}, setup},
		}
		for _, v := range variants {
			b.Run(tc.name+"/"+v.name, func(b *testing.B) {
				reader.n = 0
				for b.Loop() {
					if _, err := v.c.Check(ctx, object, "viewer", user, v.atLeast); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(reader.n)/float64(b.N), "queries/op")
			})
		}
	}
}
