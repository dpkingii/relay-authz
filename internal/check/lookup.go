package check

import (
	"context"
	"slices"
	"strings"

	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

// ReverseReader is the store query a lookup needs.
type ReverseReader interface {
	// ReadBySubjects returns every tuple whose subject is one of the objects, with or
	// without a relation (group:finance and group:finance#member both match group:finance).
	ReadBySubjects(ctx context.Context, subjects []tuple.Object) ([]tuple.Tuple, error)
}

// Lookup answers the reverse of a check: which objects of a type does a user have a relation on?
type Lookup struct {
	Reader ReverseReader
	Rules  rules.Rules
}

// Objects returns every object of objectType that user has relation on, sorted.
//
// Checking every object of the type would walk the graph once per object. Instead this
// walks it backward from the user, reaching every object#relation the user has in one
// pass. It mirrors Check's three rules: a node is reached when
//   - a tuple gives it to the user directly, or to a reached node as a userset
//   - a reached relation on the same object implies it
//   - its tupleset points at an object where the via relation is reached
//
// Each round reads the tuples pointing at every newly reached object in one query, so
// the number of queries is the number of hops, not the number of objects.
func (l Lookup) Objects(ctx context.Context, objectType, relation string, user tuple.Object) ([]tuple.Object, error) {
	implies, vias := inverse(l.Rules)
	// The user is the node with no relation, which matches tuples that name it directly.
	start := node{user, ""}
	reached := map[node]bool{start: true}
	frontier := []node{start}
	// Tuples whose subject is the object, for every object read so far.
	pointing := map[tuple.Object][]tuple.Tuple{}

	for round := 0; len(frontier) > 0; round++ {
		// Round k reaches nodes k hops from the user, which Check reaches at depth k-1.
		if round > MaxDepth+1 {
			return nil, ErrTooDeep
		}
		var unread []tuple.Object
		for _, n := range frontier {
			if _, ok := pointing[n.object]; !ok {
				pointing[n.object] = nil
				unread = append(unread, n.object)
			}
		}
		if len(unread) > 0 {
			ts, err := l.Reader.ReadBySubjects(ctx, unread)
			if err != nil {
				return nil, err
			}
			for _, t := range ts {
				o := tuple.Object{Type: t.Subject.Type, ID: t.Subject.ID}
				pointing[o] = append(pointing[o], t)
			}
		}

		var next []node
		reach := func(n node) {
			if !reached[n] {
				reached[n] = true
				next = append(next, n)
			}
		}
		for _, n := range frontier {
			for _, r := range implies[n.object.Type][n.relation] {
				reach(node{n.object, r})
			}
			for _, t := range pointing[n.object] {
				if t.Subject.Relation == n.relation {
					reach(node{t.Object, t.Relation})
				}
				// Like Check, a via follows the tupleset's subject whatever its relation.
				for _, r := range vias[t.Object.Type][via{t.Relation, n.relation}] {
					reach(node{t.Object, r})
				}
			}
		}
		frontier = next
	}

	var out []tuple.Object
	for n := range reached {
		if n.object.Type == objectType && n.relation == relation {
			out = append(out, n.object)
		}
	}
	slices.SortFunc(out, func(a, b tuple.Object) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

type via struct{ tupleset, relation string }

// inverse turns the rules around for walking backward: implies[type][r] lists the relations
// that r implies, and vias[type][{tupleset, r}] lists the relations granted to an object
// whose tupleset points at an object with r.
func inverse(rs rules.Rules) (map[string]map[string][]string, map[string]map[via][]string) {
	implies := map[string]map[string][]string{}
	vias := map[string]map[via][]string{}
	for typ, relations := range rs {
		implies[typ] = map[string][]string{}
		vias[typ] = map[via][]string{}
		for name, rel := range relations {
			for _, by := range rel.ImpliedBy {
				implies[typ][by] = append(implies[typ][by], name)
			}
			for _, v := range rel.Via {
				k := via{v.Tupleset, v.Relation}
				vias[typ][k] = append(vias[typ][k], name)
			}
		}
	}
	return implies, vias
}
