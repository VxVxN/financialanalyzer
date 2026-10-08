package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/analytics"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/ifrs"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// Repository is the data-access surface the HTTP handlers depend on. Defining it
// here (consumer side) lets handlers be unit-tested with a fake while production
// uses *database.Repository.
type Repository interface {
	Ping(ctx context.Context) error
	GetAllCompanies(ctx context.Context) ([]string, error)
	GetAllCategories(ctx context.Context) ([]string, error)
	GetAllCompaniesWithCategories(ctx context.Context) ([]database.CompanyWithCategory, error)
	GetCompaniesHistory(ctx context.Context, companies []string) (map[string][]models.QuarterData, error)
	GetCompanyHistory(ctx context.Context, company string) ([]models.QuarterData, error)
	DeleteCompany(ctx context.Context, company string) error
	GetCompanyNote(ctx context.Context, company string) (string, error)
	SaveCompanyNote(ctx context.Context, company, note string) error
	DeleteCompanyNote(ctx context.Context, company string) error
	GetMarketQuote(ctx context.Context, company string) (models.MarketQuote, bool, error)
	GetMarketQuotes(ctx context.Context) (map[string]models.MarketQuote, error)
	RecentFetchRuns(ctx context.Context, limit int) ([]models.FetchRun, error)
	GetManualFinancials(ctx context.Context, company string) ([]models.ManualFinancials, error)
	SaveManualFinancials(ctx context.Context, m models.ManualFinancials) error
	SaveManualDividends(ctx context.Context, m models.ManualFinancials) error
	DeleteManualFinancials(ctx context.Context, company string, year int) error
	SaveQuarterData(ctx context.Context, data models.QuarterData) error
	PendingDigestEvents(ctx context.Context) ([]models.DigestEvent, error)
	PortfolioBands(ctx context.Context) (map[string]string, error)
	CapReviews(ctx context.Context) ([]analytics.CapReview, error)
	SaveCapReview(ctx context.Context, rev analytics.CapReview) error
	DeleteCapReview(ctx context.Context, company string, from, to int) error
}

// ScheduleSource reports the data-refresh timetable (*scheduler.Scheduler).
type ScheduleSource interface {
	Status() []models.ScheduledJob
}

// Jobs starts a background fetch or an IFRS pull (*ops.Ops).
type Jobs interface {
	StartFetch(req fetcher.Request) error
	FetchRunning() bool
	StartIFRS(companies []string, years []int) error
	IFRSStatus() ifrs.Status
}

type Controller struct {
	repo     Repository
	logger   *slog.Logger
	now      func() time.Time // clock for quote freshness; tests pin it
	schedule ScheduleSource   // nil when the scheduler is disabled
	jobs     Jobs             // nil in tests that do not exercise /api/fetch
}

func NewController(repo Repository, logger *slog.Logger) *Controller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{
		repo:   repo,
		logger: logger,
		now:    time.Now,
	}
}

// SetSchedule makes the updates page show the scheduler's timetable.
func (controller *Controller) SetSchedule(s ScheduleSource) {
	controller.schedule = s
}

// SetJobs wires the in-process fetch runner.
func (controller *Controller) SetJobs(j Jobs) {
	controller.jobs = j
}

// serverError logs the underlying cause and returns a generic 500 to the client
// so internal/database error text is never leaked over HTTP.
func (controller *Controller) serverError(w http.ResponseWriter, msg string, err error) {
	controller.logger.Error(msg, "error", err)
	writeJSONError(w, http.StatusInternalServerError, "internal server error")
}

// htmlServerError is the page-rendering counterpart of serverError: it logs the
// cause and returns a plain-text 500 (the client expects HTML, not JSON).
func (controller *Controller) htmlServerError(w http.ResponseWriter, msg string, err error) {
	controller.logger.Error(msg, "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
