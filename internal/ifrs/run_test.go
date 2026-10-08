package ifrs

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func TestYears(t *testing.T) {
	got := Years(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
	want := []int{2021, 2022, 2023, 2024, 2025}
	if len(got) != len(want) {
		t.Fatalf("years = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("years = %v, want %v", got, want)
		}
	}
}

type memStore struct {
	companies []string
	manual    map[string][]models.ManualFinancials
	saved     []models.ManualFinancials
}

func (m *memStore) GetAllCompanies(context.Context) ([]string, error) { return m.companies, nil }
func (m *memStore) GetManualFinancials(_ context.Context, company string) ([]models.ManualFinancials, error) {
	return m.manual[company], nil
}
func (m *memStore) SaveManualFinancials(_ context.Context, row models.ManualFinancials) error {
	m.saved = append(m.saved, row)
	return nil
}

type memSource struct {
	text map[string]string
}

func (m memSource) Text(_ context.Context, ticker string, year int) (string, error) {
	if s, ok := m.text[ticker+":"+strconv.Itoa(year)]; ok {
		return s, nil
	}
	return "", ErrNotFound
}

func TestRunFillsGapsOnly(t *testing.T) {
	kept := models.Float(1)
	store := &memStore{
		companies: []string{"X5"},
		manual: map[string][]models.ManualFinancials{
			"X5": {{Company: "X5", Year: 2025, Revenue: kept, Dividends: models.Float(0)}},
		},
	}
	src := memSource{text: map[string]string{
		"X5:2025": "МЛН РУБ.\nВЫРУЧКА\n999\nЧИСТАЯ ПРИБЫЛЬ\n10000\n",
	}}
	// 10000 million = 10 billion. One column, so it is the annual figure.
	// Revenue is already stored and must stay 1; profit is empty and is filled.
	res := Run(context.Background(), store, src, nil, []int{2025, 2024}, nil, nil)
	if res.Saved != 1 || res.Missing != 1 || res.Unchanged != 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved %d rows", len(store.saved))
	}
	got := store.saved[0]
	if got.Revenue == nil || *got.Revenue != 1 {
		t.Errorf("revenue overwritten: %v", got.Revenue)
	}
	if got.Dividends == nil || *got.Dividends != 0 {
		t.Errorf("dividends lost: %v", got.Dividends)
	}
	if len(res.Fills) != 1 || res.Fills[0].Company != "X5" || res.Fills[0].Year != 2025 ||
		!strings.Contains(res.Fills[0].Text, "чистая прибыль 10") || strings.Contains(res.Fills[0].Text, "выручка") {
		t.Errorf("fills = %+v", res.Fills)
	}
	if got.NetProfit == nil || *got.NetProfit != 10 {
		t.Errorf("net profit = %v, want 10", value(got.NetProfit))
	}
	if res.Fills[0].NewYear {
		t.Error("filling a field on a stored year must not count as a new year")
	}
}

func TestRunMarksNewYear(t *testing.T) {
	store := &memStore{companies: []string{"X5"}}
	src := memSource{text: map[string]string{
		"X5:2024": "МЛН РУБ.\nВЫРУЧКА\n1000\n",
	}}
	res := Run(context.Background(), store, src, nil, []int{2024}, nil, nil)
	if res.Saved != 1 || len(res.Fills) != 1 || !res.Fills[0].NewYear || res.Fills[0].Year != 2024 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunDownloadError(t *testing.T) {
	store := &memStore{companies: []string{"X5"}}
	res := Run(context.Background(), store, errSource{}, nil, []int{2025}, nil, nil)
	if res.Failed != 1 {
		t.Fatalf("result = %+v", res)
	}
}

type errSource struct{}

func (errSource) Text(context.Context, string, int) (string, error) {
	return "", errors.New("timeout")
}

func value(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func TestRunReportsProgress(t *testing.T) {
	store := &memStore{companies: []string{"X5", "BELU"}}
	var steps []Progress
	res := Run(context.Background(), store, memSource{}, nil, []int{2025, 2024}, nil, func(p Progress) {
		steps = append(steps, p)
	})
	if res.Missing != 4 {
		t.Fatalf("result = %+v", res)
	}
	if len(steps) != 4 {
		t.Fatalf("steps = %d, want one per company-year", len(steps))
	}
	if steps[0].Company != "X5" || steps[0].Year != 2025 || steps[0].Done != 0 || steps[0].Total != 4 {
		t.Fatalf("first = %+v", steps[0])
	}
	last := steps[3]
	if last.Company != "BELU" || last.Year != 2024 || last.Done != 3 || last.Missing != 3 {
		t.Fatalf("last = %+v", last)
	}
	msg := last.Status().Message
	if msg != "BELU, 2024 — 4 из 4. Дописано: 0, без новых: 0, не найдено: 3" {
		t.Fatalf("message = %q", msg)
	}
	if !last.Status().Running {
		t.Fatal("in-flight status must say the run is still going")
	}
}

func TestMatchCompanies(t *testing.T) {
	got := MatchCompanies([]string{"X5", "BELU", "T"}, "belu, t")
	if strings.Join(got, ",") != "BELU,T" {
		t.Fatalf("match = %v", got)
	}
	if MatchCompanies([]string{"X5"}, "") != nil {
		t.Fatal("empty list must mean no filter")
	}
}

func TestReportURLs(t *testing.T) {
	urls := reportURLs("https://cdn.example", "X5", 2025)
	joined := strings.Join(urls, "\n")
	for _, want := range []string{
		"https://cdn.example/reports/2025/MOEX/X/X5_2025_12_Y_%D0%9C%D0%A1%D0%A4%D0%9E_press.pdf",
		"https://cdn.example/reports/2025/MOEX/X/x5_2025_12_y_msfo_press.pdf",
		"https://cdn.example/reports/2025/MOEX/F/FIVE_2025_12_Y_%D0%9C%D0%A1%D0%A4%D0%9E_press.pdf",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s\n%s", want, joined)
		}
	}
	press := strings.Index(joined, "msfo_press")
	full := strings.Index(joined, "x5_2025_12_y_msfo.pdf")
	if press < 0 || full < 0 || press > full {
		t.Errorf("press release should be tried before the full filing\n%s", joined)
	}
}
