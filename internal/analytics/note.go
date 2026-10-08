package analytics

import (
	"sort"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// StaleQuotes lists stored closes older than QuoteMaxAge at now, by company.
// A quote with no capitalization is not a price and is left out.
func StaleQuotes(quotes map[string]models.MarketQuote, now time.Time) []NoteQuote {
	var out []NoteQuote
	for _, q := range quotes {
		if q.Capitalization <= 0 || q.PriceDate.IsZero() || QuoteIsFresh(q, now) {
			continue
		}
		out = append(out, NoteQuote{Company: q.Company, Date: q.PriceDate.Format("02.01.2006")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Company < out[j].Company })
	return out
}

// CollectJumps lists capitalization jumps across the given histories, by
// company and then by year.
func CollectJumps(histories map[string][]models.QuarterData) []NoteJump {
	var out []NoteJump
	for company, rows := range histories {
		for _, j := range CapJumps(rows) {
			out = append(out, NoteJump{Company: company, From: j.From, To: j.To, Ratio: j.Ratio})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Company != out[j].Company {
			return out[i].Company < out[j].Company
		}
		return out[i].From < out[j].From
	})
	return out
}
