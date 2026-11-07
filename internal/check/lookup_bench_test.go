package check

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/store"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

// counting counts store queries so benchmarks can report queries per lookup.
type counting struct {
	s *store.Store
	n int
}

func (c *counting) ReadSubjects(ctx context.Context, object tuple.Object, relation string) ([]tuple.Subject, error) {
	c.n++
	return c.s.ReadSubjects(ctx, object, relation)
}

func (c *counting) ReadBySubjects(ctx context.Context, subjects []tuple.Object) ([]tuple.Tuple, error) {
	c.n++
	return c.s.ReadBySubjects(ctx, subjects)
}

// BenchmarkLookup compares the backward walk against checking every document, on the
// load test's dataset in real Postgres. Run with:
//
//	go test ./internal/check -run '^$' -bench Lookup -benchtime 5x
func BenchmarkLookup(b *testing.B) {
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
	rs, err := rules.Load("../../rules.yaml")
	if err != nil {
		b.Fatal(err)
	}

	// The same shape as loadtest/check.js: 500 users in 50 groups, half nested in the other
	// half; a tree of 5 root, 25 child and 125 leaf folders; 2,000 documents in the leaves.
	id := fmt.Sprintf("b%x", rand.Uint64())
	var ts []string
	for u := range 500 {
		ts = append(ts, fmt.Sprintf("group:%s-g%d#member@user:%s-u%d", id, u%50, id, u))
	}
	for g := range 25 {
		ts = append(ts, fmt.Sprintf("group:%s-g%d#member@group:%s-g%d#member", id, g, id, g+25))
	}
	for r := range 5 {
		ts = append(ts, fmt.Sprintf("folder:%s-r%d#viewer@group:%s-g%d#member", id, r, id, r))
	}
	for c := range 25 {
		ts = append(ts, fmt.Sprintf("folder:%s-c%d#parent@folder:%s-r%d", id, c, id, c%5))
		ts = append(ts, fmt.Sprintf("folder:%s-c%d#editor@group:%s-g%d#member", id, c, id, 5+c))
	}
	for l := range 125 {
		ts = append(ts, fmt.Sprintf("folder:%s-l%d#parent@folder:%s-c%d", id, l, id, l%25))
	}
	var docs []tuple.Object
	for d := range 2000 {
		ts = append(ts, fmt.Sprintf("document:%s-d%d#parent@folder:%s-l%d", id, d, id, d%125))
		ts = append(ts, fmt.Sprintf("document:%s-d%d#owner@user:%s-u%d", id, d, id, (d*7)%500))
		docs = append(docs, tuple.Object{Type: "document", ID: fmt.Sprintf("%s-d%d", id, d)})
	}
	for _, line := range ts {
		t, err := tuple.Parse(line)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Write(ctx, t); err != nil {
			b.Fatal(err)
		}
	}

	cases := []struct{ name, user string }{
		// u25 is in g25, which is nested in g0, a root folder's viewers: about 400 documents.
		{"root-viewer", "u25"},
		// u30 is in g30, nested in g5, a child folder's editors: about 80 documents.
		{"child-editor", "u30"},
	}
	for _, tc := range cases {
		user := tuple.Object{Type: "user", ID: id + "-" + tc.user}
		reader := &counting{s: s}
		want, err := Lookup{Reader: reader, Rules: rs}.Objects(ctx, "document", "viewer", user)
		if err != nil {
			b.Fatal(err)
		}

		b.Run(tc.name+"/backward", func(b *testing.B) {
			reader.n = 0
			for b.Loop() {
				if _, err := (Lookup{Reader: reader, Rules: rs}).Objects(ctx, "document", "viewer", user); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(reader.n)/float64(b.N), "queries/op")
			b.ReportMetric(float64(len(want)), "objects")
		})
		b.Run(tc.name+"/check-each", func(b *testing.B) {
			reader.n = 0
			c := Checker{Reader: reader, Rules: rs}
			for b.Loop() {
				n := 0
				for _, d := range docs {
					ok, err := c.Check(ctx, d, "viewer", user)
					if err != nil {
						b.Fatal(err)
					}
					if ok {
						n++
					}
				}
				if n != len(want) {
					b.Fatalf("checking each document found %d, the backward walk %d", n, len(want))
				}
			}
			b.ReportMetric(float64(reader.n)/float64(b.N), "queries/op")
			b.ReportMetric(float64(len(want)), "objects")
		})
	}
}
