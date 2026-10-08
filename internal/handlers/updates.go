package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// updatesRunLimit is how many recent runs the updates page and API list.
const updatesRunLimit = 50

var updatesTemplate = parsePage("updates.html")

var weekdaysRu = [...]string{"воскресеньям", "понедельникам", "вторникам", "средам", "четвергам", "пятницам", "субботам"}

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

type rsbuGapView struct {
	Company string
	Year    int
}

type capJumpView struct {
	Company   string
	From, To  int
	Ratio     string
	Kind      string
	KindLabel string
}

func jumpRatioText(ratio float64) string {
	times := ratio
	mark := "×"
	if ratio < 1 && ratio > 0 {
		times = 1 / ratio
		mark = "÷"
	}
	return mark + strings.ReplaceAll(fmt.Sprintf("%.1f", times), ".", ",")
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

func rsbuGapViews(histories map[string][]models.QuarterData) []rsbuGapView {
	gaps := analytics.RSBUOnlyLatest(registryTickers(), histories)
	out := make([]rsbuGapView, len(gaps))
	for i, g := range gaps {
		out[i] = rsbuGapView{Company: g.Company, Year: g.Year}
	}
	return out
}

func capJumpViews(histories map[string][]models.QuarterData, reviews []analytics.CapReview) []capJumpView {
	byCompany := analytics.GroupCapReviews(reviews)
	var out []capJumpView
	for company, rows := range histories {
		for _, j := range analytics.CapJumps(rows) {
			kind := analytics.ReviewKind(byCompany[company], j.From, j.To)
			out = append(out, capJumpView{
				Company: company, From: j.From, To: j.To,
				Ratio: jumpRatioText(j.Ratio), Kind: kind, KindLabel: analytics.CapReviewLabel(kind),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Company != out[j].Company {
			return out[i].Company < out[j].Company
		}
		return out[i].From < out[j].From
	})
	return out
}

func newRunView(r models.FetchRun) runView {
	v := runView{
		Kind:         models.RunKindLabel(r.Kind),
		Trigger:      models.RunTriggerLabel(r.Trigger),
		Scope:        r.Scope,
		Status:       models.RunStatusLabel(r.Status),
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
	companies, err := controller.repo.GetAllCompanies(r.Context())
	if err != nil {
		controller.htmlServerError(w, "failed to list companies", err)
		return
	}
	histories, err := controller.repo.GetCompaniesHistory(r.Context(), companies)
	if err != nil {
		controller.htmlServerError(w, "failed to load company history", err)
		return
	}
	reviews, err := controller.repo.CapReviews(r.Context())
	if err != nil {
		controller.htmlServerError(w, "failed to load cap reviews", err)
		return
	}

	data := struct {
		Meta             pageMeta
		SchedulerEnabled bool
		Jobs             []jobView
		Quotes           quotesView
		Runs             []runView
		QuoteMaxAgeDays  int
		RSBU             []rsbuGapView
		Jumps            []capJumpView
	}{
		Meta:             pageMeta{Title: "Обновление данных", Active: "updates"},
		SchedulerEnabled: controller.schedule != nil,
		Quotes:           quotesSummary(quotes, controller.now()),
		QuoteMaxAgeDays:  int(analytics.QuoteMaxAge / (24 * time.Hour)),
		RSBU:             rsbuGapViews(histories),
		Jumps:            capJumpViews(histories, reviews),
	}
	for _, j := range controller.scheduleStatus() {
		data.Jobs = append(data.Jobs, jobView{
			Label:    models.RunKindLabel(j.Name),
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
