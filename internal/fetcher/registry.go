package fetcher

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/VxVxN/financialanalyzer"
	"github.com/VxVxN/financialanalyzer/internal/database"
	"github.com/VxVxN/financialanalyzer/internal/scraper/moex"
)

// ---- Ticker resolution ------------------------------------------------------

type tickerSpec struct {
	Ticker   string
	INN      string
	Category string
}

// loadRegistry parses the embedded fetch_tickers.txt into a ticker->spec map.
func loadRegistry() (map[string]tickerSpec, error) {
	registry := make(map[string]tickerSpec)
	scan := bufio.NewScanner(strings.NewReader(financialanalyzer.TickerRegistry))
	for scan.Scan() {
		if spec, ok := parseRegistryLine(scan.Text()); ok {
			registry[spec.Ticker] = spec
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return registry, nil
}

// RegistryEntry is one bundled registry row.
type RegistryEntry struct {
	INN      string
	Category string
}

// RegistryEntries returns the bundled registry (fetch_tickers.txt) by ticker,
// for cmd/registry to keep known rows' names and categories and mark new and
// changed ones.
func RegistryEntries() (map[string]RegistryEntry, error) {
	registry, err := loadRegistry()
	if err != nil {
		return nil, err
	}
	out := make(map[string]RegistryEntry, len(registry))
	for t, spec := range registry {
		out[t] = RegistryEntry{INN: spec.INN, Category: spec.Category}
	}
	return out, nil
}

// parseRegistryLine reads one "TICKER INN CATEGORY" registry line; the INN is
// mandatory there (registry entries that lack it are skipped as malformed).
func parseRegistryLine(line string) (tickerSpec, bool) {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return tickerSpec{}, false
	}
	spec := tickerSpec{
		Ticker: strings.ToUpper(fields[0]),
		INN:    fields[1],
	}
	if len(fields) > 2 {
		spec.Category = strings.Join(fields[2:], " ")
	}
	if !isAllDigits(spec.INN) {
		return tickerSpec{}, false
	}
	return spec, true
}

// loadTickerSpecs resolves the request's ticker list (Tickers, else
// TickersFile; the caller checks that one is set).
func loadTickerSpecs(req Request, registry map[string]tickerSpec) ([]tickerSpec, error) {
	if req.Tickers != "" {
		return parseTickerSpecList(req.Tickers, registry), nil
	}
	if req.TickersFile != "" {
		return readTickerSpecFile(req.TickersFile, registry)
	}
	return nil, nil
}

// storedSpecs picks the registry entries for companies already in the DB, so a
// default run refreshes what is there instead of pulling the whole registry.
// A stored name must equal the ticker exactly (fetch stores tickers
// upper-cased; anything else would create a second company), and its stored
// category is kept. Companies in neither registry (CSV-only) are skipped here;
// they still get quotes.
func storedSpecs(stored []database.CompanyWithCategory, registry map[string]tickerSpec, bankRegistry map[string]bankSpec) ([]tickerSpec, []bankSpec) {
	var specs []tickerSpec
	var banks []bankSpec
	seen := make(map[string]struct{}, len(stored))
	for _, c := range stored {
		if _, dup := seen[c.Company]; dup {
			continue
		}
		seen[c.Company] = struct{}{}
		if b, ok := bankRegistry[c.Company]; ok {
			if c.Category != "" {
				b.Category = c.Category
			}
			banks = append(banks, b)
			continue
		}
		if s, ok := registry[c.Company]; ok {
			if c.Category != "" {
				s.Category = c.Category
			}
			specs = append(specs, s)
		}
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Ticker < specs[j].Ticker })
	sort.Slice(banks, func(i, j int) bool { return banks[i].Ticker < banks[j].Ticker })
	return specs, banks
}

// registrySpecs returns every registry entry, sorted by ticker for stable runs.
func registrySpecs(registry map[string]tickerSpec) []tickerSpec {
	out := make([]tickerSpec, 0, len(registry))
	for _, spec := range registry {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticker < out[j].Ticker })
	return out
}

// parseTickerSpecList parses a comma-separated request list, e.g.
// "LKOH,GAZP:oil,MGNT:2309085638:retail".
func parseTickerSpecList(raw string, registry map[string]tickerSpec) []tickerSpec {
	parts := strings.Split(raw, ",")
	specs := make([]tickerSpec, 0, len(parts))
	for _, p := range parts {
		if spec, ok := resolveSpec(splitFields(p), registry); ok {
			specs = append(specs, spec)
		}
	}
	return specs
}

// readTickerSpecFile reads "TICKER [INN] [CATEGORY]" per line; "#" starts a
// comment. Missing INN/category are filled from the registry.
func readTickerSpecFile(path string, registry map[string]tickerSpec) ([]tickerSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var specs []tickerSpec
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if spec, ok := resolveSpec(splitFields(line), registry); ok {
			specs = append(specs, spec)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return specs, nil
}

// splitFields splits a request entry on ":" or, failing that, on whitespace.
func splitFields(s string) []string {
	if strings.Contains(s, ":") {
		return strings.Split(s, ":")
	}
	return strings.Fields(s)
}

// resolveSpec turns request fields ("TICKER", "TICKER CATEGORY", or
// "TICKER INN CATEGORY") into a spec, filling INN/category from the registry
// when omitted. An all-digit field is treated as the INN, any other trailing
// field as the category. Returns false only for an empty entry; an unknown
// ticker given without an INN comes back with an empty INN, which
// fillMissingINN then looks up.
func resolveSpec(fields []string, registry map[string]tickerSpec) (tickerSpec, bool) {
	if len(fields) == 0 {
		return tickerSpec{}, false
	}
	ticker := strings.ToUpper(strings.TrimSpace(fields[0]))
	if ticker == "" {
		return tickerSpec{}, false
	}

	// Start from the registry default (zero value if the ticker is unknown).
	base := registry[ticker]
	spec := tickerSpec{Ticker: ticker, INN: base.INN, Category: base.Category}

	var catTokens []string
	for _, f := range fields[1:] {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if isAllDigits(f) {
			spec.INN = f
			continue
		}
		catTokens = append(catTokens, f)
	}
	if len(catTokens) > 0 {
		spec.Category = strings.Join(catTokens, " ")
	}
	return spec, true
}

// innLookup returns the issuer INN of a MOEX secid.
type innLookup func(ctx context.Context, secid string) (string, error)

// issINNLookup resolves INNs through MOEX ISS (security description -> ISIN
// -> issuer). Only explicitly requested tickers go through it: a scheduled
// run over stored companies relies on the registry alone, so an ISS outage
// cannot fail it. The client is made on the first lookup.
func issINNLookup(logger *slog.Logger) innLookup {
	var c *moex.Client
	return func(ctx context.Context, secid string) (string, error) {
		if c == nil {
			c = moex.NewClient()
			c.Logger = logger
		}
		e, err := c.EmitterOf(ctx, secid)
		return e.INN, err
	}
}

// fillMissingINN completes the specs that have no INN (tickers missing from
// the registry and given without one) by looking the issuer up. A bank
// ticker is not looked up — banks file with the CBR, not ГИР БО — and, like a
// failed lookup, is returned in failed so the run reports it instead of
// dropping it silently. An INN the registry already has under another ticker
// (a preferred share of a registered issuer) is warned about: the rows would
// be stored as a second company with the same figures.
func fillMissingINN(ctx context.Context, specs []tickerSpec, registry map[string]tickerSpec, banks map[string]bankSpec, lookup innLookup, logger *slog.Logger) (resolved []tickerSpec, failed []string) {
	resolved = make([]tickerSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.INN != "" {
			resolved = append(resolved, spec)
			continue
		}
		if _, ok := banks[spec.Ticker]; ok {
			logger.Warn("Ticker is a bank: request it with FETCH_BANKS", "ticker", spec.Ticker)
			failed = append(failed, spec.Ticker)
			continue
		}
		if ctx.Err() != nil {
			failed = append(failed, spec.Ticker)
			continue
		}
		inn, err := lookup(ctx, spec.Ticker)
		if err == nil && !isAllDigits(inn) {
			err = fmt.Errorf("malformed INN %q", inn)
		}
		if err != nil {
			logger.Warn("Ticker not in the registry and its INN cannot be resolved via MOEX ISS; give it as TICKER:INN",
				"ticker", spec.Ticker, "error", err)
			failed = append(failed, spec.Ticker)
			continue
		}
		logger.Info("INN resolved via MOEX ISS (add the ticker to fetch_tickers.txt to pin it)",
			"ticker", spec.Ticker, "inn", inn)
		for _, r := range registry {
			if r.INN == inn {
				logger.Warn("The issuer is already registered under another ticker: this one will be a separate company with the same figures",
					"ticker", spec.Ticker, "registered", r.Ticker)
				break
			}
		}
		spec.INN = inn
		resolved = append(resolved, spec)
	}
	return resolved, failed
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
