package handlers

import (
	"bufio"
	"strings"

	"github.com/VxVxN/financialanalyzer"
)

// portfolio is the bundled holding list (portfolio.txt), parsed once.
var portfolio = loadPortfolio(financialanalyzer.Portfolio)

// loadPortfolio reads a "TICKER" list. Blank lines and "#" comments are skipped.
func loadPortfolio(text string) map[string]struct{} {
	set := make(map[string]struct{})
	scan := bufio.NewScanner(strings.NewReader(text))
	for scan.Scan() {
		line := scan.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		ticker := strings.ToUpper(strings.TrimSpace(line))
		if ticker != "" {
			set[ticker] = struct{}{}
		}
	}
	return set
}

// inPortfolio reports whether the company is in the bundled portfolio.
func inPortfolio(company string) bool {
	_, ok := portfolio[strings.ToUpper(strings.TrimSpace(company))]
	return ok
}
