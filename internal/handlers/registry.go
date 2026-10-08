package handlers

import (
	"bufio"
	"sort"
	"strings"
	"unicode"

	"github.com/VxVxN/financialanalyzer"
)

// registryTickers is the bundled fetch_tickers.txt, ticker only.
// Parsed here so the updates page does not import the fetcher.
func registryTickers() []string {
	seen := map[string]struct{}{}
	var out []string
	scan := bufio.NewScanner(strings.NewReader(financialanalyzer.TickerRegistry))
	for scan.Scan() {
		line := scan.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !allDigits(fields[1]) {
			continue
		}
		ticker := strings.ToUpper(fields[0])
		if _, ok := seen[ticker]; ok {
			continue
		}
		seen[ticker] = struct{}{}
		out = append(out, ticker)
	}
	sort.Strings(out)
	return out
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
