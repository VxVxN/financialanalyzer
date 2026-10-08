// Package financialanalyzer holds assets embedded into the binaries so that any
// entrypoint is a self-contained executable that runs from any working
// directory — no reliance on migrations/ or templates/ being present on disk
// next to the process.
package financialanalyzer

import "embed"

// MigrationsFS contains the golang-migrate SQL files. Consumers mount it through
// the iofs source driver (see internal/database.RunMigrations).
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// TemplatesFS contains server-side HTML templates rendered by the web handlers.
//
//go:embed templates/*.html
var TemplatesFS embed.FS

// StaticFS holds the web UI's shared stylesheet, script and fonts, served
// under /static/ (see handlers.StaticHandler).
//
//go:embed static
var StaticFS embed.FS

// TickerRegistry is the bundled "TICKER INN CATEGORY" list (fetch_tickers.txt)
// used by the fetcher to resolve a ticker's legal-entity INN and default category
// without the caller having to retype them. Embedding it keeps the server
// self-contained regardless of the working directory.
//
//go:embed fetch_tickers.txt
var TickerRegistry string

// BankRegistry is the bundled "TICKER REGN CATEGORY" list (bank_tickers.txt)
// used by the fetcher to fetch exchange-listed banks' figures from the Central
// Bank's form 102/101 archives, keyed by CBR registration number rather than INN.
//
//go:embed bank_tickers.txt
var BankRegistry string

// SplitRegistry is the bundled "SECID TRADEDATE BEFORE AFTER" list
// (share_splits.txt) of splits missing from MOEX's split list; the fetcher uses
// it to rebuild historical share counts for market caps.
//
//go:embed share_splits.txt
var SplitRegistry string

// RenameRegistry is the bundled "SECID OLD_SECID..." list (ticker_renames.txt)
// of renamed MOEX securities; the fetcher prices years before a rename under the
// old secid.
//
//go:embed ticker_renames.txt
var RenameRegistry string

// Portfolio is the bundled ticker list (portfolio.txt) of companies held in
// the portfolio. The screener, the company search and the company card mark
// them; the fetcher does not read this list.
//
//go:embed portfolio.txt
var Portfolio string
