package autosync

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The whole point: a date change triggers exactly one sync, the same date does
// not, and a machine that "wakes" on a later date still syncs for that day
// rather than skipping it. Everything is driven by explicit times, so no real
// day boundary is waited for, and the same-date case is paired with the
// date-change case so a scheduler that never synced at all would fail.
func TestMaybeSyncFiresOncePerChangedDate(t *testing.T) {
	start := time.Date(2026, 9, 27, 23, 59, 0, 0, time.UTC)
	var calls int
	ctx := context.Background()
	s := New(func() time.Time { return start }, func(context.Context) error {
		calls++
		return nil
	})

	// Seeded with the start date, so the same date does not sync.
	if s.MaybeSync(ctx, start) {
		t.Error("MaybeSync on the seeded date = true, want false")
	}
	if calls != 0 {
		t.Fatalf("sync ran %d times on the seeded date, want 0", calls)
	}

	// A new calendar date syncs once.
	day2 := start.Add(2 * time.Minute)
	if !s.MaybeSync(ctx, day2) {
		t.Error("MaybeSync after the date changed = false, want true")
	}
	if calls != 1 {
		t.Fatalf("sync ran %d times after the date changed, want 1", calls)
	}

	// Later the same day does not sync again.
	if s.MaybeSync(ctx, day2.Add(6*time.Hour)) {
		t.Error("MaybeSync later the same date = true, want false")
	}
	if calls != 1 {
		t.Fatalf("sync ran %d times, want still 1", calls)
	}

	// A machine waking hours later on a new date syncs for that day — the
	// sleep-across-midnight case a wall-clock timer would miss.
	day3 := day2.Add(30 * time.Hour)
	if !s.MaybeSync(ctx, day3) {
		t.Error("MaybeSync after waking on a later date = false, want true")
	}
	if calls != 2 {
		t.Fatalf("sync ran %d times, want 2", calls)
	}
}

// A failed refresh is not fatal: it is logged, attempted once for the day (so a
// down upstream is not hammered every tick), and the scheduler keeps running.
func TestMaybeSyncLogsAndDoesNotRetryOnFailure(t *testing.T) {
	start := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	var calls int
	var logged int
	s := New(func() time.Time { return start }, func(context.Context) error {
		calls++
		return errors.New("AdoboTV unreachable")
	})
	s.Logf = func(string, ...any) { logged++ }

	day2 := start.Add(24 * time.Hour)
	if !s.MaybeSync(context.Background(), day2) {
		t.Error("MaybeSync after a date change = false, want true even though the refresh failed")
	}
	if calls != 1 {
		t.Fatalf("sync ran %d times, want 1", calls)
	}
	if logged != 1 {
		t.Errorf("failures logged = %d, want 1", logged)
	}
	// Same day: the failure is not retried on every subsequent tick.
	if s.MaybeSync(context.Background(), day2.Add(time.Hour)) {
		t.Error("MaybeSync later the same date = true, want false")
	}
	if calls != 1 {
		t.Fatalf("sync ran %d times, want the failed day attempted once", calls)
	}
}

// Run must stop and leak no goroutine when its context is cancelled. Run under
// -race with the rest of the suite.
func TestRunStopsOnContextCancel(t *testing.T) {
	s := New(time.Now, func(context.Context) error { return nil })
	s.Interval = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation; goroutine leaked")
	}
}

// A scheduler with no refresh function is inert rather than panicking.
func TestMaybeSyncInertWithoutSyncFunc(t *testing.T) {
	s := New(func() time.Time { return time.Now() }, nil)
	if s.MaybeSync(context.Background(), time.Now().Add(24*time.Hour)) {
		t.Error("MaybeSync with no sync func = true, want false")
	}
}
