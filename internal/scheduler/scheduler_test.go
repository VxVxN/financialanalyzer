package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/fetcher"
)

func msk(y int, mo time.Month, d, h, m int) time.Time {
	return time.Date(y, mo, d, h, m, 0, 0, Moscow)
}

func TestParseSpec(t *testing.T) {
	good := map[string]Spec{
		"07:00":     {Hour: 7},
		" 23:59 ":   {Hour: 23, Minute: 59},
		"sun 05:30": {Weekly: true, Weekday: time.Sunday, Hour: 5, Minute: 30},
		"MON 00:00": {Weekly: true, Weekday: time.Monday},
	}
	for in, want := range good {
		got, err := ParseSpec(in)
		if err != nil || got != want {
			t.Errorf("ParseSpec(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "7", "24:00", "07:60", "7:5", "sunday 05:00", "sun 05:00 x", "-1:00", "07:0x"} {
		if _, err := ParseSpec(in); err == nil {
			t.Errorf("ParseSpec(%q) accepted", in)
		}
	}
	if s, _ := ParseSpec("sun 05:00"); s.String() != "sun 05:00" {
		t.Errorf("String = %q", s.String())
	}
}

func TestSpecNextPrev(t *testing.T) {
	daily := Spec{Hour: 7}
	weekly := Spec{Weekly: true, Weekday: time.Sunday, Hour: 5}
	// 2026-09-30 is a Wednesday.
	tests := []struct {
		spec       Spec
		at         time.Time
		next, prev time.Time
	}{
		{daily, msk(2026, 9, 30, 6, 59), msk(2026, 9, 30, 7, 0), msk(2026, 9, 29, 7, 0)},
		{daily, msk(2026, 9, 30, 7, 0), msk(2026, 10, 1, 7, 0), msk(2026, 9, 30, 7, 0)}, // at a slot: next is strictly after
		{daily, msk(2026, 12, 31, 23, 0), msk(2027, 1, 1, 7, 0), msk(2026, 12, 31, 7, 0)},
		{weekly, msk(2026, 9, 30, 12, 0), msk(2026, 10, 4, 5, 0), msk(2026, 9, 27, 5, 0)},
		{weekly, msk(2026, 10, 4, 4, 59), msk(2026, 10, 4, 5, 0), msk(2026, 9, 27, 5, 0)},
		{weekly, msk(2026, 10, 4, 5, 1), msk(2026, 10, 11, 5, 0), msk(2026, 10, 4, 5, 0)},
		// Input in another zone: slots are Moscow wall-clock.
		{daily, time.Date(2026, 9, 30, 3, 30, 0, 0, time.UTC), msk(2026, 9, 30, 7, 0), msk(2026, 9, 29, 7, 0)},
	}
	for _, tt := range tests {
		if got := tt.spec.Next(tt.at); !got.Equal(tt.next) {
			t.Errorf("%v.Next(%v) = %v, want %v", tt.spec, tt.at, got, tt.next)
		}
		if got := tt.spec.Prev(tt.at); !got.Equal(tt.prev) {
			t.Errorf("%v.Prev(%v) = %v, want %v", tt.spec, tt.at, got, tt.prev)
		}
	}
}

// fakeStore answers LastCompletedRun from the runs the fake RunFunc recorded.
type fakeStore struct {
	completed       map[string]time.Time // kind -> latest completed full-scope start
	lookupErr       error
	abandoned       int
	abandonTriggers []string
	// doneWhileWaiting lists run kinds that a cmd/fetch run, holding the fetch
	// lock, completes while the scheduler's catch-up of that kind waits.
	doneWhileWaiting map[string]bool
}

func (f *fakeStore) LastCompletedRun(_ context.Context, kinds []string) (time.Time, bool, error) {
	if f.lookupErr != nil {
		return time.Time{}, false, f.lookupErr
	}
	var best time.Time
	for _, k := range kinds {
		if t, ok := f.completed[k]; ok && t.After(best) {
			best = t
		}
	}
	return best, !best.IsZero(), nil
}

func (f *fakeStore) AbandonStaleRuns(_ context.Context, _ time.Duration, triggers []string) (int64, error) {
	f.abandoned++
	f.abandonTriggers = triggers
	return 0, nil
}

type call struct {
	kind, trigger string
	at            time.Time
}

// runScheduler drives a Scheduler on a fake clock from start: sleep jumps the
// clock, each fetch takes 3 minutes and (unless fail) records a completed run
// in store. It stops after stopAfter fetches and returns them.
func runScheduler(t *testing.T, start time.Time, store *fakeStore, stopAfter int, fail bool) []call {
	t.Helper()
	clock := start
	var calls []call
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	run := func(ctx context.Context, req fetcher.Request, trigger string, stillNeeded func(context.Context) bool) error {
		if stillNeeded != nil {
			if store.doneWhileWaiting[req.Kind()] {
				store.completed[req.Kind()] = clock
				clock = clock.Add(10 * time.Minute)
			}
			if !stillNeeded(ctx) {
				calls = append(calls, call{req.Kind(), "skipped " + trigger, clock})
				if len(calls) >= stopAfter {
					cancel()
				}
				return fetcher.ErrNotNeeded
			}
		}
		calls = append(calls, call{req.Kind(), trigger, clock})
		clock = clock.Add(3 * time.Minute)
		if len(calls) >= stopAfter {
			cancel()
		}
		if fail {
			return errors.New("sources down")
		}
		if store.completed == nil {
			store.completed = map[string]time.Time{}
		}
		store.completed[req.Kind()] = calls[len(calls)-1].at
		return nil
	}
	s := New([]Job{QuotesJob(Spec{Hour: 7}), FinancialsJob(Spec{Weekly: true, Weekday: time.Sunday, Hour: 5})},
		store, run, slog.New(slog.DiscardHandler))
	s.now = func() time.Time { return clock }
	s.sleep = func(ctx context.Context, until time.Time) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if until.After(clock) {
			clock = until
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop")
	}
	if store.abandoned != 1 || strings.Join(store.abandonTriggers, ",") != "schedule,catchup" {
		t.Errorf("AbandonStaleRuns called %d times with %v, want once with the scheduler's triggers", store.abandoned, store.abandonTriggers)
	}
	return calls
}

func assertCalls(t *testing.T, got []call, want []call) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].kind != want[i].kind || got[i].trigger != want[i].trigger || !got[i].at.Equal(want[i].at) {
			t.Errorf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Wednesday noon, both jobs ran on time: nothing to catch up; then the daily
// quotes fire at 07:00 until Sunday, when the 05:00 financials come first.
func TestSchedulerRunsAtSlots(t *testing.T) {
	start := msk(2026, 9, 30, 12, 0) // Wednesday
	store := &fakeStore{completed: map[string]time.Time{
		fetcher.KindQuotes:     msk(2026, 9, 30, 7, 0),
		fetcher.KindFinancials: msk(2026, 9, 27, 5, 0),
	}}
	got := runScheduler(t, start, store, 6, false)
	sched := fetcher.TriggerSchedule
	assertCalls(t, got, []call{
		{fetcher.KindQuotes, sched, msk(2026, 10, 1, 7, 0)},
		{fetcher.KindQuotes, sched, msk(2026, 10, 2, 7, 0)},
		{fetcher.KindQuotes, sched, msk(2026, 10, 3, 7, 0)},
		{fetcher.KindFinancials, sched, msk(2026, 10, 4, 5, 0)},
		{fetcher.KindQuotes, sched, msk(2026, 10, 4, 7, 0)},
		{fetcher.KindQuotes, sched, msk(2026, 10, 5, 7, 0)},
	})
}

// Down since last Friday: financials missed Sunday's slot and quotes today's.
// The financials catch-up refreshes quotes too, so quotes are not caught up
// separately.
func TestSchedulerCatchUpFinancialsCoversQuotes(t *testing.T) {
	start := msk(2026, 9, 30, 12, 0)
	store := &fakeStore{completed: map[string]time.Time{
		fetcher.KindQuotes:     msk(2026, 9, 25, 7, 0),
		fetcher.KindFinancials: msk(2026, 9, 20, 5, 0),
	}}
	got := runScheduler(t, start, store, 2, false)
	settled := start.Add(defaultStartupDelay)
	assertCalls(t, got, []call{
		{fetcher.KindFinancials, fetcher.TriggerCatchUp, settled},
		{fetcher.KindQuotes, fetcher.TriggerSchedule, msk(2026, 10, 1, 7, 0)},
	})
}

// A manual cmd/fetch of the financials holds the fetch lock when the server
// starts: the financials catch-up waits for it and, re-checked under the lock,
// is skipped, and so is the quotes catch-up it covered.
func TestSchedulerCatchUpSkippedAfterWait(t *testing.T) {
	start := msk(2026, 9, 30, 12, 0)
	store := &fakeStore{
		completed: map[string]time.Time{
			fetcher.KindQuotes:     msk(2026, 9, 25, 7, 0),
			fetcher.KindFinancials: msk(2026, 9, 20, 5, 0),
		},
		doneWhileWaiting: map[string]bool{fetcher.KindFinancials: true},
	}
	got := runScheduler(t, start, store, 2, false)
	assertCalls(t, got, []call{
		{fetcher.KindFinancials, "skipped " + fetcher.TriggerCatchUp, start.Add(defaultStartupDelay + 10*time.Minute)},
		{fetcher.KindQuotes, fetcher.TriggerSchedule, msk(2026, 10, 1, 7, 0)},
	})
}

// Only today's quotes are missing; on a fresh database (no runs at all) one
// financials catch-up satisfies both jobs.
func TestSchedulerCatchUpQuotesOnly(t *testing.T) {
	start := msk(2026, 9, 30, 12, 0)
	store := &fakeStore{completed: map[string]time.Time{
		fetcher.KindQuotes:     msk(2026, 9, 29, 7, 0),
		fetcher.KindFinancials: msk(2026, 9, 27, 5, 0),
	}}
	got := runScheduler(t, start, store, 1, false)
	assertCalls(t, got, []call{{fetcher.KindQuotes, fetcher.TriggerCatchUp, start.Add(defaultStartupDelay)}})

	got = runScheduler(t, start, &fakeStore{}, 2, false)
	assertCalls(t, got, []call{
		{fetcher.KindFinancials, fetcher.TriggerCatchUp, start.Add(defaultStartupDelay)},
		{fetcher.KindQuotes, fetcher.TriggerSchedule, msk(2026, 10, 1, 7, 0)},
	})
}

// A failed run is not retried before its next slot, and a lookup error skips
// catch-up rather than guessing.
func TestSchedulerFailuresWaitForNextSlot(t *testing.T) {
	start := msk(2026, 9, 30, 12, 0)
	got := runScheduler(t, start, &fakeStore{lookupErr: errors.New("db down")}, 2, true)
	sched := fetcher.TriggerSchedule
	assertCalls(t, got, []call{
		{fetcher.KindQuotes, sched, msk(2026, 10, 1, 7, 0)},
		{fetcher.KindQuotes, sched, msk(2026, 10, 2, 7, 0)},
	})
}

func TestSchedulerStatus(t *testing.T) {
	s := New([]Job{QuotesJob(Spec{Hour: 7})}, &fakeStore{}, nil, slog.New(slog.DiscardHandler))
	s.now = func() time.Time { return msk(2026, 9, 30, 12, 0) }
	st := s.Status()
	if len(st) != 1 || st[0].Name != fetcher.KindQuotes || st[0].Schedule != "07:00" || st[0].Hour != 7 || st[0].Weekly ||
		!st[0].Next.Equal(msk(2026, 10, 1, 7, 0)) || st[0].Running {
		t.Errorf("Status = %+v", st)
	}
}

// Started 15 s before the 07:00 slot with yesterday's quotes: the slot passes
// during the startup delay, the catch-up covers it, and the job must not fire
// again right after (the next run is tomorrow).
func TestSchedulerNoDuplicateAfterCatchUp(t *testing.T) {
	start := msk(2026, 9, 30, 6, 59).Add(45 * time.Second)
	store := &fakeStore{completed: map[string]time.Time{
		fetcher.KindQuotes:     msk(2026, 9, 29, 7, 0),
		fetcher.KindFinancials: msk(2026, 9, 27, 5, 0),
	}}
	got := runScheduler(t, start, store, 2, false)
	assertCalls(t, got, []call{
		{fetcher.KindQuotes, fetcher.TriggerCatchUp, start.Add(defaultStartupDelay)},
		{fetcher.KindQuotes, fetcher.TriggerSchedule, msk(2026, 10, 1, 7, 0)},
	})
}

// Sunday 04:58, financials missed: the catch-up overruns the 05:00 slot and
// must not be followed by a second financials run; quotes come at 07:00.
func TestSchedulerCatchUpOverrunsSlot(t *testing.T) {
	start := msk(2026, 10, 4, 4, 58)
	store := &fakeStore{completed: map[string]time.Time{
		fetcher.KindQuotes:     msk(2026, 10, 3, 7, 0),
		fetcher.KindFinancials: msk(2026, 9, 20, 5, 0),
	}}
	got := runScheduler(t, start, store, 3, false)
	assertCalls(t, got, []call{
		{fetcher.KindFinancials, fetcher.TriggerCatchUp, start.Add(defaultStartupDelay)},
		{fetcher.KindQuotes, fetcher.TriggerSchedule, msk(2026, 10, 4, 7, 0)},
		{fetcher.KindQuotes, fetcher.TriggerSchedule, msk(2026, 10, 5, 7, 0)},
	})
}

// sleepUntil re-checks the wall clock: a clock that is already past the
// target returns at once, and cancellation interrupts the wait.
func TestSleepUntil(t *testing.T) {
	s := New(nil, &fakeStore{}, nil, slog.New(slog.DiscardHandler))
	now := msk(2026, 9, 30, 12, 0)
	s.now = func() time.Time { return now }
	if err := s.sleepUntil(context.Background(), now.Add(-time.Minute)); err != nil {
		t.Errorf("past target: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.sleepUntil(ctx, now.Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled wait: %v", err)
	}
}
