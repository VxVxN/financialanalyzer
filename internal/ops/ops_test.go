package ops

import (
	"context"
	"errors"
	"log/slog"
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
