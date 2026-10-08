package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/go-chi/chi/v5"
)

type fakeSchedule []models.ScheduledJob

func (f fakeSchedule) Status() []models.ScheduledJob { return f }

func newUpdatesServer(repo Repository, sched ScheduleSource) *chi.Mux {
	c := NewController(repo, slog.New(slog.DiscardHandler))
	c.now = func() time.Time { return testNow }
	if sched != nil {
		c.SetSchedule(sched)
	}
	r := chi.NewRouter()
	r.Get("/updates", c.UpdatesHandler)
	r.Get("/api/fetch-runs", c.FetchRunsAPI)
	return r
}

func sampleRuns() []models.FetchRun {
	started := time.Date(2026, 9, 30, 4, 0, 0, 0, time.UTC) // 07:00 MSK
	finished := started.Add(3*time.Minute + 5*time.Second)
	return []models.FetchRun{
		{ID: 2, Kind: "financials", Trigger: "catchup", Scope: "stored", FullScope: true, Status: models.RunPartial,
			StartedAt: started, FinishedAt: &finished, Updated: 1, UpToDate: 30, Rows: 4, QuotesSaved: 40,
			Failed: []string{"LKOH"}, QuotesFailed: []string{"CSVONLY"}},
		{ID: 1, Kind: "quotes", Trigger: "cli", Scope: "stored", FullScope: true, Status: models.RunFailed,
			StartedAt: started.Add(-time.Hour), FinishedAt: &started, Error: "list stored companies: <boom>"},
	}
}

func TestUpdatesPage(t *testing.T) {
	sched := fakeSchedule{
		{Hour: 7, Name: "quotes", Next: time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)},
		{Weekly: true, Weekday: time.Sunday, Hour: 5, Name: "financials", Running: true},
	}
	repo := &fakeRepo{runs: sampleRuns(), quotes: map[string]models.MarketQuote{
		"SBER": {Company: "SBER", Capitalization: 6000, PriceDate: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)},
		"OLD":  {Company: "OLD", Capitalization: 10, PriceDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
	}}
	rec := do(t, newUpdatesServer(repo, sched), http.MethodGet, "/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if strings.Contains(body, "boom") {
		t.Error("page leaks the raw run error")
	}
	if strings.Contains(body, "Импорт CSV") {
		t.Error("quarterly CSV import section is still on the page")
	}
	if strings.Contains(body, "Реестр тикеров") || strings.Contains(body, "Собрать предложение") {
		t.Error("ticker registry proposal section is still on the page")
	}
	if strings.Contains(body, "Загрузка данных") || strings.Contains(body, "fetch-form") {
		t.Error("fetch form section is still on the page")
	}
	for _, want := range []string{
		"ежедневно в 07:00", "Следующий запуск: 01.10.2026 07:00", // UTC shown as Moscow time
		"по воскресеньям в 05:00", "выполняется сейчас",
		"Годовые МСФО",
		"Подтянуть МСФО",
		"Загрузить дивиденды",
		"Дозаполнить пустые колонки",
		"без VPN",
		"Последняя цена закрытия: 29.09.2026", "Актуальны 1 из 2",
		"30.09.2026 07:00", "Отчётность и котировки", "пропущенный запуск", "частично", "3 мин 5 с",
		"обновлено компаний: 1, без изменений: 30, строк: 4; котировок: 40",
		"Не загружены: LKOH", "Без котировки: CSVONLY",
		"Запуск завершился ошибкой",
		`href="/"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	// Scheduler off, nothing fetched yet.
	rec = do(t, newUpdatesServer(&fakeRepo{}, nil), http.MethodGet, "/updates", "")
	body = rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "SCHEDULER_ENABLED=1") ||
		!strings.Contains(body, "Автоматическое обновление выключено") ||
		!strings.Contains(body, "Котировок нет") || !strings.Contains(body, "Запусков пока не было") {
		t.Errorf("empty page: status %d\n%s", rec.Code, body)
	}
}

func TestUpdatesRSBUAndCapJumps(t *testing.T) {
	repo := &fakeRepo{
		companies: []string{"LKOH", "X5"},
		history: map[string][]models.QuarterData{
			"LKOH": {
				{Year: 2023, Quarter: "Q4", Company: "LKOH", Source: models.SourceRSBU, Capitalization: models.Float(100)},
				{Year: 2024, Quarter: "Q4", Company: "LKOH", Source: models.SourceRSBU, Capitalization: models.Float(300)},
			},
			"X5": {{Year: 2024, Quarter: "Q4", Company: "X5", Source: models.SourceManual, Revenue: models.Float(1)}},
		},
		capReviews: []analytics.CapReview{{Company: "LKOH", From: 2023, To: 2024, Kind: analytics.CapReviewIssue}},
	}
	body := do(t, newUpdatesServer(repo, nil), http.MethodGet, "/updates", "").Body.String()
	for _, want := range []string{"Последний год — только РСБУ", ">LKOH</a>", "Скачки капитализации", `value="issue" selected`, "×3,0"} {
		if !strings.Contains(body, want) {
			t.Errorf("updates page lacks %q", want)
		}
	}
	if strings.Contains(body, ">X5</a>") {
		t.Error("X5 has IFRS for 2024 and must not be listed as RSBU-only")
	}
}

func TestFetchRunsAPI(t *testing.T) {
	rec := do(t, newUpdatesServer(&fakeRepo{runs: sampleRuns()}, fakeSchedule{{Hour: 7, Name: "quotes", Schedule: "07:00"}}), http.MethodGet, "/api/fetch-runs", "")
	var got struct {
		SchedulerEnabled bool `json:"scheduler_enabled"`
		Jobs             []struct {
			Name, Schedule string
		} `json:"jobs"`
		Runs []models.FetchRun `json:"runs"`
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("API leaks the raw run error")
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if !got.SchedulerEnabled || len(got.Jobs) != 1 || got.Jobs[0].Schedule != "07:00" ||
		len(got.Runs) != 2 || got.Runs[0].Failed[0] != "LKOH" || got.Runs[1].FinishedAt == nil {
		t.Errorf("response = %+v", got)
	}

	// Disabled scheduler and no runs: empty arrays, not null.
	rec = do(t, newUpdatesServer(&fakeRepo{}, nil), http.MethodGet, "/api/fetch-runs", "")
	if want := `{"scheduler_enabled":false,"jobs":[],"runs":[]}`; strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("empty response = %s, want %s", rec.Body, want)
	}
}
