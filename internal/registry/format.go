package registry

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Write prints the candidates as a fetch_tickers.txt proposal: the usable
// issuers as registry rows, then the excluded and failed ones as comments, so
// the whole output parses as a registry and every left-out ticker says why.
func Write(w io.Writer, cands []Candidate, now time.Time) error {
	var inc, exc, failed []Candidate
	for _, c := range cands {
		switch c.Verdict {
		case Include:
			inc = append(inc, c)
		case Exclude:
			exc = append(exc, c)
		default:
			failed = append(failed, c)
		}
	}

	b := &strings.Builder{}
	fmt.Fprintf(b, `# Ticker list for the fetcher — MOEX issuers whose annual RSBU is published in
# ГИР БО (bo.nalog.gov.ru).
#
# Format:  TICKER  INN  CATEGORY   (one per line, "#" starts a comment)
#
# Generated on %s from MOEX ISS (TQBR shares, issuer INN,
# sector indexes for the category) and ГИР БО (a report for one of the last
# %d years with revenue of at least %.0f bln RUB). Review the diff before
# committing; rows can be edited by hand (a regeneration will propose them
# again, so keep deliberate edits in mind).
#
# CAVEATS (inherent to the source — see CLAUDE.md / package docs):
#   * Figures are ANNUAL, unconsolidated RSBU (parent legal entity), NOT IFRS,
#     so P/E and ROE differ from group-level numbers, sometimes a lot.
#     "ХОЛДИНГ" marks issuers whose parent profit exceeds its revenue.
#   * Rows land at Q4 of each year (no quarterly breakdown exists in ГИР БО).
#   * EBITDA is not populated (no single RSBU line for it).
#   * Banks are not here: they come from the CBR (bank_tickers.txt).

`, now.Format(time.DateOnly), recentYears, MinRevenue)

	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, c := range inc {
		fmt.Fprintf(tw, "%s\t%s\t%s\t# %s\n", c.Ticker, c.INN, c.Category, includeNote(c))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if len(exc) > 0 {
		fmt.Fprintf(b, "\n# %s\n# NOT AVAILABLE via this pipeline (%d):\n#\n", strings.Repeat("=", 76), len(exc))
		writeComments(b, exc)
	}
	if len(failed) > 0 {
		fmt.Fprintf(b, "\n# %s\n# CHECK FAILED — rerun with REGISTRY_TICKERS to decide (%d):\n#\n", strings.Repeat("=", 76), len(failed))
		writeComments(b, failed)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// includeNote is a registry row's comment: issuer, latest figures, flags.
func includeNote(c Candidate) string {
	parts := []string{oneLine(c.Title)}
	if c.Latest != nil {
		parts = append(parts, fmt.Sprintf("РСБУ %d: выручка %s, прибыль %s",
			c.Latest.Year, fmtBln(c.Latest.Revenue), fmtBln(c.Latest.NetProfit)))
	}
	if c.Holding {
		parts = append(parts, "ХОЛДИНГ: прибыль > выручки")
	}
	if len(c.Preferred) > 0 {
		parts = append(parts, "также "+strings.Join(c.Preferred, ","))
	}
	if !c.Known {
		parts = append(parts, "НОВЫЙ")
	} else if c.Previous != "" {
		parts = append(parts, "в реестре был ИНН "+c.Previous)
	}
	return strings.Join(nonEmpty(parts), "; ")
}

// writeComments lists left-out issuers as aligned comment lines.
func writeComments(b *strings.Builder, cands []Candidate) {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, c := range cands {
		reason := c.Reason
		if c.Known {
			reason += " (сейчас в реестре)"
		}
		inn := c.INN
		if inn == "" {
			inn = "-"
		}
		fmt.Fprintf(tw, "#  %s\t%s\t%s\n", c.Ticker, inn, oneLine(reason))
	}
	_ = tw.Flush() // writes into a strings.Builder, which cannot fail
}

func nonEmpty(ss []string) []string {
	out := ss[:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// oneLine keeps an error message on its comment line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
