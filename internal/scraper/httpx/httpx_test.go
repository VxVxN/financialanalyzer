package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var fast = Policy{Attempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}

// server answers with statuses[i] on the i-th request (the last one repeats)
// and counts the requests it received.
func server(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		code := statuses[min(i, len(statuses)-1)]
		if r.Header.Get("X-Test") != "yes" {
			code = http.StatusBadRequest
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte("body"))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

var hdr = http.Header{"X-Test": {"yes"}}

func TestGet(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []int
		policy    Policy
		wantErr   bool
		wantCode  int // StatusError code expected via errors.As; 0 = none
		wantCalls int32
	}{
		{"ok first try", []int{200}, fast, false, 0, 1},
		{"5xx then ok", []int{503, 502, 200}, fast, false, 0, 3},
		{"429 then ok", []int{429, 200}, fast, false, 0, 2},
		{"5xx exhausts attempts", []int{500}, fast, true, 500, 3},
		{"404 not retried", []int{404, 200}, fast, true, 404, 1},
		{"zero policy = one attempt", []int{503, 200}, Policy{}, true, 503, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, calls := server(t, tt.statuses...)
			body, err := Get(context.Background(), srv.Client(), srv.URL, hdr, tt.policy)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && string(body) != "body" {
				t.Errorf("body = %q", body)
			}
			if tt.wantCode != 0 {
				var se *StatusError
				if !errors.As(err, &se) || se.Code != tt.wantCode {
					t.Errorf("err = %v, want StatusError %d", err, tt.wantCode)
				}
			}
			if got := calls.Load(); got != tt.wantCalls {
				t.Errorf("calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

// TestGetRetriesTransportError covers the case seen with MOEX ISS: the server
// accepts the connection but never answers, so the client times out.
func TestGetRetriesTransportError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			time.Sleep(100 * time.Millisecond) // longer than the client timeout
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 20 * time.Millisecond}
	body, err := Get(context.Background(), client, srv.URL, nil, fast)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != "ok" || n.Load() != 2 {
		t.Errorf("body=%q calls=%d, want ok after 2 calls", body, n.Load())
	}
}

func TestGetStopsOnContextCancel(t *testing.T) {
	srv, calls := server(t, 503)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Get(ctx, srv.Client(), srv.URL, hdr, Policy{Attempts: 5, BaseDelay: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls.Load() > 1 {
		t.Errorf("calls = %d, want at most 1", calls.Load())
	}
}

func TestBackoffAndRetryAfter(t *testing.T) {
	p := Policy{BaseDelay: 100 * time.Millisecond, MaxDelay: 300 * time.Millisecond}
	for attempt, want := range map[int]time.Duration{1: 100, 2: 200, 3: 300, 10: 300} {
		d := backoff(p, attempt)
		lo, hi := want*time.Millisecond*9/10, want*time.Millisecond*11/10
		if d < lo || d > hi {
			t.Errorf("backoff(attempt %d) = %v, want within [%v, %v]", attempt, d, lo, hi)
		}
	}
	if got := parseRetryAfter("7"); got != 7*time.Second {
		t.Errorf("parseRetryAfter(7) = %v", got)
	}
	for _, v := range []string{"", "-1", "Wed, 21 Oct 2015 07:28:00 GMT"} {
		if got := parseRetryAfter(v); got != 0 {
			t.Errorf("parseRetryAfter(%q) = %v, want 0", v, got)
		}
	}
}

// TestGetRetryAfter: a 429's Retry-After is waited out when within MaxDelay and
// ends the retries (no hammering) when it is longer.
func TestGetRetryAfter(t *testing.T) {
	newSrv := func(retryAfter string) (*httptest.Server, *atomic.Int32) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n.Add(1) == 1 {
				w.Header().Set("Retry-After", retryAfter)
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte("ok"))
		}))
		t.Cleanup(srv.Close)
		return srv, &n
	}

	srv, calls := newSrv("1")
	start := time.Now()
	body, err := Get(context.Background(), srv.Client(), srv.URL, nil,
		Policy{Attempts: 2, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Second})
	if err != nil || string(body) != "ok" || calls.Load() != 2 {
		t.Fatalf("within MaxDelay: body=%q err=%v calls=%d, want ok after 2 calls", body, err, calls.Load())
	}
	if waited := time.Since(start); waited < 900*time.Millisecond {
		t.Errorf("waited %v, want ~1s from Retry-After", waited)
	}

	srv, calls = newSrv("60")
	_, err = Get(context.Background(), srv.Client(), srv.URL, nil,
		Policy{Attempts: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusTooManyRequests || calls.Load() != 1 {
		t.Errorf("beyond MaxDelay: err=%v calls=%d, want the 429 after 1 call", err, calls.Load())
	}
}

// TestGetCancelDuringBackoff: cancelling while waiting between attempts returns
// promptly with context.Canceled and keeps the triggering error in the message.
func TestGetCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	go func() {
		<-hit
		time.Sleep(100 * time.Millisecond) // the 503 is back; the client now sleeps an hour
		cancel()
	}()

	start := time.Now()
	_, err := Get(ctx, srv.Client(), srv.URL, nil, Policy{Attempts: 3, BaseDelay: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Errorf("err = %v, want the 503 cause kept", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("Get did not return promptly after cancel")
	}
}

func TestBackoffHugeAttempt(t *testing.T) {
	p := Policy{BaseDelay: time.Second, MaxDelay: 5 * time.Second}
	if d := backoff(p, 100); d < 4*time.Second || d > 6*time.Second {
		t.Errorf("backoff(100) = %v, want ~MaxDelay (no overflow to 0)", d)
	}
}
