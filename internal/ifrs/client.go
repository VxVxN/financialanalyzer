package ifrs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/scraper/httpx"
)

// ErrNotFound means none of the report URLs for that company-year exist.
var ErrNotFound = errors.New("ifrs report not found")

// extraNames are older secids whose filings the CDN still stores under the
// old ticker. X5's reports through 2023 are filed as FIVE; T was TCSG.
var extraNames = map[string][]string{
	"X5": {"FIVE"},
	"T":  {"TCSG"},
}

const maxPDFBytes = 32 << 20

// Client downloads annual IFRS PDFs. Base is the CDN origin without a trailing
// slash; tests point it at httptest.
type Client struct {
	Base string
	HTTP *http.Client
	// extract overrides PDF text extraction in tests.
	extract func([]byte) (string, error)
}

// NewClient talks to the Yandex-hosted CDN that mirrors the filings. It
// answers from Russia, so a run does not need a VPN.
func NewClient() *Client {
	return &Client{
		Base: "https://cdn.financemarker.ru",
		HTTP: &http.Client{Timeout: 45 * time.Second},
	}
}

// Text returns the text of one company-year report, trying the press release
// before the full filing and the current ticker before a former one.
func (c *Client) Text(ctx context.Context, ticker string, year int) (string, error) {
	if c.HTTP == nil {
		c.HTTP = NewClient().HTTP
	}
	extract := c.extract
	if extract == nil {
		extract = extractText
	}
	base := c.Base
	if base == "" {
		base = NewClient().Base
	}
	var lastErr error
	var saw404 bool
	var fallback string
	for _, u := range reportURLs(base, ticker, year) {
		body, err := httpx.Get(ctx, c.HTTP, u, browserHeader(), httpx.Policy{Attempts: 2, BaseDelay: 500 * time.Millisecond, MaxDelay: 2 * time.Second})
		if err != nil {
			var se *httpx.StatusError
			if errors.As(err, &se) && se.Code == http.StatusNotFound {
				saw404 = true
				continue
			}
			lastErr = err
			continue
		}
		if len(body) < 5 || !bytes.HasPrefix(body, []byte("%PDF")) || len(body) > maxPDFBytes {
			continue
		}
		text, err := extract(body)
		if err != nil || strings.TrimSpace(text) == "" {
			continue
		}
		if _, ok := Parse(text); ok {
			return text, nil
		}
		if fallback == "" {
			fallback = text
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	if lastErr != nil && !saw404 {
		return "", lastErr
	}
	return "", ErrNotFound
}

func browserHeader() http.Header {
	h := make(http.Header)
	h.Set("User-Agent", "Mozilla/5.0")
	return h
}

// reportURLs lists the press release first, then the full filing, under the
// current ticker and then a former one. The CDN names recent files in Latin
// ("msfo") and older ones in Cyrillic ("МСФО").
func reportURLs(base, ticker string, year int) []string {
	names := []string{strings.TrimSpace(ticker)}
	key := strings.ToUpper(names[0])
	names = append(names, extraNames[key]...)
	var urls []string
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" {
			continue
		}
		letter := firstLetter(name)
		if letter == "" {
			continue
		}
		upper, lower := strings.ToUpper(name), strings.ToLower(name)
		files := []string{
			fmt.Sprintf("%s_%d_12_Y_МСФО_press.pdf", upper, year),
			fmt.Sprintf("%s_%d_12_y_msfo_press.pdf", lower, year),
			fmt.Sprintf("%s_%d_12_Y_МСФО.pdf", upper, year),
			fmt.Sprintf("%s_%d_12_y_msfo.pdf", lower, year),
		}
		for _, file := range files {
			u := fmt.Sprintf("%s/reports/%d/MOEX/%s/%s", strings.TrimRight(base, "/"), year, letter, url.PathEscape(file))
			if seen[u] {
				continue
			}
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}
