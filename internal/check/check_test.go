package check

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/dpkingii/relay-authz/internal/rules"
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

func (m memReader) ReadBySubjects(_ context.Context, subjects []tuple.Object) ([]tuple.Tuple, error) {
	var out []tuple.Tuple
	for _, t := range m {
		if slices.Contains(subjects, tuple.Object{Type: t.Subject.Type, ID: t.Subject.ID}) {
			out = append(out, t)
		}
	}
	return out, nil
}

type failingReader struct{}

func (failingReader) ReadSubjects(context.Context, tuple.Object, string) ([]tuple.Subject, error) {
	return nil, errors.New("database down")
}

func (failingReader) ReadBySubjects(context.Context, []tuple.Object) ([]tuple.Tuple, error) {
	return nil, errors.New("database down")
}

func repoRules(t *testing.T) rules.Rules {
	r, err := rules.Load("../../rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return r
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

// example is the tuple set TestCheck and TestLookup share.
func example(t *testing.T) memReader {
	return tuples(t,
		"document:budget#viewer@user:alice",
		"document:budget#editor@user:bob",
		"document:budget#owner@user:olga",
		"document:roadmap#viewer@user:carol",
		"document:budget#viewer@bot:dave",

		"document:budget#viewer@group:finance#member",
		"group:finance#member@user:fay",
		"group:finance#member@group:accounting#member",
		"group:accounting#member@user:abe",

		"document:budget#parent@folder:q3",
		"folder:q3#viewer@user:vic",
		"folder:q3#editor@group:leads#member",
		"group:leads#member@user:lee",
		"folder:q3#parent@folder:finance",
		"folder:finance#owner@user:fin",

		"group:a#member@group:b#member",
		"group:b#member@group:a#member",
		"group:b#member@user:yan",
		"document:loop#viewer@group:a#member",
	)
}

func TestCheck(t *testing.T) {
	c := Checker{Rules: repoRules(t), Reader: example(t)}
	tests := []struct {
		name     string
		object   string
		relation string
		user     string
		want     bool
	}{
		{"direct tuple", "document:budget", "viewer", "user:alice", true},
		{"no tuple", "document:budget", "viewer", "user:erin", false},
		{"same relation on another object", "document:budget", "viewer", "user:carol", false},
		{"same id, different type", "document:budget", "viewer", "user:dave", false},
		{"object with no tuples", "document:missing", "viewer", "user:alice", false},

		{"editor implies viewer", "document:budget", "viewer", "user:bob", true},
		{"owner implies viewer through editor", "document:budget", "viewer", "user:olga", true},
		{"viewer does not imply editor", "document:budget", "editor", "user:alice", false},

		{"group member", "document:budget", "viewer", "user:fay", true},
		{"nested group member", "document:budget", "viewer", "user:abe", true},
		{"group member gets only the group's relation", "document:budget", "editor", "user:fay", false},

		{"parent folder viewer", "document:budget", "viewer", "user:vic", true},
		{"parent folder editor through a group", "document:budget", "editor", "user:lee", true},
		{"grandparent folder owner", "document:budget", "viewer", "user:fin", true},
		{"parent folder viewer is not an editor", "document:budget", "editor", "user:vic", false},

		{"member inside a group cycle", "document:loop", "viewer", "user:yan", true},
		{"non-member of a group cycle", "document:loop", "viewer", "user:zed", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Check(context.Background(), object(t, tc.object), tc.relation, object(t, tc.user))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Check(%s#%s@%s) = %v, want %v", tc.object, tc.relation, tc.user, got, tc.want)
			}
		})
	}
}

// groupChain makes document:doc viewable through n nested groups, with user:end in the last one.
func groupChain(t *testing.T, n int) memReader {
	ss := []string{"document:doc#viewer@group:g0#member"}
	for i := range n - 1 {
		ss = append(ss, fmt.Sprintf("group:g%d#member@group:g%d#member", i, i+1))
	}
	ss = append(ss, fmt.Sprintf("group:g%d#member@user:end", n-1))
	return tuples(t, ss...)
}

func TestDepthLimit(t *testing.T) {
	ctx := context.Background()
	doc, end := object(t, "document:doc"), object(t, "user:end")

	ok, err := Checker{Rules: repoRules(t), Reader: groupChain(t, MaxDepth)}.Check(ctx, doc, "viewer", end)
	if err != nil || !ok {
		t.Errorf("chain of %d groups: got %v, %v, want true", MaxDepth, ok, err)
	}
	_, err = Checker{Rules: repoRules(t), Reader: groupChain(t, MaxDepth+1)}.Check(ctx, doc, "viewer", end)
	if !errors.Is(err, ErrTooDeep) {
		t.Errorf("chain of %d groups: got err %v, want ErrTooDeep", MaxDepth+1, err)
	}
}

func TestCheckReturnsReaderErrors(t *testing.T) {
	c := Checker{Rules: repoRules(t), Reader: failingReader{}}
	_, err := c.Check(context.Background(), object(t, "document:budget"), "viewer", object(t, "user:alice"))
	if err == nil {
		t.Fatal("want an error when the store fails, got nil")
	}
}

// TestMatchesBruteForce compares Check against an independent bottom-up computation
// on random tuple sets, which include group and folder cycles.
func TestMatchesBruteForce(t *testing.T) {
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
		want := closure(rs, ts, users, objects)
		c := Checker{Rules: rs, Reader: memReader(ts)}
		for _, o := range objects {
			for rel := range rs[o.Type] {
				for _, u := range users {
					got, err := c.Check(context.Background(), o, rel, u)
					if err != nil {
						t.Fatalf("seed %d: Check(%s#%s@%s): %v", seed, o, rel, u, err)
					}
					if got != want[grant{o, rel, u}] {
						t.Fatalf("seed %d: Check(%s#%s@%s) = %v, brute force says %v\ntuples: %v",
							seed, o, rel, u, got, !got, ts)
					}
				}
			}
		}
	}
}

func randomTuples(r *rand.Rand, users, objects []tuple.Object) []tuple.Tuple {
	pick := func(typ string) tuple.Object {
		for {
			if o := objects[r.IntN(len(objects))]; o.Type == typ {
				return o
			}
		}
	}
	subject := func() tuple.Subject {
		if r.IntN(2) == 0 {
			u := users[r.IntN(len(users))]
			return tuple.Subject{Type: u.Type, ID: u.ID}
		}
		g := pick("group")
		return tuple.Subject{Type: g.Type, ID: g.ID, Relation: "member"}
	}
	var ts []tuple.Tuple
	for range 5 + r.IntN(20) {
		var t tuple.Tuple
		switch r.IntN(4) {
		case 0:
			t = tuple.Tuple{Object: pick("group"), Relation: "member", Subject: subject()}
		case 1:
			typ := []string{"folder", "document"}[r.IntN(2)]
			f := pick("folder")
			t = tuple.Tuple{Object: pick(typ), Relation: "parent", Subject: tuple.Subject{Type: f.Type, ID: f.ID}}
		default:
			typ := []string{"folder", "document"}[r.IntN(2)]
			rel := []string{"owner", "editor", "viewer"}[r.IntN(3)]
			t = tuple.Tuple{Object: pick(typ), Relation: rel, Subject: subject()}
		}
		ts = append(ts, t)
	}
	return ts
}

type grant struct {
	object   tuple.Object
	relation string
	user     tuple.Object
}

// closure applies every rule to every object repeatedly until no new grant appears.
func closure(rs rules.Rules, ts []tuple.Tuple, users, objects []tuple.Object) map[grant]bool {
	g := map[grant]bool{}
	for changed := true; changed; {
		changed = false
		add := func(x grant) {
			if !g[x] {
				g[x] = true
				changed = true
			}
		}
		for _, u := range users {
			for _, t := range ts {
				s := tuple.Object{Type: t.Subject.Type, ID: t.Subject.ID}
				if (t.Subject.Relation == "" && s == u) || (t.Subject.Relation != "" && g[grant{s, t.Subject.Relation, u}]) {
					add(grant{t.Object, t.Relation, u})
				}
			}
			for _, o := range objects {
				for rel, rule := range rs[o.Type] {
					for _, implied := range rule.ImpliedBy {
						if g[grant{o, implied, u}] {
							add(grant{o, rel, u})
						}
					}
					for _, via := range rule.Via {
						for _, t := range ts {
							related := tuple.Object{Type: t.Subject.Type, ID: t.Subject.ID}
							if t.Object == o && t.Relation == via.Tupleset && g[grant{related, via.Relation, u}] {
								add(grant{o, rel, u})
							}
						}
					}
				}
			}
		}
	}
	return g
}
