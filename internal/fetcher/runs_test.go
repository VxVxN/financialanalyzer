package fetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
}

func (f *fakeRunStore) StartFetchRun(_ context.Context, run models.FetchRun) (int64, error) {
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.started = append(f.started, run)
	return int64(len(f.started)), nil
}

func (f *fakeRunStore) AbandonStaleRuns(context.Context, time.Duration, []string) (int64, error) {
	f.sweeps++
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
	rec, err := record(ctx, store, req, TriggerCLI, now, logger, func(context.Context) (Summary, error) {
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

	// A failed start is logged, the fetch still runs, and no finish is attempted.
	store = &fakeRunStore{startErr: errors.New("no table")}
	ran := false
	clock = []time.Time{t0, t0}
	if _, err := record(context.Background(), store, Request{QuotesOnly: true}, TriggerSchedule, now, logger, func(context.Context) (Summary, error) {
		ran = true
		return Summary{}, nil
	}); err != nil || !ran || len(store.finished) != 0 {
		t.Errorf("unrecorded run: err=%v ran=%v finished=%d", err, ran, len(store.finished))
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
