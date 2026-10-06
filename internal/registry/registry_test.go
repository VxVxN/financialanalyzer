package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/scraper/girbo"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

type fakeExchange struct {
	board    []moex.BoardSecurity
	emitters map[string]moex.Emitter // secid -> issuer
	failISS  map[string]bool
	indexes  map[string][]string
}

func (f fakeExchange) BoardSecurities(context.Context) ([]moex.BoardSecurity, error) {
	return f.board, nil
}

func (f fakeExchange) EmitterByISIN(_ context.Context, secid, _ string) (moex.Emitter, error) {
	if f.failISS[secid] {
		return moex.Emitter{}, errors.New("timeout")
	}
	e, ok := f.emitters[secid]
	if !ok {
		return moex.Emitter{}, fmt.Errorf("%s: %w", secid, moex.ErrNoEmitter)
	}
	return e, nil
}

func (f fakeExchange) IndexConstituents(_ context.Context, index string) ([]string, error) {
	m, ok := f.indexes[index]
	if !ok {
		return nil, errors.New("unavailable")
	}
	return m, nil
}

type fakeFilings struct {
	reports map[string][]girbo.AnnualReport // inn -> reports, newest first
	errs    map[string]error
	skipped map[string][]int // inn -> years the skip callback rejected
}

func (f fakeFilings) FetchAnnual(_ context.Context, inn string, skip func(int) bool) ([]girbo.AnnualReport, error) {
	if err := f.errs[inn]; err != nil {
		return nil, err
	}
	var out []girbo.AnnualReport
	for _, r := range f.reports[inn] {
		if skip(r.Year) {
			f.skipped[inn] = append(f.skipped[inn], r.Year)
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func report(year int, revenue, profit float64) girbo.AnnualReport {
	return girbo.AnnualReport{Year: year, Revenue: models.Float(revenue), NetProfit: models.Float(profit)}
}

func share(secid, secType string) moex.BoardSecurity {
	return moex.BoardSecurity{SecID: secid, ISIN: "RU" + secid, SecType: secType}
}

func TestBuild(t *testing.T) {
	ex := fakeExchange{
		board: []moex.BoardSecurity{
			share("NLMK", "1"),
			share("SBER", "1"),
			share("TATNP", "2"), // the preferred share comes first
			share("TATN", "1"),
			share("FIXR", "D"), // a receipt: not a share
			share("FRGN", "1"), // no INN in ISS
			share("ROSN", "1"), // closed reporting
			share("MGNT", "1"), // holding without revenue
			share("OZON", "1"), // holding with revenue
			share("NOBO", "1"), // not in ГИР БО
			share("SLOW", "1"), // ISS fails
			share("DOWN", "1"), // ГИР БО fails
		},
		emitters: map[string]moex.Emitter{
			"NLMK":  {INN: "4823006703", Title: "ПАО «НЛМК»"},
			"TATN":  {INN: "1644003838", Title: "ПАО «Татнефть»"},
			"TATNP": {INN: "1644003838", Title: "ПАО «Татнефть»"},
			"ROSN":  {INN: "7706107510"},
			"MGNT":  {INN: "2309085638"},
			"OZON":  {INN: "3900045916"},
			"NOBO":  {INN: "1111111111"},
			"DOWN":  {INN: "2222222222"},
		},
		failISS: map[string]bool{"SLOW": true},
		indexes: map[string][]string{
			"MOEXMM": {"NLMK"},
			"MOEXOG": {"TATNP", "ROSN"}, // only the preferred share is in it
			"MOEXCN": {"MGNT"},
			// the rest are unavailable: their members fall back to "other"
		},
	}
	fil := fakeFilings{
		reports: map[string][]girbo.AnnualReport{
			"4823006703": {report(2025, 900, 100), report(2024, 950, 120), report(2020, 500, 50)},
			"1644003838": {report(2024, 1500, 250)},
			"7706107510": {report(2021, 9000, 900)},
			"2309085638": {report(2025, 0.4, 20)},
			"3900045916": {report(2025, 5, 30)},
		},
		errs: map[string]error{
			"1111111111": fmt.Errorf("search: %w", girbo.ErrNoOrganization),
			"2222222222": errors.New("503"),
		},
		skipped: map[string][]int{},
	}
	opts := Options{
		Now:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Banks: map[string]struct{}{"SBER": {}},
		Known: map[string]KnownEntry{
			"OZON": {INN: "3900045916", Category: "retail"}, // the registry's category wins
			"MGNT": {INN: "2309085638"},
			"NLMK": {INN: "4800000000"},
		},
	}
	got, err := Build(context.Background(), ex, fil, opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	byTicker := map[string]Candidate{}
	var order []string
	for _, c := range got {
		byTicker[c.Ticker] = c
		order = append(order, c.Ticker)
	}
	if want := "DOWN,FRGN,MGNT,NLMK,NOBO,OZON,ROSN,SBER,SLOW,TATN"; strings.Join(order, ",") != want {
		t.Fatalf("tickers = %v, want %s (sorted, receipt dropped, TATNP folded)", order, want)
	}

	checks := []struct {
		ticker   string
		verdict  Verdict
		reason   string // substring
		category string
	}{
		{"NLMK", Include, "", "metals"},
		{"TATN", Include, "", "oil"},
		{"OZON", Include, "", "retail"},
		{"SBER", Exclude, "банк", "other"},
		{"FRGN", Exclude, "ИНН", "other"},
		{"ROSN", Exclude, "нет отчётности РСБУ за 2024–2025", "oil"},
		{"MGNT", Exclude, "0.4 млрд", "consumer"},
		{"NOBO", Exclude, "нет в ГИР БО", "other"},
		{"SLOW", Failed, "ISS: timeout", "other"},
		{"DOWN", Failed, "ГИР БО: 503", "other"},
	}
	for _, tc := range checks {
		c := byTicker[tc.ticker]
		if c.Verdict != tc.verdict || !strings.Contains(c.Reason, tc.reason) || c.Category != tc.category {
			t.Errorf("%s = %v %q %q; want %v, reason ~%q, category %q",
				tc.ticker, c.Verdict, c.Reason, c.Category, tc.verdict, tc.reason, tc.category)
		}
	}

	if c := byTicker["TATN"]; strings.Join(c.Preferred, ",") != "TATNP" {
		t.Errorf("TATN preferred = %v, want [TATNP]", c.Preferred)
	}
	if c := byTicker["NLMK"]; c.Latest == nil || c.Latest.Year != 2025 || !c.Known || c.Previous != "4800000000" {
		t.Errorf("NLMK = %+v; want latest 2025, known with previous INN", c)
	}
	if !byTicker["OZON"].Holding || byTicker["NLMK"].Holding {
		t.Error("OZON (profit > revenue) should be a holding, NLMK not")
	}
	if got := fil.skipped["4823006703"]; fmt.Sprint(got) != "[2020]" {
		t.Errorf("skipped years = %v, want only 2020 (older than 2024)", got)
	}
}

func TestBuildTickersFilter(t *testing.T) {
	ex := fakeExchange{
		board:    []moex.BoardSecurity{share("NLMK", "1"), share("TATN", "1")},
		emitters: map[string]moex.Emitter{"NLMK": {INN: "4823006703"}, "TATN": {INN: "1644003838"}},
	}
	fil := fakeFilings{reports: map[string][]girbo.AnnualReport{"4823006703": {report(2025, 900, 100)}}, skipped: map[string][]int{}}
	got, err := Build(context.Background(), ex, fil, Options{
		Now:     time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Tickers: map[string]struct{}{"NLMK": {}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(got) != 1 || got[0].Ticker != "NLMK" || got[0].Verdict != Include {
		t.Errorf("got %+v, want only NLMK included", got)
	}
}

func TestBuildCanceled(t *testing.T) {
	ex := fakeExchange{board: []moex.BoardSecurity{share("NLMK", "1")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, ex, fakeFilings{}, Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestWrite(t *testing.T) {
	cands := []Candidate{
		{Ticker: "NLMK", INN: "4823006703", Title: "ПАО «НЛМК»", Category: "metals", Verdict: Include,
			Latest: &girbo.AnnualReport{Year: 2025, Revenue: models.Float(900), NetProfit: models.Float(100)}},
		{Ticker: "OZON", INN: "3900045916", Category: "other", Verdict: Include, Holding: true, Known: true,
			Preferred: []string{"OZONP"}},
		{Ticker: "MGNT", INN: "2309085638", Verdict: Exclude, Reason: "холдинг", Known: true},
		{Ticker: "SLOW", Verdict: Failed, Reason: "ISS:\n timeout"},
	}
	var b strings.Builder
	if err := Write(&b, cands, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := b.String()

	// Every non-comment line is a registry row: TICKER INN CATEGORY.
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if f := strings.Fields(line); len(f) > 0 {
			rows = append(rows, strings.Join(f, " "))
		}
	}
	if want := "NLMK 4823006703 metals|OZON 3900045916 other"; strings.Join(rows, "|") != want {
		t.Errorf("rows = %q, want %q", rows, want)
	}
	for _, want := range []string{
		"Generated on 2026-10-01",
		"РСБУ 2025: выручка 900.0 млрд, прибыль 100.0 млрд; НОВЫЙ",
		"ХОЛДИНГ: прибыль > выручки; также OZONP",
		"MGNT  2309085638  холдинг (сейчас в реестре)",
		"SLOW  -  ISS: timeout",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestBuildKnownPreferredKeepsRegistryTicker(t *testing.T) {
	// The registry has the preferred share: the proposal must keep that name
	// (stored companies are keyed by it), listing the common one beside it.
	ex := fakeExchange{
		board: []moex.BoardSecurity{share("SNGS", "1"), share("SNGSP", "2"), share("SNGS", "1")},
		emitters: map[string]moex.Emitter{
			"SNGS":  {INN: "8602060555"},
			"SNGSP": {INN: "8602060555"},
		},
	}
	fil := fakeFilings{reports: map[string][]girbo.AnnualReport{"8602060555": {report(2025, 900, 100)}}, skipped: map[string][]int{}}
	got, err := Build(context.Background(), ex, fil, Options{
		Now:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Known: map[string]KnownEntry{"SNGSP": {INN: "8602060555", Category: "oil"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1 (duplicate listing and preferred folded): %+v", len(got), got)
	}
	c := got[0]
	if c.Ticker != "SNGSP" || fmt.Sprint(c.Preferred) != "[SNGS]" || !c.Known || c.Category != "oil" || c.Previous != "" {
		t.Errorf("got %+v; want SNGSP known (oil) with SNGS listed", c)
	}
}

func TestBuildKnownWithoutINN(t *testing.T) {
	// A registry ticker whose ISS step fails is still marked as registered,
	// so the proposal shows that a working row would be dropped.
	ex := fakeExchange{
		board:   []moex.BoardSecurity{share("BELU", "1"), share("WEIRD", "1")},
		failISS: map[string]bool{"BELU": true},
		emitters: map[string]moex.Emitter{
			"WEIRD": {INN: "CY123456"},
		},
	}
	got, err := Build(context.Background(), ex, fakeFilings{}, Options{
		Now:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Known: map[string]KnownEntry{"BELU": {INN: "7705634425", Category: "consumer"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(got) != 2 || got[0].Ticker != "BELU" || !got[0].Known || got[0].Verdict != Failed {
		t.Errorf("BELU = %+v; want a known Failed candidate", got)
	}
	if got[1].Verdict != Exclude || !strings.Contains(got[1].Reason, "не российский") {
		t.Errorf("WEIRD = %+v; want excluded for a foreign INN", got[1])
	}
}

func TestBuildPartialFilings(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	partial := func(r ...girbo.AnnualReport) partialFilings {
		return partialFilings{reports: r}
	}
	tests := []struct {
		name    string
		fil     partialFilings
		verdict Verdict
	}{
		// 2025 is the newest possible report in 2026: a failed older year
		// cannot change the decision.
		{"newest year parsed", partial(report(2025, 0.2, 1)), Exclude},
		// Only 2024 parsed: 2025 may be the one that failed, so decide later.
		{"newest year failed", partial(report(2024, 0.2, 1)), Failed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := fakeExchange{
				board:    []moex.BoardSecurity{share("NLMK", "1")},
				emitters: map[string]moex.Emitter{"NLMK": {INN: "4823006703"}},
			}
			got, err := Build(context.Background(), ex, tt.fil, Options{Now: now})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got[0].Verdict != tt.verdict {
				t.Errorf("verdict = %v (%s), want %v", got[0].Verdict, got[0].Reason, tt.verdict)
			}
		})
	}
}

// partialFilings returns its reports with a *girbo.PartialError, as
// FetchAnnual does when some years' details failed.
type partialFilings struct{ reports []girbo.AnnualReport }

func (f partialFilings) FetchAnnual(context.Context, string, func(int) bool) ([]girbo.AnnualReport, error) {
	return f.reports, &girbo.PartialError{Failed: 1, Err: errors.New("503")}
}
