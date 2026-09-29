# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Go application that ingests Russian-language quarterly financial data and serves an interactive web UI for cross-company metric comparison. Three binaries share `internal/`:

- `cmd/import` — one-shot CSV ingestion (set `CSV_PATH`, run, exits).
- `cmd/scrape` — pulls quarterly financials directly from smart-lab.ru for a list of tickers (see "Smart-lab scraper" below). NOTE: smart-lab is now paywalled; `cmd/fetch` is the free replacement.
- `cmd/fetch` — builds financials from free primary sources: annual RSBU from ГИР БО + year-end market cap from MOEX ISS, computing P/E and ROE itself (see "Primary-source fetch" below); listed banks come from the CBR's quarterly form 102 archives (see "Bank fetch").
- `cmd/plot` — HTTP server (default `:8088`) that renders the UI and chart pages.

All three call `database.RunMigrations` on startup, so any entrypoint will bring the schema up to date.

## Commands

```bash
# Run web server (reads env vars, see config.go for defaults)
go run ./cmd/plot

# Import a CSV file (filename encodes company + category, see "CSV format" below)
CSV_PATH=/path/to/SBER_banks.csv go run ./cmd/import

# Scrape smart-lab.ru — explicit ticker:category list
SCRAPE_TICKERS="SBER:banks,LKOH:oil,GAZP:oil" go run ./cmd/scrape

# Scrape smart-lab.ru — re-fetch all tickers already in the DB
go run ./cmd/scrape

# Fetch from free primary sources (ГИР БО + MOEX ISS). Ticker INN/category are
# resolved from the bundled registry (fetch_tickers.txt), so a request can be
# just the ticker; INN/category may still be given explicitly to override.
go run ./cmd/fetch                                    # every ticker in the registry
FETCH_TICKERS="LKOH,GAZP:oil" go run ./cmd/fetch      # registry-resolved
FETCH_TICKERS="MGNT:2309085638:retail" go run ./cmd/fetch  # full explicit form
FETCH_TICKERS_FILE=/path/to/list.txt go run ./cmd/fetch
# Knobs: FETCH_CONCURRENCY (default 3, parallel tickers), FETCH_FORCE (re-fetch
# periods already in the DB instead of skipping them).

# Banks come from the CBR form 102 archives (separate pipeline, separate
# registry bank_tickers.txt mapping TICKER->REGN). Runs by default alongside the
# ГИР БО list; request a subset / limit the year range explicitly:
FETCH_BANKS="SBER,VTBR" FETCH_BANK_FROM_YEAR=2021 go run ./cmd/fetch
# With no FETCH_* var set, both pipelines run over their full bundled registries;
# requesting one kind (FETCH_TICKERS or FETCH_BANKS) suppresses the other.

# Build
go build ./...

# Tests (none exist yet; standard invocation)
go test ./...
```

Both binaries must run from the repo root — `RunMigrations` loads `file://migrations` and `IndexHandler` loads `templates/index.html` as relative paths.

Config is env-var only (`internal/config/config.go`): `PORT`, `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE`, `CSV_PATH`. Defaults target a local Postgres (`localhost:5432`, user `postgres`, password `password`, db `postgres`). The scraper additionally reads `SCRAPE_TICKERS` or `SCRAPE_TICKERS_FILE` (both optional — see below).

## Architecture

**Data model.** `company_financials` is the single fact table, keyed by `(year, quarter, company)` with a `category` column and one column per metric (capitalization, revenue, net_profit, ebitda, debt, pe, roe). `company_notes` holds free-text notes keyed by company. `SaveQuarterData` does an upsert that uses `COALESCE(EXCLUDED.x, existing.x)` so a row imported with only some metrics filled in won't wipe previously-imported metrics for the same quarter.

**CSV ingestion (`internal/parser/csv_parser.go`).** Input is semicolon-delimited with the header row containing quarter labels like `2023-Q1`; `LTM` columns are skipped. Metric rows are matched in two ways:
- Exact prefix match against a handler map (`Капитализация`, `Выручка`, `EBITDA`, `ROE`).
- Substring match for "special" metrics with disambiguation rules (`P/E`, `Долг` but not `Чистый долг`, `Чистая прибыль` but not the `н/с` variant).

Company name and category are parsed from the **filename**: `<COMPANY>_<CATEGORY>.csv` (split on `_`). Numeric values are normalized by stripping spaces, `%`, quotes, and converting `,` → `.`.

**HTTP layer (`cmd/plot/main.go`, `internal/handlers/`).** Routes are registered in `main.go`; each handler is a method on `Controller` (one file per route in `internal/handlers/`). `Controller` holds only `*database.Repository`. `/chart/{metric}` returns a self-contained HTML page (embedded CSS + go-echarts JS + a data table) and supports `?theme=dark|light` and `?companies=A,B,C`. The index page (`templates/index.html`) is loaded fresh on each request — edits don't require restart.

**Adding a metric** requires changes in several places that must stay in sync: `models.QuarterData` (field + `IsEmpty`), a migration to add the column, the `SaveQuarterData` upsert, parser handlers/special-type switch in `csv_parser.go`, the `metrics` slice in `handlers/index.go`, and `formatMetricName`/`getMetricUnit`/`getTooltipFormatter` in `handlers/chart.go`.

**Smart-lab scraper (`internal/scraper/smartlab/`, `cmd/scrape/`).** Pulls `https://smart-lab.ru/q/{TICKER}/f/q/` and parses the `<table class="simple-little-table financials">` block. Metric rows are identified by their stable `field="..."` attribute: `market_cap`, `revenue`, `net_income`, `ebitda`, `debt`, `p_e`, `roe`. Quarter labels come from `tr.header_row` (format `2024Q4`); the trailing LTM column is dropped. Cell values are stored verbatim (matching the CSV convention — monetary fields in billions of RUB). Banks won't have Revenue/EBITDA/Debt populated — that's expected.

Ticker list resolution order in `cmd/scrape`:
1. `SCRAPE_TICKERS="SBER:banks,LKOH:oil"` — comma-separated `TICKER:CATEGORY` pairs.
2. `SCRAPE_TICKERS_FILE=/path/to/list.txt` — one `TICKER CATEGORY` per line, `#` comments allowed.
3. Fallback: re-scrape every company already in `company_financials` (category preserved).

The scraper is polite by default: ~1.2 s between requests, custom User-Agent, single-threaded. The fixture at `internal/scraper/smartlab/testdata/LKOH.html` is the regression test against the real page structure — refresh it (re-curl the URL) if smart-lab changes the markup.

**Primary-source fetch (`internal/scraper/girbo/`, `internal/scraper/moex/`, `cmd/fetch/`).** Free replacement for the smart-lab scraper, sourcing the same `QuarterData` fields from primary data instead of a paywalled aggregator:

- `girbo` pulls annual RSBU statements from ГИР БО (`bo.nalog.gov.ru`). Flow: `SearchByINN` (`/advanced-search/organizations/search?query={inn}` — note the API wraps the matched value in `<strong>` tags, which `parseSearch` strips) → `ListReports` (`/nbo/organizations/{id}/bfo` returns every year with a real report `id`; the `publication` field is a status code, not an id) → details (`/nbo/bfo/{reportID}/details` → `financialResult.current2110` revenue, `current2400` net profit, `balance.current1300` equity, `current1410+current1510` borrowings). Source is in **thousands of RUB**, converted to **billions**.
- `moex` reconstructs market cap from MOEX ISS as `CLOSE × ISSUESIZE` (no historical-cap endpoint exists): `ISSUESIZE` from `/iss/securities/{SECID}.json`, year-end `CLOSE` from the EOD history endpoint. Returns billions of RUB.
- `cmd/fetch` joins them per `(ticker, year)` and computes `PE = cap/net_profit`, `ROE = net_profit/equity*100`.

Inherent limitations (documented in package docs): ГИР БО is **annual-only** (rows are stored at `Q4`, so TTM/quarterly aggregation is degenerate) and **unconsolidated RSBU, not IFRS** (parent-entity figures diverge from group numbers, so P/E/ROE won't match smart-lab; ROE in particular runs high). **Banks are absent** (they report to the Central Bank). **EBITDA is left empty.** Ticker↔INN is not bridged by either API, so the INN comes from the bundled registry (`fetch_tickers.txt`, embedded via `financialanalyzer.TickerRegistry`); add a row there to support a new ticker. `cmd/fetch` runs tickers concurrently (`FETCH_CONCURRENCY`, each worker with its own rate-limited clients), skips `(company, year)` periods already in the DB unless `FETCH_FORCE` is set, and a single failed report year no longer discards the rest of the ticker (`girbo.FetchAnnual` collects partial results). Fixtures in each package's `testdata/` are the regression tests; refresh by re-curling if the APIs change.

**Bank fetch (`internal/scraper/cbr/`, banks branch of `cmd/fetch/`).** Banks file form 102 with the Central Bank, not ГИР БО, so they have their own pipeline. The CBR publishes the whole banking sector's form 102 ("Отчёт о финансовых результатах") per period as a RAR archive of DBF tables at `https://www.cbr.ru/vfs/credit/forms/102-YYYYMMDD.rar` (`.zip` before 2009; the page only lists old archives but recent dates resolve directly). Flow: `cbr.Client.FetchPeriod(archiveDate)` downloads the RAR (decoded with `github.com/nwaples/rardecode/v2`, RAR5), extracts the `*_P1.dbf` data table (a tiny hand-rolled `parseDBF` reads the dBASE III header + fixed-width records — only ASCII columns REGN/CODE/SIM_ITOGO are needed, so cp866 text is irrelevant), and returns `REGN -> cumulative net profit (billions)`. Net profit after tax is **form 102 CODE 61101** (61102 is the loss counterpart); DBF values are **thousands of RUB**. The archive date is the first day *after* the period and figures are **YTD cumulative** (`YYYY0401`=Q1, `0701`=H1, `1001`=9M, next-year `0101`=full year — see `cbr.ArchiveDate`). `cmd/fetch` downloads a year's four archives once, **differences consecutive cumulatives** into true single-quarter net profit per bank, and attaches year-end market cap + annual P/E (cap / full-year profit) + ROE to the Q4 row. ROE's equity base is **total regulatory capital (Basel III own funds), form 123 line "000"**, fetched by `cbr.FetchCapital` from the analogous `123-YYYYMMDD.rar` archive (year-end balance; data table `*_123D.dbf`, columns REGN/C1/C3, also thousands of RUB); ROE = full-year net profit / year-end capital × 100 (regulatory capital, not balance-sheet equity, so it's approximate). Banks are keyed by CBR **REGN** (from `bank_tickers.txt`, embedded via `financialanalyzer.BankRegistry`); the ticker doubles as the MOEX secid for cap. Revenue/EBITDA/debt have no clean form 102 line (smart-lab leaves them empty for banks too) and stay empty. Fixtures `internal/scraper/cbr/testdata/102-20240101.rar` (Sberbank REGN 1481 net profit = 1493.1 bln) and `123-20240101.rar` (Sberbank capital = 6265.2 bln, ROE ≈ 23.8%) are the regression tests — both exact matches to published figures; a synthetic in-test DBF covers the parser directly.

**Migrations.** Standard `golang-migrate` numbered up/down files in `migrations/`. Migrations run automatically on every startup of any binary; new migrations take effect with no separate step.
