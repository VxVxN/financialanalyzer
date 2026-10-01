# Financial Analyzer

[![CI](https://github.com/VxVxN/financialanalyzer/actions/workflows/ci.yml/badge.svg)](https://github.com/VxVxN/financialanalyzer/actions/workflows/ci.yml)

A Go application that ingests Russian-language quarterly financial data and serves
an interactive web UI for cross-company metric comparison and single-company
long-term-investor dashboards.

It computes the metrics that matter for fundamental analysis — margins, leverage,
growth (YoY / CAGR), TTM and annual aggregates, and a composite quality score —
straight from raw quarterly figures, and renders them as interactive charts and
tables.

## Features

- **Cross-company comparison** — overlay any metric for any set of companies on a
  single interactive chart (`/chart/{metric}`), with a data table underneath.
- **Screener** (`/screener`) — every company in one sortable, filterable table:
  current P/E, P/B and dividend yield at the latest exchange close, ROE,
  margins, leverage, growth, score and data-quality flags.
- **Single-company dashboard** (`/company/{name}`) — KPIs, sparklines, a quality
  score with a transparent breakdown, a trend explorer, and free-text notes.
- **Derived analytics** — net/EBITDA/operating margins, Debt/EBITDA, net debt,
  EV, EV/EBIT, free cash flow, P/FCF, P/B, dividend yield, revenue & net-profit
  YoY and 3y/5y CAGR, all computed on quarterly, TTM, or annual bases.
- **Two ingestion paths** — CSV import and a free primary-source fetcher
  (ГИР БО + MOEX ISS for companies, CBR forms 102/101 for banks).
- **Automatic refresh** — with `SCHEDULER_ENABLED=1` the server itself refreshes
  quotes daily and financials weekly, catching up slots missed while it was
  down; `/updates` shows the timetable, quote freshness and every run's outcome.
- **Self-contained binaries** — migrations and templates are embedded, so every
  binary runs from any working directory with nothing on disk beside it.
- **Light/dark themes** on every page.

## Architecture

```
cmd/
  plot    HTTP server (UI + chart/dashboard pages + JSON API)
  import  one-shot CSV ingestion
  fetch   free primary-source fetcher (ГИР БО RSBU + MOEX market cap)
internal/
  fetcher      the fetch pipelines, shared by cmd/fetch and the scheduler
  scheduler    timetable that runs the fetcher inside cmd/plot
  application  composition root (wires config → db → repo)
  config       env-var configuration + validation
  database     connection pool, repository, embedded migrations
  handlers     chi HTTP handlers (one file per route)
  analytics    metric derivation (margins, growth, TTM/annual, score)
  parser       semicolon-CSV parser
  scraper/     girbo, moex, cbr data sources + shared httpx retries
  version      build metadata stamped via -ldflags
migrations/    golang-migrate SQL (embedded into the binaries)
templates/     server-rendered HTML (embedded)
```

The data model is a single fact table `company_financials`, keyed by
`(year, quarter, company)` with one column per metric, plus a `company_notes`
table, `market_quotes` (latest close per company) and `fetch_runs` (a log of
every data refresh). Writes use a `COALESCE`-based upsert so a partial import never wipes
previously-stored metrics. An unreported metric is stored as `NULL`; a reported
zero from a primary source (e.g. no debt) is stored as `0` (CSV zeros are
treated as placeholders and skipped). All entrypoints run migrations on startup.

## Quick start

### Prerequisites

- Go 1.24+
- PostgreSQL 13+

### Run

```bash
# 1. Start Postgres (example with Docker)
docker run -d --name fa-postgres -p 5432:5432 \
  -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=financialanalyzer postgres:16

# 2. Configure (see Configuration below) and run the server
export DB_PASSWORD=secret DB_NAME=financialanalyzer
make run            # or: go run ./cmd/plot
```

The server listens on `:8088` by default. Open <http://localhost:8088>.

Migrations are applied automatically on startup.

## Data ingestion

```bash
# Import a CSV file (filename encodes company + category: SBER_banks.csv)
CSV_PATH=/path/to/SBER_banks.csv go run ./cmd/import

# Fetch from free primary sources — entries are TICKER:INN:CATEGORY
FETCH_TICKERS="LKOH:7708004767:oil,MGNT:2309085638:retail" go run ./cmd/fetch
FETCH_TICKERS_FILE=/path/to/list.txt go run ./cmd/fetch

# No list: refresh only the companies already in the DB; FETCH_ALL=1 fetches
# every ticker in the bundled registries
go run ./cmd/fetch
FETCH_ALL=1 go run ./cmd/fetch

# Refresh only the latest exchange closes (current P/E etc.)
FETCH_QUOTES_ONLY=1 go run ./cmd/fetch
```

Instead of running `cmd/fetch` by hand, the server can refresh the stored
companies itself: start it with `SCHEDULER_ENABLED=1` and it refreshes quotes
daily at 07:00 and financials (incrementally, then quotes) on Sundays at 05:00,
Moscow time (`SCHEDULE_QUOTES` / `SCHEDULE_FINANCIALS`; `off` disables a job).
Runs are sequential; a slot missed while the server was down is caught up 30 s
after startup. New companies are still added with `cmd/fetch`. Every run, from
either binary, is logged in `fetch_runs` and shown on `/updates`.

Dividends have no free exchange API, so they come from CSV: a row starting with
`Дивиденды` holds the year's total in billions of RUB in the Q4 column (`0` there
means "no payout").

Historical market caps undo later share splits (MOEX's split list plus the
bundled `share_splits.txt` for splits MOEX omits); the fetcher warns when a cap
jumps more than 4× year over year so a missing split can be added.

The primary-source fetcher reports **annual, unconsolidated RSBU** figures, so
P/E and ROE diverge from IFRS aggregators; banks come from separate CBR form
102/101 archives (`FETCH_BANKS`, registry `bank_tickers.txt`; bank revenue is
net interest income + fee income), and EBITDA is left empty; profit from sales
(line 2200) stands in for EBIT in EV/EBIT. Cash, operating cash flow and capex
(lines 1250, 4100, 4221) feed net debt, EV and FCF; rows fetched before these
columns existed get them with `FETCH_FORCE=1 go run ./cmd/fetch`. See package
docs for details.

## Configuration

All configuration is via environment variables (`internal/config`):

| Variable      | Default       | Description                              |
|---------------|---------------|------------------------------------------|
| `PORT`        | `8088`        | HTTP listen port                         |
| `DB_HOST`     | `localhost`   | Postgres host                            |
| `DB_PORT`     | `5432`        | Postgres port                            |
| `DB_USER`     | `postgres`    | Postgres user                            |
| `DB_PASSWORD` | `password`    | Postgres password (**change for prod**)  |
| `DB_NAME`     | `postgres`    | Database name                            |
| `DB_SSLMODE`  | `disable`     | `lib/pq` sslmode                         |
| `CSV_PATH`    | _(empty)_     | CSV file path for `cmd/import`           |
| `AUTH_USER`   | _(empty)_     | Basic Auth user for write endpoints      |
| `AUTH_PASSWORD` | _(empty)_   | Basic Auth password (set with `AUTH_USER`) |
| `SCHEDULER_ENABLED` | _(off)_ | `1` makes `cmd/plot` refresh data on a schedule |
| `SCHEDULE_QUOTES` | `07:00`   | Daily quotes slot, Moscow time (`off` disables) |
| `SCHEDULE_FINANCIALS` | `sun 05:00` | Weekly financials slot, Moscow time (`off` disables) |

The server logs a warning if the default database password is in use, or if
`AUTH_USER`/`AUTH_PASSWORD` are unset (write endpoints are then open). See
[`.env.example`](.env.example).

## HTTP endpoints

| Method | Path                              | Description                          |
|--------|-----------------------------------|--------------------------------------|
| GET    | `/`                               | Comparison UI                        |
| GET    | `/chart/{metric}`                 | Chart + table page (`?companies=`, `?theme=`, `?period=`) |
| GET    | `/company/{name}`                 | Single-company dashboard             |
| GET    | `/screener`                       | Cross-company screener               |
| GET    | `/api/screener`                   | Screener rows as JSON (null = no data) |
| GET    | `/updates`                        | Refresh timetable, quote freshness, run history |
| GET    | `/api/fetch-runs`                 | Scheduler jobs and the latest 50 runs as JSON |
| GET    | `/api/companies`                  | List companies                       |
| DELETE | `/api/companies`                  | Delete a company 🔒                  |
| GET    | `/api/categories`                 | List categories                      |
| GET    | `/api/companies-with-categories`  | Companies with their category        |
| GET/POST/DELETE | `/api/company-note`      | Read / save 🔒 / delete 🔒 a company note |
| GET    | `/healthz`                        | Liveness probe                       |
| GET    | `/readyz`                         | Readiness probe (checks the DB)      |
| GET    | `/version`                        | Build metadata                       |

API errors use a uniform `{"error": "..."}` envelope. 🔒 endpoints require
HTTP Basic Auth when `AUTH_USER`/`AUTH_PASSWORD` are set, and always require a
JSON body (`Content-Type: application/json`) when they carry one.

## Development

```bash
make help        # list all targets
make check       # gofmt-check + vet + test (pre-commit gate)
make test        # run tests
make cover       # tests with coverage summary
make race        # tests with the race detector
make lint        # golangci-lint (v2)
make build       # build all binaries into ./bin with version stamping

# Repository integration tests (skipped by default) need a throwaway Postgres:
TEST_DATABASE_DSN="host=127.0.0.1 port=5433 user=test dbname=test sslmode=disable" \
  go test ./internal/database/
```

Build metadata is stamped via `-ldflags` (see the `Makefile`) and exposed at
`/version` and in the startup log.

## License

[MIT](LICENSE).
