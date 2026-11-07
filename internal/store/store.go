// Package store keeps relation tuples in Postgres.
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dpkingii/relay-authz/internal/tuple"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Write adds a tuple and returns the new revision. Writing one that already exists
// changes nothing but still bumps the revision.
func (s *Store) Write(ctx context.Context, t tuple.Tuple) (int64, error) {
	return s.change(ctx, `
		insert into tuples (object_type, object_id, relation, subject_type, subject_id, subject_relation)
		values ($1, $2, $3, $4, $5, $6)
		on conflict do nothing`, t)
}

// Delete removes a tuple and returns the new revision. Deleting one that doesn't
// exist changes nothing but still bumps the revision.
func (s *Store) Delete(ctx context.Context, t tuple.Tuple) (int64, error) {
	return s.change(ctx, `
		delete from tuples
		where object_type = $1 and object_id = $2 and relation = $3
		  and subject_type = $4 and subject_id = $5 and subject_relation = $6`, t)
}

func (s *Store) change(ctx context.Context, sql string, t tuple.Tuple) (int64, error) {
	var rev int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, sql,
			t.Object.Type, t.Object.ID, t.Relation, t.Subject.Type, t.Subject.ID, t.Subject.Relation); err != nil {
			return err
		}
		// The row lock this takes is held until commit, so revisions commit in order.
		return tx.QueryRow(ctx, "update revision set value = value + 1 returning value").Scan(&rev)
	})
	return rev, err
}

// Revision returns the latest committed revision.
func (s *Store) Revision(ctx context.Context) (int64, error) {
	var rev int64
	err := s.pool.QueryRow(ctx, "select value from revision").Scan(&rev)
	return rev, err
}

// ReadSubjects returns every subject with the relation on the object.
func (s *Store) ReadSubjects(ctx context.Context, object tuple.Object, relation string) ([]tuple.Subject, error) {
	rows, err := s.pool.Query(ctx, `
		select subject_type, subject_id, subject_relation from tuples
		where object_type = $1 and object_id = $2 and relation = $3`,
		object.Type, object.ID, relation)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (tuple.Subject, error) {
		var s tuple.Subject
		err := row.Scan(&s.Type, &s.ID, &s.Relation)
		return s, err
	})
}

// ReadBySubjects returns every tuple whose subject is one of the objects, with any subject relation.
func (s *Store) ReadBySubjects(ctx context.Context, subjects []tuple.Object) ([]tuple.Tuple, error) {
	types, ids := make([]string, len(subjects)), make([]string, len(subjects))
	for i, o := range subjects {
		types[i], ids[i] = o.Type, o.ID
	}
	rows, err := s.pool.Query(ctx, `
		select t.object_type, t.object_id, t.relation, t.subject_type, t.subject_id, t.subject_relation
		from tuples t
		join unnest($1::text[], $2::text[]) as s(type, id) on t.subject_type = s.type and t.subject_id = s.id`,
		types, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (tuple.Tuple, error) {
		var t tuple.Tuple
		err := row.Scan(&t.Object.Type, &t.Object.ID, &t.Relation, &t.Subject.Type, &t.Subject.ID, &t.Subject.Relation)
		return t, err
	})
}
