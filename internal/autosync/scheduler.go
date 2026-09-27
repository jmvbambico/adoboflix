// Package autosync refreshes the active source's library once a day, so the
// first page load of the day is served from a warm cache.
//
// It exists because a subscribed source caches its library with a short TTL and
// otherwise only fetches on a read. Booting and reading already fetch; the one
// gap the daily refresh fills is the first read after midnight, which would
// otherwise pay a cold fetch on the user's first click of the day.
package autosync

import (
	"context"
	"log"
	"sync"
	"time"
)

// defaultInterval is how often Run re-checks whether the calendar date has
// changed. One minute is a cheap poll — the check is a string compare — and it
// bounds how long after waking the machine notices a new day.
const defaultInterval = time.Minute

// dateLayout is the local calendar date a refresh is attributed to.
const dateLayout = "2006-01-02"

// Scheduler triggers a daily refresh when the local calendar date changes.
//
// It deliberately does NOT arm a timer for the next midnight. This runs in a
// local desktop app on a machine that sleeps, and a long time.Timer is not a
// reliable wall-clock alarm across suspend: a laptop suspended across midnight
// would either fire the timer late or not at all and skip the day. Instead it
// re-checks on a short interval whether the calendar date has changed since the
// last refresh, so a machine that wakes at 09:00 refreshes then rather than
// skipping the day. The poll costs one time comparison a minute.
type Scheduler struct {
	// Now is the time source. Nil means time.Now. It is injectable so the
	// date-change logic is exercised without waiting for real time.
	Now func() time.Time
	// Interval is how often Run re-checks the date. Zero or negative means
	// defaultInterval.
	Interval time.Duration
	// Sync refreshes the source. Nil makes the scheduler inert.
	Sync func(ctx context.Context) error
	// Logf reports a failed refresh. Nil means log.Printf.
	Logf func(format string, args ...any)

	mu sync.Mutex
	// lastDate is the calendar date of the last refresh attempt, so the same
	// day is not attempted twice and a new day is.
	lastDate string
}

// New builds a scheduler seeded with the current date, so starting the server
// does not immediately fire a refresh — boot already fetches on first read, and
// the point of the daily refresh is the transition to a new day. syncFn is the
// refresh to run; a nil syncFn makes the scheduler inert.
func New(now func() time.Time, syncFn func(context.Context) error) *Scheduler {
	if now == nil {
		now = time.Now
	}
	return &Scheduler{
		Now:      now,
		Interval: defaultInterval,
		Sync:     syncFn,
		lastDate: now().Format(dateLayout),
	}
}

// MaybeSync refreshes the source when now falls on a different calendar date
// than the last attempt, and reports whether it attempted a refresh.
//
// The date is recorded BEFORE the refresh runs, so a failed refresh is logged
// once for that day rather than retried on every tick — a full day of
// one-minute retries would hammer upstream for no benefit. A failure is not an
// error state for the app: the cache is left as it was, and the source's own
// TTL still fetches on any real read, so the worst case is that the day's
// warm-up was skipped.
func (s *Scheduler) MaybeSync(ctx context.Context, now time.Time) bool {
	if s.Sync == nil {
		return false
	}
	date := now.Format(dateLayout)

	s.mu.Lock()
	if date == s.lastDate {
		s.mu.Unlock()
		return false
	}
	s.lastDate = date
	s.mu.Unlock()

	if err := s.Sync(ctx); err != nil {
		s.logf("source sync: daily refresh failed: %v (library left as it was; the next read refreshes on the cache TTL)", err)
	}
	return true
}

// Run refreshes on each date change until ctx is cancelled, then returns. The
// ticker is stopped on the way out and the goroutine is not left behind, so the
// caller can wait on Run's completion for a clean shutdown.
func (s *Scheduler) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.MaybeSync(ctx, s.now())
		}
	}
}

// now returns the configured clock, defaulting to time.Now.
func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// logf reports through the configured logger.
func (s *Scheduler) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}
