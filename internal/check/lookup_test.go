package check

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/dpkingii/relay-authz/internal/tuple"
)

func ids(objects []tuple.Object) []string {
	out := []string{}
	for _, o := range objects {
		out = append(out, o.String())
	}
	return out
}

func TestLookup(t *testing.T) {
	l := Lookup{Rules: repoRules(t), Reader: example(t)}
	tests := []struct {
		name                string
		typ, relation, user string
		want                []string
	}{
		{"direct tuple", "document", "viewer", "user:alice", []string{"document:budget"}},
		{"another object", "document", "viewer", "user:carol", []string{"document:roadmap"}},
		{"nothing", "document", "viewer", "user:erin", []string{}},
		{"same id, different type", "document", "viewer", "user:dave", []string{}},
		{"owner implies editor", "document", "editor", "user:olga", []string{"document:budget"}},
		{"viewer does not imply editor", "document", "editor", "user:alice", []string{}},
		{"nested group member", "document", "viewer", "user:abe", []string{"document:budget"}},
		{"parent folder editor through a group", "document", "editor", "user:lee", []string{"document:budget"}},
		{"grandparent folder owner, folders", "folder", "viewer", "user:fin", []string{"folder:finance", "folder:q3"}},
		{"grandparent folder owner, documents", "document", "viewer", "user:fin", []string{"document:budget"}},
		{"member inside a group cycle", "document", "viewer", "user:yan", []string{"document:loop"}},
		{"groups the user is in", "group", "member", "user:yan", []string{"group:a", "group:b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := l.Objects(context.Background(), tc.typ, tc.relation, object(t, tc.user))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids(got), tc.want) {
				t.Errorf("Objects(%s#%s@%s) = %v, want %v", tc.typ, tc.relation, tc.user, ids(got), tc.want)
			}
		})
	}
}

func TestLookupDepthLimit(t *testing.T) {
	ctx := context.Background()
	end := object(t, "user:end")

	got, err := Lookup{Rules: repoRules(t), Reader: groupChain(t, MaxDepth)}.Objects(ctx, "document", "viewer", end)
	if err != nil || len(got) != 1 {
		t.Errorf("chain of %d groups: got %v, %v, want document:doc", MaxDepth, got, err)
	}
	_, err = Lookup{Rules: repoRules(t), Reader: groupChain(t, MaxDepth+1)}.Objects(ctx, "document", "viewer", end)
	if !errors.Is(err, ErrTooDeep) {
		t.Errorf("chain of %d groups: got err %v, want ErrTooDeep", MaxDepth+1, err)
	}
}

func TestLookupReturnsReaderErrors(t *testing.T) {
	l := Lookup{Rules: repoRules(t), Reader: failingReader{}}
	if _, err := l.Objects(context.Background(), "document", "viewer", object(t, "user:alice")); err == nil {
		t.Fatal("want an error when the store fails, got nil")
	}
}

// TestLookupMatchesBruteForce compares Objects against the same bottom-up computation
// TestMatchesBruteForce uses for Check.
func TestLookupMatchesBruteForce(t *testing.T) {
	rs := repoRules(t)
	var users, objects []tuple.Object
	for i := range 4 {
		users = append(users, tuple.Object{Type: "user", ID: fmt.Sprint(i)})
		for _, typ := range []string{"group", "folder", "document"} {
			objects = append(objects, tuple.Object{Type: typ, ID: fmt.Sprint(i)})
		}
	}

	for seed := range uint64(300) {
		ts := randomTuples(rand.New(rand.NewPCG(seed, 0)), users, objects)
		g := closure(rs, ts, users, objects)
		l := Lookup{Rules: rs, Reader: memReader(ts)}
		for typ, relations := range rs {
			for rel := range relations {
				for _, u := range users {
					want := []string{}
					for _, o := range objects {
						if o.Type == typ && g[grant{o, rel, u}] {
							want = append(want, o.String())
						}
					}
					got, err := l.Objects(context.Background(), typ, rel, u)
					if err != nil {
						t.Fatalf("seed %d: Objects(%s#%s@%s): %v", seed, typ, rel, u, err)
					}
					if !slices.Equal(ids(got), want) {
						t.Fatalf("seed %d: Objects(%s#%s@%s) = %v, brute force says %v\ntuples: %v",
							seed, typ, rel, u, ids(got), want, ts)
					}
				}
			}
		}
	}
}
