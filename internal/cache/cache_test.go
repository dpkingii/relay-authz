package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dpkingii/relay-authz/internal/check"
	"github.com/dpkingii/relay-authz/internal/rules"
	"github.com/dpkingii/relay-authz/internal/tuple"
)

// fakeStore is a tuple set and revision the test changes directly, counting reads
// so tests can tell a cache hit from a recomputed check.
type fakeStore struct {
	tuples   []tuple.Tuple
	revision int64
	reads    int
}

func (f *fakeStore) ReadSubjects(_ context.Context, object tuple.Object, relation string) ([]tuple.Subject, error) {
	f.reads++
	var out []tuple.Subject
	for _, t := range f.tuples {
		if t.Object == object && t.Relation == relation {
			out = append(out, t.Subject)
		}
	}
	return out, nil
}

func (f *fakeStore) Revision(context.Context) (int64, error) { return f.revision, nil }

var (
	doc   = tuple.Object{Type: "document", ID: "budget"}
	alice = tuple.Object{Type: "user", ID: "alice"}
	grant = tuple.Tuple{Object: doc, Relation: "viewer", Subject: tuple.Subject{Type: "user", ID: "alice"}}
)

func redisClient(t testing.TB) *redis.Client {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; run `docker compose up -d` first")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

func newChecker(t testing.TB, rdb *redis.Client, f *fakeStore) Checker {
	rs, err := rules.Load("../../rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return Checker{
		Uncached:  check.Checker{Reader: f, Rules: rs},
		Revisions: &Revisions{Read: f.Revision},
		Redis:     rdb,
		// A fresh prefix per test keeps runs from reading each other's entries.
		Prefix: fmt.Sprintf("test:%x:", rand.Uint64()),
	}
}

func checkAlice(t *testing.T, c Checker, atLeast int64) Result {
	t.Helper()
	res, err := c.Check(context.Background(), doc, "viewer", alice, atLeast)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRepeatCheckIsServedFromCache(t *testing.T) {
	f := &fakeStore{tuples: []tuple.Tuple{grant}, revision: 1}
	c := newChecker(t, redisClient(t), f)

	first := checkAlice(t, c, 1)
	reads := f.reads
	second := checkAlice(t, c, 1)
	if first.Cached || !second.Cached {
		t.Errorf("cached = %v then %v, want false then true", first.Cached, second.Cached)
	}
	if !second.Allowed || second.Revision != 1 {
		t.Errorf("cached result = %+v, want allowed at revision 1", second)
	}
	if f.reads != reads {
		t.Errorf("cache hit read the store %d more times", f.reads-reads)
	}
}

func TestRevokeIsSeenAtTheDeleteRevision(t *testing.T) {
	f := &fakeStore{tuples: []tuple.Tuple{grant}, revision: 1}
	c := newChecker(t, redisClient(t), f)
	if !checkAlice(t, c, 1).Allowed {
		t.Fatal("alice should start with access")
	}

	f.tuples, f.revision = nil, 2 // the revoke

	if res := checkAlice(t, c, 2); res.Allowed || res.Cached || res.Revision != 2 {
		t.Errorf("check at the revoke's revision = %+v, want a fresh deny at revision 2", res)
	}
}

func TestRecentWithoutStalenessSeesEveryChange(t *testing.T) {
	f := &fakeStore{tuples: []tuple.Tuple{grant}, revision: 1}
	c := newChecker(t, redisClient(t), f)
	if res := checkAlice(t, c, Recent); !res.Allowed || res.Revision != 1 {
		t.Fatalf("first check = %+v, want allow at revision 1", res)
	}
	if res := checkAlice(t, c, Recent); !res.Cached {
		t.Errorf("repeat check with no writes = %+v, want a cache hit", res)
	}

	f.tuples, f.revision = nil, 2

	if res := checkAlice(t, c, Recent); res.Allowed || res.Cached || res.Revision != 2 {
		t.Errorf("check after revoke = %+v, want a fresh deny at revision 2", res)
	}
}

func TestRecentChecksShareASnapshot(t *testing.T) {
	f := &fakeStore{tuples: []tuple.Tuple{grant}, revision: 1}
	c := newChecker(t, redisClient(t), f)
	c.Revisions.MaxStaleness = time.Hour
	checkAlice(t, c, Recent)

	f.revision = 2 // a write somewhere else

	if res := checkAlice(t, c, Recent); !res.Allowed || !res.Cached || res.Revision != 1 {
		t.Errorf("check within the window = %+v, want the cached allow from revision 1", res)
	}

	f.tuples, f.revision = nil, 3 // the revoke

	// at_least still sees the revoke, however stale the snapshot.
	if res := checkAlice(t, c, 3); res.Allowed || res.Cached || res.Revision != 3 {
		t.Errorf("check at the revoke's revision = %+v, want a fresh deny at revision 3", res)
	}
}

func TestSnapshotMovesAfterMaxStaleness(t *testing.T) {
	f := &fakeStore{revision: 1}
	r := &Revisions{Read: f.Revision, MaxStaleness: 50 * time.Millisecond}
	snapshot := func() int64 {
		t.Helper()
		rev, err := r.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return rev
	}
	if got := snapshot(); got != 1 {
		t.Fatalf("first snapshot = %d, want 1", got)
	}
	f.revision = 2
	if got := snapshot(); got != 1 {
		t.Errorf("snapshot within the window = %d, want 1", got)
	}
	time.Sleep(60 * time.Millisecond)
	if got := snapshot(); got != 2 {
		t.Errorf("snapshot after the window = %d, want 2", got)
	}
}

func TestOlderRevisionAcceptsCachedResult(t *testing.T) {
	f := &fakeStore{tuples: []tuple.Tuple{grant}, revision: 1}
	c := newChecker(t, redisClient(t), f)
	checkAlice(t, c, 1)

	f.tuples, f.revision = nil, 2

	// The caller asked for revision 1 or later, and the cached answer is from revision 1.
	if res := checkAlice(t, c, 1); !res.Allowed || !res.Cached || res.Revision != 1 {
		t.Errorf("check at revision 1 = %+v, want the cached allow from revision 1", res)
	}
}

func TestFutureRevisionIsRejected(t *testing.T) {
	c := newChecker(t, redisClient(t), &fakeStore{revision: 1})
	_, err := c.Check(context.Background(), doc, "viewer", alice, 5)
	if !errors.Is(err, ErrFutureRevision) {
		t.Errorf("err = %v, want ErrFutureRevision", err)
	}
}

func TestWithoutRedis(t *testing.T) {
	f := &fakeStore{tuples: []tuple.Tuple{grant}, revision: 1}
	c := newChecker(t, nil, f)
	for range 2 {
		if res := checkAlice(t, c, 1); !res.Allowed || res.Cached {
			t.Errorf("result = %+v, want an uncached allow", res)
		}
	}
}

func TestRedisDownStillAnswers(t *testing.T) {
	f := &fakeStore{tuples: []tuple.Tuple{grant}, revision: 1}
	down := redis.NewClient(&redis.Options{Addr: "localhost:1", MaxRetries: -1})
	t.Cleanup(func() { down.Close() })
	c := newChecker(t, down, f)
	if res := checkAlice(t, c, 1); !res.Allowed || res.Cached {
		t.Errorf("result = %+v, want an uncached allow", res)
	}
}
