// Package scheduler runs the fetch pipelines on a timetable inside cmd/plot, so
// quotes and financials stay current without anyone clicking /updates.
//
// Jobs fire at wall-clock slots in Moscow time (daily "HH:MM" or weekly
// "sun HH:MM"). Runs are strictly sequential — one goroutine, one fetch at a
// time — and a slot that fires while another job is running waits for it. On
// startup a job with no completed full-scope run today (Moscow time; weekly
// jobs only on their weekday) is caught up at once, so a server that was down
// at 07:00 — or that starts before the slot — still refreshes.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/fetcher"
	"github.com/VxVxN/financialanalyzer/internal/models"
)

// Moscow is the schedule's time zone (a fixed offset: no DST since 2014).
var Moscow = models.Moscow

const (
	day  = 24 * time.Hour
	week = 7 * day

	// defaultStartupDelay lets the server settle before catch-up runs, and
	// keeps a crash-looping process from hammering the sources.
	defaultStartupDelay = 30 * time.Second
)

// Spec is a daily or weekly wall-clock slot in Moscow time.
type Spec struct {
	Weekly       bool
	Weekday      time.Weekday // when Weekly
	Hour, Minute int
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// ParseSpec reads "HH:MM" (daily) or "DAY HH:MM" (weekly, DAY one of
// mon..sun), both Moscow time.
func ParseSpec(s string) (Spec, error) {
	var spec Spec
	fields := strings.Fields(strings.ToLower(s))
	switch len(fields) {
	case 1:
	case 2:
		wd, ok := weekdays[fields[0]]
		if !ok {
			return Spec{}, fmt.Errorf("schedule %q: unknown weekday %q (want mon..sun)", s, fields[0])
		}
		spec.Weekly, spec.Weekday = true, wd
		fields = fields[1:]
	default:
		return Spec{}, fmt.Errorf("schedule %q: want \"HH:MM\" or \"DAY HH:MM\"", s)
	}
	hh, mm, ok := strings.Cut(fields[0], ":")
	h, errH := strconv.Atoi(hh)
	m, errM := strconv.Atoi(mm)
	if !ok || errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 || len(mm) != 2 {
		return Spec{}, fmt.Errorf("schedule %q: bad time %q (want HH:MM)", s, fields[0])
	}
	spec.Hour, spec.Minute = h, m
	return spec, nil
}

// Period is the time between two slots.
func (s Spec) Period() time.Duration {
	if s.Weekly {
		return week
	}
	return day
}

// Next returns the first slot strictly after t.
func (s Spec) Next(t time.Time) time.Time {
	m := t.In(Moscow)
	next := time.Date(m.Year(), m.Month(), m.Day(), s.Hour, s.Minute, 0, 0, Moscow)
	if s.Weekly {
		next = next.AddDate(0, 0, (int(s.Weekday)-int(m.Weekday())+7)%7)
	}
	if !next.After(t) {
		next = next.Add(s.Period())
	}
	return next
}

// Prev returns the latest slot at or before t (slots are exactly Period apart
// in a zone without DST).
func (s Spec) Prev(t time.Time) time.Time {
	return s.Next(t.Add(-s.Period()))
}

func (s Spec) String() string {
	clock := fmt.Sprintf("%02d:%02d", s.Hour, s.Minute)
	if s.Weekly {
		return strings.ToLower(s.Weekday.String()[:3]) + " " + clock
	}
	return clock
}

// Job is one scheduled fetch.
type Job struct {
	Spec    Spec
	Request fetcher.Request
	// SatisfiedBy lists the run kinds whose completed full-scope run counts as
	// this job's (a financials run refreshes quotes too).
	SatisfiedBy []string
}

// Name is the job's run kind ("quotes" or "financials").
func (j Job) Name() string { return j.Request.Kind() }

// Store is the fetch_runs slice of the repository the scheduler needs.
type Store interface {
	LastCompletedRun(ctx context.Context, kinds []string) (time.Time, bool, error)
	AbandonStaleRuns(ctx context.Context, olderThan time.Duration, triggers []string) (int64, error)
}

// RunFunc performs one fetch (fetcher.RunRecorded in production). stillNeeded,
// when non-nil, is to be checked once the fetch lock is held (see
// fetcher.RunOptions): false skips the run with fetcher.ErrNotNeeded.
type RunFunc func(ctx context.Context, req fetcher.Request, trigger string, stillNeeded func(context.Context) bool) error

// Scheduler runs jobs at their slots. Construct it with New.
type Scheduler struct {
	jobs   []Job
	store  Store
	run    RunFunc
	logger *slog.Logger

	now          func() time.Time
	sleep        func(ctx context.Context, until time.Time) error
	startupDelay time.Duration
	afterCatchUp func(context.Context) // once, after the startup catch-up finishes

	mu      sync.Mutex
	next    []time.Time // per job, next slot
	running int         // index of the running job, -1 if idle
}

// New returns a scheduler for jobs; Run starts it.
func New(jobs []Job, store Store, run RunFunc, logger *slog.Logger) *Scheduler {
	s := &Scheduler{
		jobs:         jobs,
		store:        store,
		run:          run,
		logger:       logger,
		now:          time.Now,
		startupDelay: defaultStartupDelay,
		next:         make([]time.Time, len(jobs)),
		running:      -1,
	}
	s.sleep = s.sleepUntil
	return s
}

// SetAfterCatchUp runs fn once, after missed slots have been caught up and
// before the timetable loop. A startup canceled during that wait does not
// call it.
func (s *Scheduler) SetAfterCatchUp(fn func(context.Context)) {
	s.afterCatchUp = fn
}

// Status reports each job's schedule and next slot.
func (s *Scheduler) Status() []models.ScheduledJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]models.ScheduledJob, len(s.jobs))
	for i, j := range s.jobs {
		next := s.next[i]
		if next.IsZero() {
			next = j.Spec.Next(s.now())
		}
		out[i] = models.ScheduledJob{
			Name: j.Name(), Schedule: j.Spec.String(),
			Weekly: j.Spec.Weekly, Weekday: j.Spec.Weekday, Hour: j.Spec.Hour, Minute: j.Spec.Minute,
			Next: next, Running: s.running == i,
		}
	}
	return out
}

// Run blocks until ctx is canceled: it catches up missed slots, then fires
// each job at its slots. A failed run is logged; the job waits for its next
// slot.
func (s *Scheduler) Run(ctx context.Context) {
	if len(s.jobs) == 0 {
		return
	}
	now := s.now()
	s.mu.Lock()
	for i, j := range s.jobs {
		s.next[i] = j.Spec.Next(now)
	}
	s.mu.Unlock()

	// Only one scheduler runs, so any of its runs still marked running died
	// with the previous process, whatever their age.
	own := []string{fetcher.TriggerSchedule, fetcher.TriggerCatchUp}
	if n, err := s.store.AbandonStaleRuns(ctx, fetcher.StaleRunAge, own); err != nil {
		s.logger.Warn("Scheduler: cannot mark abandoned runs", "error", err)
	} else if n > 0 {
		s.logger.Info("Scheduler: marked abandoned runs", "count", n)
	}
	for _, j := range s.jobs {
		s.logger.Info("Scheduler: job scheduled", "job", j.Name(), "schedule", j.Spec.String()+" MSK", "next", j.Spec.Next(now))
	}

	if err := s.sleep(ctx, now.Add(s.startupDelay)); err != nil {
		return
	}
	caught := make([]bool, len(s.jobs))
	for _, i := range s.catchUpOrder() {
		if ctx.Err() != nil {
			return
		}
		// Checked just before each run, so a financials catch-up that also
		// refreshed quotes satisfies the quotes job.
		if s.missed(ctx, s.jobs[i]) {
			// Checked again once the fetch lock is held: a manual fetch
			// this one waited for may have done the work meanwhile.
			j := s.jobs[i]
			s.execute(ctx, i, fetcher.TriggerCatchUp, func(ctx context.Context) bool { return s.missed(ctx, j) })
			caught[i] = true
		}
	}
	// Slots that passed during the startup delay or a catch-up run were either
	// just caught up or found satisfied; either way they must not fire now.
	// A catch-up before today's slot also covers that slot (do not fire at 07:00
	// after a 06:00 catch-up).
	s.mu.Lock()
	after := maxTime(now, s.now())
	for i, j := range s.jobs {
		n := j.Spec.Next(after)
		if caught[i] && sameMoscowDay(n, after) {
			n = j.Spec.Next(n)
		}
		s.next[i] = n
	}
	s.mu.Unlock()

	if s.afterCatchUp != nil && ctx.Err() == nil {
		s.afterCatchUp(ctx)
	}

	for {
		i := s.earliest()
		s.mu.Lock()
		slot := s.next[i]
		s.mu.Unlock()
		if err := s.sleep(ctx, slot); err != nil {
			return
		}
		s.execute(ctx, i, fetcher.TriggerSchedule, nil)
		// A run that overran later slots skips them rather than firing
		// back-to-back.
		s.mu.Lock()
		s.next[i] = s.jobs[i].Spec.Next(maxTime(slot, s.now()))
		s.mu.Unlock()
	}
}

// missed reports whether the job's latest slot passed with no completed
// full-scope run of a satisfying kind since. A lookup error counts as not
// missed: the regular slot will come.
func (s *Scheduler) missed(ctx context.Context, j Job) bool {
	last, ok, err := s.store.LastCompletedRun(ctx, j.SatisfiedBy)
	if err != nil {
		s.logger.Warn("Scheduler: cannot read the last run", "job", j.Name(), "error", err)
		return false
	}
	return !ok || last.Before(catchUpSince(j.Spec, s.now()))
}

// catchUpSince is the instant a completed run must be at or after to count as
// this job having run "this period". For a daily job that is the start of the
// Moscow day: a restart before the slot still refreshes if nothing ran today.
// A weekly job on a different weekday keeps the last slot (Sunday's financials
// are not rerun every Wednesday).
func catchUpSince(spec Spec, now time.Time) time.Time {
	since := spec.Prev(now)
	if spec.Weekly && spec.Weekday != now.In(Moscow).Weekday() {
		return since
	}
	if day := moscowDayStart(now); day.After(since) {
		return day
	}
	return since
}

func moscowDayStart(t time.Time) time.Time {
	m := t.In(Moscow)
	return time.Date(m.Year(), m.Month(), m.Day(), 0, 0, 0, 0, Moscow)
}

func sameMoscowDay(a, b time.Time) bool {
	return moscowDayStart(a).Equal(moscowDayStart(b))
}

// catchUpOrder lists job indices with the jobs that satisfy the most other
// jobs first (financials before quotes), so one catch-up run can cover several.
func (s *Scheduler) catchUpOrder() []int {
	covers := make([]int, len(s.jobs))
	for i, j := range s.jobs {
		for k, other := range s.jobs {
			if k != i && slices.Contains(other.SatisfiedBy, j.Name()) {
				covers[i]++
			}
		}
	}
	order := make([]int, len(s.jobs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return covers[order[a]] > covers[order[b]] })
	return order
}

// earliest returns the index of the job due first (ties: declaration order).
func (s *Scheduler) earliest() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	best := 0
	for i := range s.next {
		if s.next[i].Before(s.next[best]) {
			best = i
		}
	}
	return best
}

func (s *Scheduler) execute(ctx context.Context, i int, trigger string, stillNeeded func(context.Context) bool) {
	j := s.jobs[i]
	s.mu.Lock()
	s.running = i
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = -1
		s.mu.Unlock()
	}()

	s.logger.Info("Scheduler: run started", "job", j.Name(), "trigger", trigger)
	start := s.now()
	if err := s.run(ctx, j.Request, trigger, stillNeeded); err != nil {
		if errors.Is(err, fetcher.ErrNotNeeded) {
			s.logger.Info("Scheduler: run skipped, another run already did it", "job", j.Name())
			return
		}
		if errors.Is(err, fetcher.ErrNoCompanies) {
			s.logger.Info("Scheduler: nothing to refresh, the DB has no companies yet (add them from /updates)", "job", j.Name())
			return
		}
		if ctx.Err() != nil {
			s.logger.Info("Scheduler: run interrupted by shutdown", "job", j.Name())
			return
		}
		s.logger.Error("Scheduler: run failed", "job", j.Name(), "error", err)
		return
	}
	s.logger.Info("Scheduler: run finished", "job", j.Name(), "duration", s.now().Sub(start).Round(time.Second))
}

// maxNap bounds a single timer: Go timers follow the monotonic clock, which
// stops while the host is suspended, so a long sleep re-checks the wall clock.
const maxNap = time.Hour

// sleepUntil waits for the wall-clock time until, or returns ctx.Err().
func (s *Scheduler) sleepUntil(ctx context.Context, until time.Time) error {
	for {
		d := until.Sub(s.now())
		if d <= 0 {
			return nil
		}
		t := time.NewTimer(min(d, maxNap))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// QuotesJob refreshes the latest closes of every stored company.
func QuotesJob(spec Spec) Job {
	return Job{
		Spec:        spec,
		Request:     fetcher.Request{QuotesOnly: true},
		SatisfiedBy: []string{fetcher.KindQuotes, fetcher.KindFinancials},
	}
}

// FinancialsJob refreshes every stored company's financials (incrementally:
// periods already stored are skipped) and then its quote.
func FinancialsJob(spec Spec) Job {
	return Job{
		Spec:        spec,
		Request:     fetcher.Request{},
		SatisfiedBy: []string{fetcher.KindFinancials},
	}
}
