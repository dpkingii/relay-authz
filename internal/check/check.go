// Package check answers "does this user have this relation on this object?"
package check

import (
	"context"
	"errors"

	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

// MaxDepth bounds how many hops a check follows, so a deep group or folder
// chain can't make one request run unbounded queries.
const MaxDepth = 25

var ErrTooDeep = errors.New("check: exceeded max depth")

// Reader is the one store query a check needs. The Postgres store implements it,
// and the tests use an in-memory one.
type Reader interface {
	ReadSubjects(ctx context.Context, object tuple.Object, relation string) ([]tuple.Subject, error)
}

type Checker struct {
	Reader Reader
	Rules  rules.Rules
}

// Check reports whether user has relation on object, following usersets
// (group:finance#member), implied relations and related objects from the rules.
func (c Checker) Check(ctx context.Context, object tuple.Object, relation string, user tuple.Object) (bool, error) {
	w := walk{Checker: c, user: user, seen: map[node]bool{}}
	return w.check(ctx, node{object, relation}, 0)
}

// node is one object#relation in the graph a check walks.
type node struct {
	object   tuple.Object
	relation string
}

type walk struct {
	Checker
	user tuple.Object
	seen map[node]bool
}

func (w *walk) check(ctx context.Context, n node, depth int) (bool, error) {
	// Every rule is a union, so a node that already returned false stays false, and
	// a true anywhere ends the walk. That makes a second visit to any node, whether
	// a cycle or a diamond, safe to answer false without querying it again.
	if w.seen[n] {
		return false, nil
	}
	w.seen[n] = true
	if depth > MaxDepth {
		return false, ErrTooDeep
	}

	subjects, err := w.Reader.ReadSubjects(ctx, n.object, n.relation)
	if err != nil {
		return false, err
	}
	for _, s := range subjects {
		if s.Relation == "" {
			if s.Type == w.user.Type && s.ID == w.user.ID {
				return true, nil
			}
			continue
		}
		if ok, err := w.check(ctx, node{tuple.Object{Type: s.Type, ID: s.ID}, s.Relation}, depth+1); ok || err != nil {
			return ok, err
		}
	}

	rule := w.Rules[n.object.Type][n.relation]
	for _, implied := range rule.ImpliedBy {
		if ok, err := w.check(ctx, node{n.object, implied}, depth+1); ok || err != nil {
			return ok, err
		}
	}
	for _, via := range rule.Via {
		related, err := w.Reader.ReadSubjects(ctx, n.object, via.Tupleset)
		if err != nil {
			return false, err
		}
		for _, r := range related {
			if ok, err := w.check(ctx, node{tuple.Object{Type: r.Type, ID: r.ID}, via.Relation}, depth+1); ok || err != nil {
				return ok, err
			}
		}
	}
	return false, nil
}
