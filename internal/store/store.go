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

// Write adds a tuple. Writing one that already exists is a no-op.
func (s *Store) Write(ctx context.Context, t tuple.Tuple) error {
	_, err := s.pool.Exec(ctx, `
		insert into tuples (object_type, object_id, relation, subject_type, subject_id, subject_relation)
		values ($1, $2, $3, $4, $5, $6)
		on conflict do nothing`,
		t.Object.Type, t.Object.ID, t.Relation, t.Subject.Type, t.Subject.ID, t.Subject.Relation)
	return err
}

// Delete removes a tuple. Deleting one that doesn't exist is a no-op.
func (s *Store) Delete(ctx context.Context, t tuple.Tuple) error {
	_, err := s.pool.Exec(ctx, `
		delete from tuples
		where object_type = $1 and object_id = $2 and relation = $3
		  and subject_type = $4 and subject_id = $5 and subject_relation = $6`,
		t.Object.Type, t.Object.ID, t.Relation, t.Subject.Type, t.Subject.ID, t.Subject.Relation)
	return err
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
