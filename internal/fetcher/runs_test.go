package fetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

type fakeRunStore struct {
	startErr  error
	started   []models.FetchRun
	finished  []models.FetchRun
	finishCtx error // ctx.Err() seen by FinishFetchRun
	sweeps    int
	sweepAge  []time.Duration

	lockErr  error // LockFetch's error
	waits    bool  // LockFetch calls onWait (another fetch holds the lock)
	locks    int
	released int
	events   []string // "lock", "release", "notify" in order
}

func (f *fakeRunStore) LockFetch(ctx context.Context, onWait func()) (func(), error) {
	if f.waits {
		onWait()
	}
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	f.locks++
	f.events = append(f.events, "lock")
	return func() { f.released++; f.events = append(f.events, "release") }, nil
}

func (f *fakeRunStore) StartFetchRun(_ context.Context, run models.FetchRun) (int64, error) {
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.started = append(f.started, run)
	return int64(len(f.started)), nil
}

func (f *fakeRunStore) AbandonStaleRuns(_ context.Context, olderThan time.Duration, _ []string) (int64, error) {
	f.sweeps++
	f.sweepAge = append(f.sweepAge, olderThan)
	return 0, nil
}

func (f *fakeRunStore) FinishFetchRun(ctx context.Context, run models.FetchRun) error {
	f.finishCtx = ctx.Err()
	f.finished = append(f.finished, run)
	return nil
}

func TestRunStatus(t *testing.T) {
	tests := []struct {
		name string
		sum  Summary
		err  error
		want string
	}{
		{"clean", Summary{Updated: 2, QuotesSaved: 3}, nil, models.RunOK},
		{"csv-only quote failures are routine", Summary{QuotesSaved: 3, QuotesFailed: []string{"CSVONLY"}}, nil, models.RunOK},
		{"no quote saved at all", Summary{QuotesFailed: []string{"SBER"}}, nil, models.RunPartial},
		{"company failed", Summary{Failed: []string{"LKOH"}, QuotesSaved: 1}, nil, models.RunPartial},
		{"error", Summary{}, errors.New("db down"), models.RunFailed},
		{"canceled", Summary{}, fmt.Errorf("list: %w", context.Canceled), models.RunCanceled},
		{"empty DB", Summary{}, ErrNoCompanies, models.RunOK},
	}
	for _, tt := range tests {
		if got := runStatus(tt.sum, tt.err); got != tt.want {
			t.Errorf("%s: runStatus = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRecord(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	t0 := time.Date(2026, 9, 30, 4, 0, 0, 0, time.UTC)
	clock := []time.Time{t0, t0.Add(90 * time.Second)}
	now := func() time.Time { v := clock[0]; clock = clock[1:]; return v }

	store := &fakeRunStore{}
	ctx, cancel := context.WithCancel(context.Background())
	req := Request{Banks: "SBER", Force: true}
	rec, err := record(ctx, store, req, TriggerCLI, now, RunOptions{}, logger, func(context.Context) (Summary, error) {
		cancel() // shutdown mid-run: the outcome must still be recorded
		return Summary{Updated: 1, Rows: 4}, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the run's error", err)
	}
	if len(store.started) != 1 || store.started[0].Status != models.RunRunning ||
		store.started[0].Kind != KindFinancials || store.started[0].FullScope || store.started[0].Scope != "banks=SBER force" {
		t.Errorf("started = %+v", store.started)
	}
	if store.sweeps != 1 {
		t.Errorf("stale-run sweeps = %d, want 1", store.sweeps)
	}
	if len(store.finished) != 1 || store.finishCtx != nil {
		t.Fatalf("finished = %+v (ctx err %v), want one record on a live context", store.finished, store.finishCtx)
	}
	got := store.finished[0]
	if got.ID != 1 || got.Status != models.RunCanceled || got.Rows != 4 || got.Duration() != 90*time.Second || got.Error == "" {
		t.Errorf("finished = %+v", got)
	}
	if rec.Status != models.RunCanceled {
		t.Errorf("returned record = %+v", rec)
	}
	if store.locks != 1 || store.released != 1 {
		t.Errorf("lock taken %d, released %d times; want 1/1", store.locks, store.released)
	}
	if len(store.sweepAge) != 1 || store.sweepAge[0] != 0 {
		t.Errorf("sweep ages = %v: under the lock every running row is dead", store.sweepAge)
	}

	// A failed start is logged, the fetch still runs, and no finish is attempted.
	store = &fakeRunStore{startErr: errors.New("no table")}
	ran := false
	clock = []time.Time{t0, t0}
	if _, err := record(context.Background(), store, Request{QuotesOnly: true}, TriggerSchedule, now, RunOptions{}, logger, func(context.Context) (Summary, error) {
		ran = true
		return Summary{}, nil
	}); err != nil || !ran || len(store.finished) != 0 {
		t.Errorf("unrecorded run: err=%v ran=%v finished=%d", err, ran, len(store.finished))
	}
}

type fakeNotifier struct {
	store *fakeRunStore
	texts []string
	err   error
}

func (n *fakeNotifier) Notify(ctx context.Context, text string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	n.texts = append(n.texts, text)
	if n.store != nil {
		n.store.events = append(n.store.events, "notify")
	}
	return n.err
}

func TestRecordLock(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	now := func() time.Time { return time.Date(2026, 9, 30, 4, 0, 0, 0, time.UTC) }
	ok := func(context.Context) (Summary, error) { return Summary{QuotesSaved: 1}, nil }

	// Another fetch holds the lock: this one waits, then runs normally.
	store := &fakeRunStore{waits: true}
	if _, err := record(context.Background(), store, Request{}, TriggerSchedule, now, RunOptions{}, logger, ok); err != nil {
		t.Fatalf("waited run: %v", err)
	}
	if len(store.finished) != 1 || store.finished[0].Status != models.RunOK || store.released != 1 {
		t.Errorf("waited run: finished=%+v released=%d", store.finished, store.released)
	}

	// Canceled while waiting: nothing runs and nothing is recorded.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store = &fakeRunStore{waits: true, lockErr: fmt.Errorf("fetch lock: %w", context.Canceled)}
	ran := false
	rec, err := record(ctx, store, Request{}, TriggerSchedule, now, RunOptions{Notifier: &fakeNotifier{}}, logger, func(context.Context) (Summary, error) {
		ran = true
		return Summary{}, nil
	})
	if !errors.Is(err, context.Canceled) || ran || len(store.started) != 0 || rec.Status != models.RunCanceled {
		t.Errorf("canceled wait: err=%v ran=%v started=%d rec=%+v", err, ran, len(store.started), rec)
	}

	// The lock itself fails (not a cancel): the run must not overlap another
	// one, so it fails without running and is reported.
	store = &fakeRunStore{lockErr: errors.New("fetch lock: too many connections")}
	n := &fakeNotifier{}
	ran = false
	rec, err = record(context.Background(), store, Request{}, TriggerCLI, now, RunOptions{Notifier: n}, logger, func(context.Context) (Summary, error) {
		ran = true
		return Summary{}, nil
	})
	if err == nil || ran || len(store.started) != 0 || rec.Status != models.RunFailed {
		t.Errorf("lock failure: err=%v ran=%v started=%d rec=%+v", err, ran, len(store.started), rec)
	}
	if len(n.texts) != 1 || !strings.Contains(n.texts[0], "too many connections") {
		t.Errorf("lock failure notifications = %q", n.texts)
	}

	// A catch-up that the run it waited for made redundant is skipped under
	// the lock, unrecorded.
	store = &fakeRunStore{waits: true}
	checked := false
	ran = false
	_, err = record(context.Background(), store, Request{}, TriggerCatchUp, now, RunOptions{StillNeeded: func(context.Context) bool {
		checked = store.locks == 1 && store.released == 0
		return false
	}}, logger, func(context.Context) (Summary, error) {
		ran = true
		return Summary{}, nil
	})
	if !errors.Is(err, ErrNotNeeded) || ran || !checked || len(store.started) != 0 || store.released != 1 {
		t.Errorf("not needed: err=%v ran=%v checked under lock=%v started=%d released=%d", err, ran, checked, len(store.started), store.released)
	}
}

func TestRecordNotifies(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	t0 := time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC) // 05:00 MSK
	tests := []struct {
		name   string
		req    Request
		sum    Summary
		err    error
		notify bool
		want   []string // substrings of the message
	}{
		{"ok is silent", Request{}, Summary{Updated: 1, QuotesSaved: 3}, nil, false, nil},
		{"canceled is silent", Request{}, Summary{}, context.Canceled, false, nil},
		{"empty DB is silent", Request{}, Summary{}, ErrNoCompanies, false, nil},
		{"partial", Request{}, Summary{Updated: 2, UpToDate: 5, QuotesSaved: 7, Failed: []string{"LKOH", "T"}}, nil, true,
			[]string{"Обновление данных: частично", "Отчётность и котировки, по расписанию", "30.09.2026 05:00 МСК",
				"Обновлено компаний: 2, без изменений: 5", "Не удалось обновить: LKOH, T"}},
		{"failed", Request{Tickers: "OZON"}, Summary{}, errors.New("list companies: connection refused"), true,
			[]string{"Обновление данных: ошибка", "(tickers=OZON)", "Ошибка: list companies: connection refused"}},
		{"no quote saved", Request{QuotesOnly: true}, Summary{QuotesFailed: []string{"SBER", "GAZP"}}, nil, true,
			[]string{"Котировки, по расписанию", "Ни одной котировки, ошибки по: SBER, GAZP"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeRunStore{}
			n := &fakeNotifier{store: store, err: errors.New("telegram down")} // a send error is only logged
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, _ = record(ctx, store, tt.req, TriggerSchedule, func() time.Time { return t0 }, RunOptions{Notifier: n}, logger, func(context.Context) (Summary, error) {
				if errors.Is(tt.err, context.Canceled) {
					cancel()
				}
				return tt.sum, tt.err
			})
			if !tt.notify {
				if len(n.texts) != 0 {
					t.Errorf("notified %q", n.texts)
				}
				return
			}
			if len(n.texts) != 1 {
				t.Fatalf("%d notifications, want 1", len(n.texts))
			}
			for _, w := range tt.want {
				if !strings.Contains(n.texts[0], w) {
					t.Errorf("message %q lacks %q", n.texts[0], w)
				}
			}
			if got := strings.Join(store.events, ","); got != "lock,release,notify" {
				t.Errorf("events = %s, want the lock released before notifying", got)
			}
		})
	}
}

func TestRequestScope(t *testing.T) {
	tests := []struct {
		req   Request
		kind  string
		full  bool
		scope string
	}{
		{Request{}, KindFinancials, true, "stored"},
		{Request{QuotesOnly: true}, KindQuotes, true, "stored"},
		{Request{All: true}, KindFinancials, true, "registry"},
		{Request{Tickers: "OZON,X5", Banks: "T"}, KindFinancials, false, "tickers=OZON,X5 banks=T"},
		{Request{TickersFile: "/home/me/l.txt", Force: true}, KindFinancials, false, "file=l.txt force"},
		{Request{Backfill: true}, KindFinancials, true, "stored backfill"},
		{Request{Force: true, Backfill: true}, KindFinancials, true, "stored force"},
	}
	for _, tt := range tests {
		if tt.req.Kind() != tt.kind || tt.req.FullScope() != tt.full || tt.req.Scope() != tt.scope {
			t.Errorf("%+v: kind=%q full=%v scope=%q", tt.req, tt.req.Kind(), tt.req.FullScope(), tt.req.Scope())
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		saved     int
		saveErrs  int
		sourceErr bool
		known     bool
		want      outcome
	}{
		{"new rows", 3, 0, false, true, outcomeUpdated},
		{"nothing new", 0, 0, false, true, outcomeUpToDate},
		{"unknown to the source", 0, 0, false, false, outcomeFailed},
		{"source outage hides behind stored rows", 0, 0, true, true, outcomeFailed},
		{"some years failed, others saved", 2, 0, true, true, outcomeFailed},
		{"every save failed", 0, 4, false, true, outcomeFailed},
		{"one save failed", 3, 1, false, true, outcomeFailed},
	}
	for _, tt := range tests {
		if got := classify(tt.saved, tt.saveErrs, tt.sourceErr, tt.known); got != tt.want {
			t.Errorf("%s: classify = %v, want %v", tt.name, got, tt.want)
		}
	}
}
