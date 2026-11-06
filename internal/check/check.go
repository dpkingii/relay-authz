// Package check answers "does this user have this relation on this object?"
package check

import (
	"context"

	"github.com/dpkingii/relay-authz/internal/tuple"
)

// Reader is the one store query a check needs. The Postgres store implements it,
// and the tests use an in-memory one.
type Reader interface {
	ReadSubjects(ctx context.Context, object tuple.Object, relation string) ([]tuple.Subject, error)
}

// Check reports whether user has relation on object.
//
// Week 1: direct tuples only. A tuple whose subject is a userset
// (group:finance#member) does not grant anything yet; following it is week 2.
func Check(ctx context.Context, r Reader, object tuple.Object, relation string, user tuple.Object) (bool, error) {
	subjects, err := r.ReadSubjects(ctx, object, relation)
	if err != nil {
		return false, err
	}
	want := tuple.Subject{Type: user.Type, ID: user.ID}
	for _, s := range subjects {
		if s == want {
			return true, nil
		}
	}
	return false, nil
}
