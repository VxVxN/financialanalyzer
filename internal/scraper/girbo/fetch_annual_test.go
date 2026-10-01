package girbo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/scraper/httpx"
)

// fakeGirbo serves the LUKOIL fixtures: search, the five-year report list, and
// the same details payload for every year except those in failing (500).
func fakeGirbo(t *testing.T, failing map[string]bool) (*Client, *[]string) {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		return b
	}
	search, list, details := read("lukoil_search.json"), read("lukoil_bfo_list.json"), read("lukoil_details.json")
	var detailCalls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/advanced-search/"):
			_, _ = w.Write(search)
		case strings.HasSuffix(r.URL.Path, "/bfo"):
			_, _ = w.Write(list)
		case strings.HasSuffix(r.URL.Path, "/details"):
			id := strings.Split(r.URL.Path, "/")[3]
			detailCalls = append(detailCalls, id)
			if failing[id] {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(details)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient()
	c.BaseURL, c.Delay, c.Retry = srv.URL, 0, httpx.Policy{Attempts: 1}
	return c, &detailCalls
}

func years(reps []AnnualReport) []int {
	out := make([]int, len(reps))
	for i, r := range reps {
		out[i] = r.Year
	}
	return out
}

func TestFetchAnnualSkipsStoredYears(t *testing.T) {
	c, calls := fakeGirbo(t, nil)
	reps, err := c.FetchAnnual(context.Background(), "7708004767", func(y int) bool { return y <= 2023 })
	if err != nil {
		t.Fatalf("FetchAnnual: %v", err)
	}
	if got := years(reps); len(got) != 2 || got[0] != 2025 || got[1] != 2024 {
		t.Errorf("years = %v, want [2025 2024]", got)
	}
	if len(*calls) != 2 {
		t.Errorf("detail calls = %v, want only the two unskipped years", *calls)
	}
}

func TestFetchAnnualPartialFailure(t *testing.T) {
	// 2025 (27306568) fails; the other four years parse.
	c, _ := fakeGirbo(t, map[string]bool{"27306568": true})
	reps, err := c.FetchAnnual(context.Background(), "7708004767", nil)
	var partial *PartialError
	if !errors.As(err, &partial) || partial.Failed != 1 || !strings.Contains(err.Error(), "2025") {
		t.Fatalf("err = %v, want a PartialError for 2025", err)
	}
	if len(reps) != 4 || reps[0].Year != 2024 {
		t.Errorf("years = %v, want the four that parsed", years(reps))
	}

	// Every fetched year failing is a plain error with no reports.
	c, _ = fakeGirbo(t, map[string]bool{"27306568": true, "24177749": true})
	reps, err = c.FetchAnnual(context.Background(), "7708004767", func(y int) bool { return y <= 2023 })
	if err == nil || errors.As(err, &partial) || reps != nil {
		t.Errorf("all failed: reps=%v err=%v, want a plain error", years(reps), err)
	}
}
