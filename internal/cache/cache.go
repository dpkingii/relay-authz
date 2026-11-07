// Package cache keeps check results in Redis, tagged with the revision they were computed at.
//
// A caller says how fresh an answer it needs with a minimum revision, a simplified version
// of Zanzibar's zookies. After revoking access, a caller passes the revision the delete
// returned, so a result cached before the revoke is never served (the "new enemy" problem).
// A caller that can tolerate staleness passes an older revision and gets more cache hits.
// A caller that passes none gets an answer at the recent snapshot from Revisions.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dpkingii/relay-authz/internal/check"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

// An entry only stays useful until the snapshot moves past it for default checks,
// so a short TTL keeps Redis from filling with dead results.
const ttl = 10 * time.Minute

var ErrFutureRevision = errors.New("at_least is newer than the latest revision")

type Checker struct {
	Uncached  check.Checker
	Revisions *Revisions
	Redis     *redis.Client // nil turns caching off
	Prefix    string        // changes when the rules do, so results computed under old rules aren't reused
}

type Result struct {
	Allowed bool
	// The answer reflects every change up to at least this revision.
	Revision int64
	Cached   bool
}

type entry struct {
	Allowed  bool  `json:"a"`
	Revision int64 `json:"r"`
}

// Recent asks for an answer at the snapshot from Revisions, which reflects every change
// committed up to its MaxStaleness ago.
const Recent int64 = -1

// Check answers from the cache if it holds a result computed at revision atLeast or later,
// and otherwise runs the check against the store and caches it.
func (c Checker) Check(ctx context.Context, object tuple.Object, relation string, user tuple.Object, atLeast int64) (Result, error) {
	// The revision is always read before any tuples, so the check sees every change up to it.
	var err error
	recent := atLeast == Recent
	if recent {
		if atLeast, err = c.Revisions.Snapshot(ctx); err != nil {
			return Result{}, err
		}
	}

	key := c.Prefix + object.String() + "#" + relation + "@" + user.String()
	if e, ok := c.get(ctx, key); ok && e.Revision >= atLeast {
		return Result{Allowed: e.Allowed, Revision: e.Revision, Cached: true}, nil
	}

	rev := atLeast
	if !recent {
		if rev, err = c.Revisions.Read(ctx); err != nil {
			return Result{}, err
		}
		if rev < atLeast {
			return Result{}, ErrFutureRevision
		}
	}
	allowed, err := c.Uncached.Check(ctx, object, relation, user)
	if err != nil {
		return Result{}, err
	}
	c.set(ctx, key, entry{Allowed: allowed, Revision: rev})
	return Result{Allowed: allowed, Revision: rev}, nil
}

// get and set treat Redis as best-effort: if it's down, checks still work, just uncached.
func (c Checker) get(ctx context.Context, key string) (entry, bool) {
	if c.Redis == nil {
		return entry{}, false
	}
	data, err := c.Redis.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			log.Printf("cache get: %v", err)
		}
		return entry{}, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return entry{}, false
	}
	return e, true
}

func (c Checker) set(ctx context.Context, key string, e entry) {
	if c.Redis == nil {
		return
	}
	data, _ := json.Marshal(e)
	if err := c.Redis.Set(ctx, key, data, ttl).Err(); err != nil {
		log.Printf("cache set: %v", err)
	}
}
