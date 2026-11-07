package store

import (
	"context"
	"fmt"
	"math/rand/v2"
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
	s, err := New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// uniqueID keeps tests that share the database from seeing each other's tuples.
func uniqueID() string { return fmt.Sprintf("t%x", rand.Uint64()) }

func mustParse(t *testing.T, s string) tuple.Tuple {
	tup, err := tuple.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return tup
}

func subjects(t *testing.T, s *Store, object tuple.Object, relation string) []string {
	subs, err := s.ReadSubjects(context.Background(), object, relation)
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
	doc, other := uniqueID(), uniqueID()
	for _, tup := range []string{
		"document:" + doc + "#viewer@user:alice",
		"document:" + doc + "#viewer@group:finance#member",
		"document:" + doc + "#editor@user:bob",
		"document:" + other + "#viewer@user:carol",
	} {
		if _, err := s.Write(ctx, mustParse(t, tup)); err != nil {
			t.Fatal(err)
		}
	}

	object := tuple.Object{Type: "document", ID: doc}
	got := subjects(t, s, object, "viewer")
	want := []string{"group:finance#member", "user:alice"}
	if !slices.Equal(got, want) {
		t.Errorf("viewers = %v, want %v", got, want)
	}

	if _, err := s.Delete(ctx, mustParse(t, "document:"+doc+"#viewer@user:alice")); err != nil {
		t.Fatal(err)
	}
	got = subjects(t, s, object, "viewer")
	want = []string{"group:finance#member"}
	if !slices.Equal(got, want) {
		t.Errorf("viewers after delete = %v, want %v", got, want)
	}
}

func TestWriteAndDeleteAreIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	doc := uniqueID()
	tup := mustParse(t, "document:"+doc+"#viewer@user:alice")
	object := tuple.Object{Type: "document", ID: doc}
	for range 2 {
		if _, err := s.Write(ctx, tup); err != nil {
			t.Fatal(err)
		}
	}
	if got := subjects(t, s, object, "viewer"); len(got) != 1 {
		t.Errorf("viewers after writing twice = %v, want one", got)
	}
	for range 2 {
		if _, err := s.Delete(ctx, tup); err != nil {
			t.Fatal(err)
		}
	}
	if got := subjects(t, s, object, "viewer"); len(got) != 0 {
		t.Errorf("viewers after deleting = %v, want none", got)
	}
}

func TestReadBySubjects(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a, b, other := uniqueID(), uniqueID(), uniqueID()
	for _, tup := range []string{
		"document:d" + a + "#viewer@user:" + a,
		"document:d" + a + "#viewer@group:" + b + "#member",
		"folder:f" + a + "#parent@folder:" + b,
		"document:d" + other + "#viewer@user:" + other,
	} {
		if _, err := s.Write(ctx, mustParse(t, tup)); err != nil {
			t.Fatal(err)
		}
	}

	ts, err := s.ReadBySubjects(ctx, []tuple.Object{
		{Type: "user", ID: a}, {Type: "group", ID: b}, {Type: "folder", ID: b}, {Type: "user", ID: "nobody" + a},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tup := range ts {
		got = append(got, tup.String())
	}
	slices.Sort(got)
	want := []string{
		"document:d" + a + "#viewer@group:" + b + "#member",
		"document:d" + a + "#viewer@user:" + a,
		"folder:f" + a + "#parent@folder:" + b,
	}
	if !slices.Equal(got, want) {
		t.Errorf("tuples = %v, want %v", got, want)
	}
}

func TestEveryChangeBumpsTheRevision(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tup := mustParse(t, "document:"+uniqueID()+"#viewer@user:alice")

	before, err := s.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	written, err := s.Write(ctx, tup)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := s.Delete(ctx, tup)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !(before < written && written < deleted && deleted <= latest) {
		t.Errorf("revisions before %d, write %d, delete %d, latest %d: want increasing", before, written, deleted, latest)
	}
}
