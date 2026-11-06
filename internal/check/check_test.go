package check

import (
	"context"
	"errors"
	"testing"

	"github.com/dpkingii/relay-authz/internal/tuple"
)

// memReader is an in-memory Reader over a fixed list of tuples.
type memReader []tuple.Tuple

func (m memReader) ReadSubjects(_ context.Context, object tuple.Object, relation string) ([]tuple.Subject, error) {
	var out []tuple.Subject
	for _, t := range m {
		if t.Object == object && t.Relation == relation {
			out = append(out, t.Subject)
		}
	}
	return out, nil
}

type failingReader struct{}

func (failingReader) ReadSubjects(context.Context, tuple.Object, string) ([]tuple.Subject, error) {
	return nil, errors.New("database down")
}

func tuples(t *testing.T, ss ...string) memReader {
	var m memReader
	for _, s := range ss {
		tup, err := tuple.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		m = append(m, tup)
	}
	return m
}

func object(t *testing.T, s string) tuple.Object {
	o, err := tuple.ParseObject(s)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestDirectCheck(t *testing.T) {
	r := tuples(t,
		"document:budget#viewer@user:alice",
		"document:budget#editor@user:bob",
		"document:roadmap#viewer@user:carol",
		"document:budget#viewer@bot:dave",
	)
	tests := []struct {
		name     string
		object   string
		relation string
		user     string
		want     bool
	}{
		{"direct tuple", "document:budget", "viewer", "user:alice", true},
		{"no tuple", "document:budget", "viewer", "user:erin", false},
		{"other relation on the same object", "document:budget", "viewer", "user:bob", false},
		{"same relation on another object", "document:budget", "viewer", "user:carol", false},
		{"same id, different type", "document:budget", "viewer", "user:dave", false},
		{"object with no tuples", "document:missing", "viewer", "user:alice", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Check(context.Background(), r, object(t, tc.object), tc.relation, object(t, tc.user))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Check(%s#%s@%s) = %v, want %v", tc.object, tc.relation, tc.user, got, tc.want)
			}
		})
	}
}

func TestCheckReturnsReaderErrors(t *testing.T) {
	_, err := Check(context.Background(), failingReader{}, object(t, "document:budget"), "viewer", object(t, "user:alice"))
	if err == nil {
		t.Fatal("want an error when the store fails, got nil")
	}
}
