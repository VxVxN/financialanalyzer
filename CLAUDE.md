# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Go application that ingests Russian-language quarterly financial data and serves an interactive web UI for cross-company metric comparison. Three binaries share `internal/`:

- `cmd/import` — one-shot CSV ingestion (set `CSV_PATH`, run, exits).
- `cmd/fetch` — builds financials from free primary sources: annual RSBU from ГИР БО + year-end market cap from MOEX ISS, computing P/E and ROE itself (see "Primary-source fetch" below); listed banks come from the CBR's quarterly form 102 archives (see "Bank fetch").
- `cmd/plot` — HTTP server (default `:8088`) that renders the UI and chart pages.

All three call `database.RunMigrations` on startup, so any entrypoint will bring the schema up to date.

## Commands

```bash
# Run web server (reads env vars, see config.go for defaults)
go run ./cmd/plot

# Import a CSV file (filename encodes company + category, see "CSV format" below)
CSV_PATH=/path/to/SBER_banks.csv go run ./cmd/import

# Fetch from free primary sources (ГИР БО + MOEX ISS). Ticker INN/category are
# resolved from the bundled registry (fetch_tickers.txt), so a request can be
# just the ticker; INN/category may still be given explicitly to override.
go run ./cmd/fetch                                    # every ticker in the registry
FETCH_TICKERS="OZON,X5:retail" go run ./cmd/fetch    # registry-resolved
FETCH_TICKERS="MGNT:2309085638:retail" go run ./cmd/fetch  # full explicit form
FETCH_TICKERS_FILE=/path/to/list.txt go run ./cmd/fetch
# Knobs: FETCH_CONCURRENCY (default 3, parallel tickers), FETCH_FORCE (re-fetch
# periods already in the DB instead of skipping them).

# Banks come from the CBR form 102 archives (separate pipeline, separate
# registry bank_tickers.txt mapping TICKER->REGN). Runs by default alongside the
# ГИР БО list; request a subset / limit the year range explicitly:
FETCH_BANKS="T" FETCH_BANK_FROM_YEAR=2009 go run ./cmd/fetch
# With no FETCH_* var set, both pipelines run over their full bundled registries;
# requesting one kind (FETCH_TICKERS or FETCH_BANKS) suppresses the other.

# Build
go build ./...

# Tests. The repository integration tests in internal/database skip unless
# TEST_DATABASE_DSN points at a THROWAWAY Postgres (they truncate its tables).
go test ./...
TEST_DATABASE_DSN="host=127.0.0.1 port=55432 user=test dbname=postgres sslmode=disable" go test ./internal/database/
```

Migrations and templates are embedded (`embed.go`: `financialanalyzer.MigrationsFS` / `TemplatesFS`), so binaries run from any working directory.

Config is env-var only (`internal/config/config.go`): `PORT`, `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE`, `CSV_PATH`, `AUTH_USER`, `AUTH_PASSWORD`. Defaults target a local Postgres (`localhost:5432`, user `postgres`, password `password`, db `postgres`). `AUTH_USER`/`AUTH_PASSWORD` must be set together (`Validate` rejects one without the other).

## Architecture

**Data model.** `company_financials` is the single fact table, keyed by `(year, quarter, company)` with a `category` column and one column per metric (capitalization, revenue, net_profit, ebitda, debt, pe, roe, equity, dividends). Metric fields in `models.QuarterData` are `*float64`: **nil = not reported (NULL), non-nil zero = a real zero** (e.g. a debt-free company). Loaders set only what they know — the zero value is safe, because nil never overwrites a stored value. Build values with `models.Float(v)`; analytics reads them via `models.ValueOrNaN` (NaN is its missing-value convention, so a reported 0 stays 0 in series and snapshots). A `source` column (`models.Source*`: `rsbu`, `cbr_102`, `csv`; `smartlab` survives only on legacy rows from the removed scraper; NULL for older rows) records which pipeline wrote the row; every loader must set `QuarterData.Source`, and the upsert keeps the existing source when the new one is empty. `company_notes` holds free-text notes keyed by company. `SaveQuarterData` does an upsert that uses `COALESCE(EXCLUDED.x, existing.x)` so a row imported with only some metrics filled in won't wipe previously-imported metrics for the same quarter (a consequence: a wrong stored value can be corrected, but not cleared back to NULL, by re-importing). Rows written before the pointer change stored reported zeros as NULL; re-run the loader (`FETCH_FORCE=1` for `cmd/fetch`) to fill them in. All repository methods take a `context.Context` first (handlers pass `r.Context()`).

**CSV ingestion (`internal/parser/csv_parser.go`).** Input is semicolon-delimited with the header row containing quarter labels like `2023-Q1`; `LTM` columns are skipped. Metric rows are matched in two ways:
- Exact prefix match against a handler map (`Капитализация`, `Выручка`, `EBITDA`, `ROE`).
- Substring match for "special" metrics with disambiguation rules (`P/E`, `Долг` but not `Чистый долг`, `Чистая прибыль` but not the `н/с` variant).

Company name and category are parsed from the **filename**: `<COMPANY>_<CATEGORY>.csv` (split on `_`). Numeric values are normalized by stripping spaces, `%`, quotes, and converting `,` → `.`. Any value that parses to 0 is treated as **no data** (aggregator exports use 0 as a placeholder, e.g. a bank's revenue or a loss quarter's P/E) — honest zeros come only from the primary sources.

**HTTP layer (`cmd/plot/main.go`, `internal/handlers/`).** Routes are registered in `newRouter` in `main.go` (tested in `cmd/plot/main_test.go` without a DB); each handler is a method on `Controller` (one file per route in `internal/handlers/`). `Controller` depends on the consumer-side `handlers.Repository` interface. State-changing routes (`DELETE /api/companies`, `POST`/`DELETE /api/company-note`) sit in a group behind `handlers.RequireBasicAuth` (only when `AUTH_USER`/`AUTH_PASSWORD` are set; the browser shows its native login dialog) and `handlers.RequireJSONBody` (always; a body must be `application/json`, which forces a CORS preflight and blocks cross-site form CSRF). Any new mutating route belongs in that group. `/chart/{metric}` returns a self-contained HTML page (embedded CSS + go-echarts JS + a data table) and supports `?theme=dark|light` and `?companies=A,B,C`. The index page (`templates/index.html`) is loaded fresh on each request — edits don't require restart.

**Adding a metric** requires changes in several places that must stay in sync: `models.QuarterData` (`*float64` field + `IsEmpty`), `rawValue` in `analytics/analytics.go`, a migration to add the column, the `SaveQuarterData` upsert, parser handlers/special-type switch in `csv_parser.go`, the `metrics` slice in `handlers/index.go`, and `formatMetricName`/`getMetricUnit`/`getTooltipFormatter` in `handlers/chart.go`.

**Primary-source fetch (`internal/scraper/girbo/`, `internal/scraper/moex/`, `cmd/fetch/`).** Free replacement for the removed smart-lab scraper (smart-lab became paywalled), sourcing `QuarterData` fields from primary data:

- `girbo` pulls annual RSBU statements from ГИР БО (`bo.nalog.gov.ru`). Flow: `SearchByINN` (`/advanced-search/organizations/search?query={inn}` — note the API wraps the matched value in `<strong>` tags, which `parseSearch` strips) → `ListReports` (`/nbo/organizations/{id}/bfo` returns every year with a real report `id`; the `publication` field is a status code, not an id) → details (`/nbo/bfo/{reportID}/details` → `financialResult.current2110` revenue, `current2400` net profit, `balance.current1300` equity, `current1410+current1510` borrowings). Source is in **thousands of RUB**, converted to **billions**.
- `moex` reconstructs market cap from MOEX ISS as `CLOSE × ISSUESIZE` (no historical-cap endpoint exists): `ISSUESIZE` from `/iss/securities/{SECID}.json`, year-end `CLOSE` from the EOD history endpoint. Returns billions of RUB. It also provides **dividends**: `DividendsTotal` sums per-share payouts from `/iss/securities/{SECID}/dividends.json` by **record-date year** × current `ISSUESIZE` → billions. Years before the first MOEX record, or with a non-RUB or amount-less payout, are unknown (nil); any later year without payouts is a real 0 (so a stopped payer shows 0 — only as reliable as ISS's completeness). A `dividends.cursor` block reporting more rows than returned is an error, never a silently truncated list. `ISSUESIZE`, the dividend list and a failed dividend lookup are cached per `Client` (clients are per worker, not concurrency-safe). `Client.BaseURL` is overridable for httptest. The dividends fixture `testdata/dividends_synthetic.json` is **hand-written** (ISS was unreachable when added) — replace it with a real capture.
- `cmd/fetch` joins them per `(ticker, year)`, stores `equity` (line 1300) and `dividends`, and computes `PE = cap/net_profit`, `ROE = net_profit/equity*100`.

Derived valuation metrics (`analytics.DerivedSeries`): **`pb` = capitalization / equity**, **`div_yield` = dividends / capitalization × 100** (trailing: the year's record-date dividends over the year-end cap). Both inputs are point-in-time (not flows), living on the Q4 row; P/B is NaN for non-positive equity, and `CheckRow` flags negative equity (concerning equity/P/B/ROE). The dashboard takes P/B and yield from the latest period that has them and shows that period under the KPI (`Snapshot.PBLabel`/`DivYieldLabel`) — for banks the latest row is usually a profit-only Q1-Q3. **Backfill:** `cmd/fetch` skips periods already stored, so existing rows only get `equity`/`dividends` with `FETCH_FORCE=1`. Payout ratio is intentionally absent: bank Q4 rows hold single-quarter profit, so dividends / Q4 profit would be wrong for them. The CSV parser does not read equity (a `Капитал` prefix would collide with `Капитализация`) or dividends.

Inherent limitations (documented in package docs): ГИР БО is **annual-only** (rows are stored at `Q4`, so TTM/quarterly aggregation is degenerate) and **unconsolidated RSBU, not IFRS** (parent-entity figures diverge from group numbers, so P/E/ROE won't match smart-lab; ROE in particular runs high). **Banks are absent** (they report to the Central Bank). **EBITDA is left empty.** Ticker↔INN is not bridged by either API, so the INN comes from the bundled registry (`fetch_tickers.txt`, embedded via `financialanalyzer.TickerRegistry`); add a row there to support a new ticker. `cmd/fetch` runs tickers concurrently (`FETCH_CONCURRENCY`, each worker with its own rate-limited clients), skips `(company, year)` periods already in the DB unless `FETCH_FORCE` is set, and a single failed report year no longer discards the rest of the ticker (`girbo.FetchAnnual` collects partial results). Fixtures in each package's `testdata/` are the regression tests; refresh by re-curling if the APIs change.

**Bank fetch (`internal/scraper/cbr/`, banks branch of `cmd/fetch/`).** Banks file form 102 with the Central Bank, not ГИР БО, so they have their own pipeline. The CBR publishes the whole banking sector's form 102 ("Отчёт о финансовых результатах") per period as a RAR archive of DBF tables at `https://www.cbr.ru/vfs/credit/forms/102-YYYYMMDD.rar` (`.zip` before 2009; the page only lists old archives but recent dates resolve directly). Flow: `cbr.Client.FetchPeriod(archiveDate)` downloads the RAR (decoded with `github.com/nwaples/rardecode/v2`, RAR5), extracts the `*_P1.dbf` data table (a tiny hand-rolled `parseDBF` reads the dBASE III header + fixed-width records — only ASCII columns REGN/CODE/SIM_ITOGO are needed, so cp866 text is irrelevant), and returns `REGN -> cumulative net profit (billions)`. Net profit after tax is **form 102 CODE 61101** (61102 is the loss counterpart); DBF values are **thousands of RUB**. The archive date is the first day *after* the period and figures are **YTD cumulative** (`YYYY0401`=Q1, `0701`=H1, `1001`=9M, next-year `0101`=full year — see `cbr.ArchiveDate`). `cmd/fetch` downloads a year's four archives once, **differences consecutive cumulatives** into true single-quarter net profit per bank, and attaches year-end market cap + annual P/E (cap / full-year profit) + ROE to the Q4 row. ROE's equity base is **total regulatory capital (Basel III own funds), form 123 line "000"**, fetched by `cbr.FetchCapital` from the analogous `123-YYYYMMDD.rar` archive (year-end balance; data table `*_123D.dbf`, columns REGN/C1/C3, also thousands of RUB); ROE = full-year net profit / year-end capital × 100 (regulatory capital, not balance-sheet equity, so it's approximate). The same capital is stored as the bank's `equity` (so bank P/B is approximate too), and Q4 rows get MOEX `dividends`. Banks are keyed by CBR **REGN** (from `bank_tickers.txt`, embedded via `financialanalyzer.BankRegistry`); the ticker doubles as the MOEX secid for cap. Revenue/EBITDA/debt have no clean form 102 line (smart-lab leaves them empty for banks too) and stay empty. Fixtures `internal/scraper/cbr/testdata/102-20240101.rar` (Sberbank REGN 1481 net profit = 1493.1 bln) and `123-20240101.rar` (Sberbank capital = 6265.2 bln, ROE ≈ 23.8%) are the regression tests — both exact matches to published figures; a synthetic in-test DBF covers the parser directly.

**Data quality (`internal/analytics/quality.go`).** `CheckRow`/`CheckHistory` flag suspicious figures: negative equity, P/E > 200 (negative P/E = a loss, not flagged), |ROE| > 100%, revenue < 1% of market cap, and net profit > revenue (the signature of a holding company's standalone RSBU, where income is subsidiaries' dividends). Each `Anomaly` lists the raw metrics it concerns; `Concerns`/`AnomaliesByLabel` map it onto derived metrics and chart labels. `IsComparable` treats `rsbu`/`cbr_102` as not comparable across companies; `csv`/`smartlab` and legacy NULL sources are neutral. `source` is row-level, last writer wins (see `models.QuarterData`). The chart page shows a Source column, highlights flagged cells (hover = reason) and draws flagged points as triangles; the dashboard shows a "Data quality" block. Note: code inside go-echarts `opts.FuncOpts` is JSON-encoded and injected verbatim, so it must not contain double quotes or backslashes — use `jsStringMap`/`jsString` in `handlers/chart.go` for dynamic strings.

**HTTP retries (`internal/scraper/httpx/`).** All scraper clients (girbo, moex, cbr) fetch through `httpx.Get`, which retries transport errors, 429 and 5xx with exponential backoff (`httpx.DefaultPolicy`: 3 attempts, 1s/2s, honours `Retry-After`, and gives up instead of retrying early when it exceeds `MaxDelay`); clients reset their rate-limit clock after the last attempt; other statuses return an `*httpx.StatusError` immediately (cbr maps 404 to `ErrNotPublished`). Each client's `Retry` field overrides the policy (tests use millisecond delays). Rate limiting (`Delay`) stays per client.

**Migrations.** Standard `golang-migrate` numbered up/down files in `migrations/`. Migrations run automatically on every startup of any binary; new migrations take effect with no separate step.
