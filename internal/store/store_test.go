package store

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/dpkingii/relay-authz/internal/tuple"
)

func newStore(t *testing.T) *Store {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; run `docker compose up -d` first")
	}
	ctx := context.Background()
	s, err := New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.pool.Exec(ctx, "truncate tuples"); err != nil {
		t.Fatal(err)
	}
	return s
}

func mustParse(t *testing.T, s string) tuple.Tuple {
	tup, err := tuple.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return tup
}

func subjects(t *testing.T, s *Store, object, relation string) []string {
	obj, err := tuple.ParseObject(object)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := s.ReadSubjects(context.Background(), obj, relation)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sub := range subs {
		out = append(out, sub.String())
	}
	slices.Sort(out)
	return out
}

func TestWriteReadDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, tup := range []string{
		"document:budget#viewer@user:alice",
		"document:budget#viewer@group:finance#member",
		"document:budget#editor@user:bob",
		"document:roadmap#viewer@user:carol",
	} {
		if err := s.Write(ctx, mustParse(t, tup)); err != nil {
			t.Fatal(err)
		}
	}

	got := subjects(t, s, "document:budget", "viewer")
	want := []string{"group:finance#member", "user:alice"}
	if !slices.Equal(got, want) {
		t.Errorf("viewers = %v, want %v", got, want)
	}

	if err := s.Delete(ctx, mustParse(t, "document:budget#viewer@user:alice")); err != nil {
		t.Fatal(err)
	}
	got = subjects(t, s, "document:budget", "viewer")
	want = []string{"group:finance#member"}
	if !slices.Equal(got, want) {
		t.Errorf("viewers after delete = %v, want %v", got, want)
	}
}

func TestWriteAndDeleteAreIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tup := mustParse(t, "document:budget#viewer@user:alice")
	for range 2 {
		if err := s.Write(ctx, tup); err != nil {
			t.Fatal(err)
		}
	}
	if got := subjects(t, s, "document:budget", "viewer"); len(got) != 1 {
		t.Errorf("viewers after writing twice = %v, want one", got)
	}
	for range 2 {
		if err := s.Delete(ctx, tup); err != nil {
			t.Fatal(err)
		}
	}
	if got := subjects(t, s, "document:budget", "viewer"); len(got) != 0 {
		t.Errorf("viewers after deleting = %v, want none", got)
	}
}
