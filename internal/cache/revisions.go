package cache

import (
	"context"
	"sync"
	"time"
)

// Revisions picks the revision a default check is evaluated at.
//
// If every default check read the latest revision, any write anywhere would make every
// cached result too old, and with 1% writes almost nothing would hit. Zanzibar instead
// evaluates default checks at a slightly stale snapshot. Here the snapshot is a revision
// read at most MaxStaleness ago: it stays the same for the whole window, so results cached
// during the window keep matching. A caller that needs a recent change, such as its own
// revoke, passes at_least and is never served anything older.
type Revisions struct {
	Read         func(context.Context) (int64, error) // the latest committed revision
	MaxStaleness time.Duration

	mu     sync.Mutex
	rev    int64
	readAt time.Time
}

// Snapshot returns a revision that reflects every change committed up to MaxStaleness ago.
func (r *Revisions) Snapshot(ctx context.Context) (int64, error) {
	// Without the lock, checks don't wait on each other's reads when every one reads anyway.
	if r.MaxStaleness == 0 {
		return r.Read(ctx)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.readAt) > r.MaxStaleness {
		// The time before the read, since changes committed during it may be missing.
		start := time.Now()
		rev, err := r.Read(ctx)
		if err != nil {
			return 0, err
		}
		r.rev, r.readAt = rev, start
	}
	return r.rev, nil
}
