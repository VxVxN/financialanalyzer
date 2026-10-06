package ops

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/fetcher"
)

func TestStartFetchRejectsOverlap(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	o := &Ops{
		ctx:    context.Background(),
		logger: slog.New(slog.DiscardHandler),
		runFetch: func(context.Context, fetcher.Request) error {
			close(started)
			<-release
			return nil
		},
	}
	if err := o.StartFetch(fetcher.Request{Tickers: "LKOH", TickersFile: "/secret"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start")
	}
	if err := o.StartFetch(fetcher.Request{}); !errors.Is(err, ErrBusy) {
		t.Errorf("second: %v, want ErrBusy", err)
	}
	close(release)
	waitUntil(t, func() bool { return !o.FetchRunning() })
}

func TestStartRegistryKeepsLastTextOnFailure(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	o := &Ops{
		ctx:    context.Background(),
		logger: slog.New(slog.DiscardHandler),
		buildRegistry: func(_ context.Context, tickers string) (RegistryStatus, error) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
				return RegistryStatus{Tickers: tickers, Text: "NLMK 1 metals", Include: 1, GeneratedAt: &now}, nil
			}
			return RegistryStatus{}, errors.New("girbo down")
		},
	}
	if err := o.StartRegistry("NLMK"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return !o.RegistrySnapshot().Running && o.RegistrySnapshot().Text != "" })
	if err := o.StartRegistry("PHOR"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { st := o.RegistrySnapshot(); return !st.Running && st.Failed })
	st := o.RegistrySnapshot()
	if st.Text != "NLMK 1 metals" || st.Include != 1 || st.Tickers != "PHOR" {
		t.Errorf("status = %+v", st)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}
