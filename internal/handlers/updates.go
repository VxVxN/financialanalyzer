package handlers

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	financialanalyzer "github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// updatesRunLimit is how many recent runs the updates page and API list.
const updatesRunLimit = 50

var updatesTemplate = template.Must(
	template.ParseFS(financialanalyzer.TemplatesFS, "templates/updates.html"),
)

var (
	runKindLabels = map[string]string{
		models.RunKindQuotes:     "Котировки",
		models.RunKindFinancials: "Отчётность и котировки",
	}
	runTriggerLabels = map[string]string{
		models.TriggerCLI:      "вручную",
		models.TriggerSchedule: "по расписанию",
		models.TriggerCatchUp:  "пропущенный запуск",
	}
	runStatusLabels = map[string]string{
		models.RunRunning:   "выполняется",
		models.RunOK:        "успешно",
		models.RunPartial:   "частично",
		models.RunFailed:    "ошибка",
		models.RunCanceled:  "прерван",
		models.RunAbandoned: "оборван",
	}
	weekdaysRu = [...]string{"воскресеньям", "понедельникам", "вторникам", "средам", "четвергам", "пятницам", "субботам"}
)

// labelOr returns labels[key], or key itself for an unknown value.
func labelOr(labels map[string]string, key string) string {
	if l, ok := labels[key]; ok {
		return l
	}
	return key
}

// scheduleLabel renders a slot in Russian: "ежедневно в 07:00" or
// "по воскресеньям в 05:00".
func scheduleLabel(s models.ScheduledJob) string {
	clock := fmt.Sprintf("%02d:%02d", s.Hour, s.Minute)
	if s.Weekly {
		return "по " + weekdaysRu[s.Weekday] + " в " + clock
	}
	return "ежедневно в " + clock
}

// mskTime formats a moment as Moscow wall-clock time.
func mskTime(t time.Time) string {
	return t.In(models.Moscow).Format("02.01.2006 15:04")
}

// humanDuration renders a run's duration compactly ("45 с", "3 мин 5 с").
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d с", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d мин %d с", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d ч %d мин", int(d.Hours()), int(d.Minutes())%60)
}

type jobView struct {
	Label, Schedule, Next string
	Running               bool
}

type runView struct {
	Kind, Trigger, Scope, Status, StatusClass string
	Started, Duration                         string
	Summary                                   string
	Failed, QuotesFailed                      string
	HasError                                  bool
}

type quotesView struct {
	Total, Fresh int
	Latest       string // latest trade date, "" if none
}

// quotesSummary counts stored and fresh quotes and finds the latest trade date.
func quotesSummary(quotes map[string]models.MarketQuote, now time.Time) quotesView {
	var v quotesView
	var latest time.Time
	for _, q := range quotes {
		v.Total++
		if analytics.QuoteIsFresh(q, now) {
			v.Fresh++
		}
		if q.PriceDate.After(latest) {
			latest = q.PriceDate
		}
	}
	if !latest.IsZero() {
		v.Latest = latest.Format("02.01.2006")
	}
	return v
}

func newRunView(r models.FetchRun) runView {
	v := runView{
		Kind:         labelOr(runKindLabels, r.Kind),
		Trigger:      labelOr(runTriggerLabels, r.Trigger),
		Scope:        r.Scope,
		Status:       labelOr(runStatusLabels, r.Status),
		StatusClass:  r.Status,
		Started:      mskTime(r.StartedAt),
		Failed:       strings.Join(r.Failed, ", "),
		QuotesFailed: strings.Join(r.QuotesFailed, ", "),
		HasError:     r.Error != "",
	}
	if r.FinishedAt != nil {
		v.Duration = humanDuration(r.Duration())
	}
	var parts []string
	if r.Kind == models.RunKindFinancials {
		parts = append(parts, fmt.Sprintf("обновлено компаний: %d, без изменений: %d, строк: %d", r.Updated, r.UpToDate, r.Rows))
	}
	parts = append(parts, fmt.Sprintf("котировок: %d", r.QuotesSaved))
	v.Summary = strings.Join(parts, "; ")
	return v
}

// updatesAPIResponse is the JSON shape of /api/fetch-runs.
type updatesAPIResponse struct {
	SchedulerEnabled bool                  `json:"scheduler_enabled"`
	Jobs             []models.ScheduledJob `json:"jobs"`
	Runs             []models.FetchRun     `json:"runs"`
}

func (controller *Controller) scheduleStatus() []models.ScheduledJob {
	if controller.schedule == nil {
		return nil
	}
	return controller.schedule.Status()
}

func (controller *Controller) recentRuns(ctx context.Context) ([]models.FetchRun, error) {
	runs, err := controller.repo.RecentFetchRuns(ctx, updatesRunLimit)
	if runs == nil {
		runs = []models.FetchRun{}
	}
	return runs, err
}

// FetchRunsAPI returns the scheduler's timetable and the latest fetch runs.
func (controller *Controller) FetchRunsAPI(w http.ResponseWriter, r *http.Request) {
	runs, err := controller.recentRuns(r.Context())
	if err != nil {
		controller.serverError(w, "failed to list fetch runs", err)
		return
	}
	jobs := controller.scheduleStatus()
	if jobs == nil {
		jobs = []models.ScheduledJob{}
	}
	writeJSON(w, http.StatusOK, updatesAPIResponse{
		SchedulerEnabled: controller.schedule != nil,
		Jobs:             jobs,
		Runs:             runs,
	})
}

// UpdatesHandler renders the "Обновление данных" page: the refresh timetable,
// quote freshness and the history of fetch runs.
func (controller *Controller) UpdatesHandler(w http.ResponseWriter, r *http.Request) {
	runs, err := controller.recentRuns(r.Context())
	if err != nil {
		controller.htmlServerError(w, "failed to list fetch runs", err)
		return
	}
	quotes, err := controller.repo.GetMarketQuotes(r.Context())
	if err != nil {
		controller.htmlServerError(w, "failed to load market quotes", err)
		return
	}

	data := struct {
		SchedulerEnabled bool
		Jobs             []jobView
		Quotes           quotesView
		Runs             []runView
		QuoteMaxAgeDays  int
	}{
		SchedulerEnabled: controller.schedule != nil,
		Quotes:           quotesSummary(quotes, controller.now()),
		QuoteMaxAgeDays:  int(analytics.QuoteMaxAge / (24 * time.Hour)),
	}
	for _, j := range controller.scheduleStatus() {
		data.Jobs = append(data.Jobs, jobView{
			Label:    labelOr(runKindLabels, j.Name),
			Schedule: scheduleLabel(j),
			Next:     mskTime(j.Next),
			Running:  j.Running,
		})
	}
	for _, run := range runs {
		data.Runs = append(data.Runs, newRunView(run))
	}

	var buf bytes.Buffer
	if err := updatesTemplate.Execute(&buf, data); err != nil {
		controller.htmlServerError(w, "failed to render updates page", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
