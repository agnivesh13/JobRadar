package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// LeaderLock is leader election on a Postgres advisory lock. The lock
// belongs to one database session: whoever holds it is the leader, and if
// that process dies its connection closes and Postgres frees the lock, so a
// standby takes over on its next attempt. No timeouts or clocks to tune.
type LeaderLock struct {
	url  string
	id   int64
	conn *pgx.Conn // the session that owns the lock; nil when not leader
}

func NewLeaderLock(databaseURL string, id int64) *LeaderLock {
	return &LeaderLock{url: databaseURL, id: id}
}

// Hold reports whether this process is the leader right now, trying to
// become it if it is not. Call it before every piece of leader-only work.
func (l *LeaderLock) Hold(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if l.conn != nil {
		// Still leader only while the session that took the lock is alive.
		if l.conn.Ping(ctx) == nil {
			return true
		}
		l.conn.Close(context.Background())
		l.conn = nil
	}
	conn, err := pgx.Connect(ctx, l.url)
	if err != nil {
		return false
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, l.id).Scan(&got); err != nil || !got {
		conn.Close(context.Background())
		return false
	}
	l.conn = conn
	return true
}

// Release gives up leadership (on shutdown), so a standby takes over at once.
func (l *LeaderLock) Release() {
	if l.conn != nil {
		l.conn.Close(context.Background())
		l.conn = nil
	}
}
