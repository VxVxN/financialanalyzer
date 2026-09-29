// Package httpx is the shared GET-with-retry helper used by the scraper
// clients (girbo, moex, cbr). Each client keeps its own rate limit
// and headers; httpx only adds bounded retries with exponential backoff for
// transient failures — network errors, timeouts, 429 and 5xx — so one flaky
// response no longer drops a whole company-year.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// Policy bounds the retries of a single Get. The zero value means "one attempt,
// no retries".
type Policy struct {
	Attempts  int           // total attempts including the first; <1 is treated as 1
	BaseDelay time.Duration // backoff before the 2nd attempt, doubled each retry
	MaxDelay  time.Duration // cap on a single backoff; a longer Retry-After ends the retries. 0 = no cap
}

// DefaultPolicy is used by the scraper clients: 3 attempts, 1s then 2s backoff.
var DefaultPolicy = Policy{Attempts: 3, BaseDelay: time.Second, MaxDelay: 10 * time.Second}

// StatusError is returned for a non-200 response. Callers can match a specific
// code with errors.As (e.g. cbr maps 404 to ErrNotPublished).
type StatusError struct {
	Code int
	URL  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("status %d for %s", e.Code, e.URL)
}

// Get performs a GET with the given headers and returns the full body of a 200
// response. Transient failures are retried per policy; a non-retryable status
// (e.g. 404) or context cancellation returns immediately.
func Get(ctx context.Context, client *http.Client, url string, header http.Header, p Policy) ([]byte, error) {
	attempts := max(p.Attempts, 1)
	var lastErr error
	for attempt := 1; ; attempt++ {
		body, retryAfter, err := getOnce(ctx, client, url, header)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if attempt >= attempts || !retryable(ctx, err) {
			break
		}
		wait := backoff(p, attempt)
		if retryAfter > 0 {
			if p.MaxDelay > 0 && retryAfter > p.MaxDelay {
				// Retrying before the server allows only earns more 429s.
				return nil, fmt.Errorf("server asked to retry after %v: %w", retryAfter, lastErr)
			}
			wait = retryAfter
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (last error: %v)", ctx.Err(), lastErr)
		}
	}
	if attempts > 1 && retryable(ctx, lastErr) {
		return nil, fmt.Errorf("after %d attempts: %w", attempts, lastErr)
	}
	return nil, lastErr
}

// getOnce is a single attempt. The body is read inside the attempt so a
// connection dropped mid-download is retried too. retryAfter is the parsed
// Retry-After of a 429/503 (0 when absent).
func getOnce(ctx context.Context, client *http.Client, url string, header http.Header) ([]byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range header {
		req.Header[k] = v
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain a little so the connection can be reused.
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return nil, parseRetryAfter(resp.Header.Get("Retry-After")), &StatusError{Code: resp.StatusCode, URL: url}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read body of %s: %w", url, err)
	}
	return body, 0, nil
}

// retryable reports whether err is worth another attempt: any transport error
// (connection refused/reset, TLS handshake timeout, client timeout) unless the
// caller's context is done, plus 429 and 5xx statuses.
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == http.StatusTooManyRequests || se.Code >= 500
	}
	return true
}

// backoff returns BaseDelay * 2^(attempt-1) with ±10% jitter, capped at MaxDelay.
func backoff(p Policy, attempt int) time.Duration {
	if p.BaseDelay <= 0 {
		return 0
	}
	d := p.BaseDelay << min(attempt-1, 30) // bounded shift: no overflow to <= 0
	if d <= 0 || (p.MaxDelay > 0 && d > p.MaxDelay) {
		d = p.MaxDelay
	}
	if d <= 0 {
		return 0
	}
	jitter := time.Duration(rand.Int64N(int64(d)/5+1)) - d/10
	return d + jitter
}

// parseRetryAfter understands the delay-seconds form of Retry-After (the one
// APIs actually send); an HTTP-date or garbage yields 0.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
