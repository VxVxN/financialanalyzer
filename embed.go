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

// TickerRegistry is the bundled "TICKER INN CATEGORY" list (fetch_tickers.txt)
// used by cmd/fetch to resolve a ticker's legal-entity INN and default category
// without the caller having to retype them. Embedding it keeps the fetch binary
// self-contained regardless of the working directory.
//
//go:embed fetch_tickers.txt
var TickerRegistry string

// BankRegistry is the bundled "TICKER REGN CATEGORY" list (bank_tickers.txt)
// used by cmd/fetch to fetch exchange-listed banks' net profit from the Central
// Bank's form 102 archives, keyed by CBR registration number rather than INN.
//
//go:embed bank_tickers.txt
var BankRegistry string
