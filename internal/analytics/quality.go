package analytics

import (
	"fmt"
	"sort"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// Data-quality checks. They don't prove a figure is wrong; they flag rows whose
// numbers look like the wrong entity (a holding company's standalone RSBU
// instead of group IFRS) or a unit mistake, so the UI can warn before the value
// is compared with other companies.

// Thresholds for CheckRow.
const (
	maxSanePE          = 200.0 // P/E above this means earnings ~0 or the wrong entity
	maxSaneROE         = 100.0 // |ROE| above this, in percent
	minRevenueCapRatio = 0.01  // revenue below 1% of market cap
)

// Anomaly is one suspicious figure in one reporting period.
type Anomaly struct {
	Year    int
	Quarter string
	Label   string   // "YYYY-Qn", same format as quarterly Point labels
	Metrics []string // raw metrics the anomaly concerns (see Concerns)
	Message string
}

// CheckRow returns the anomalies found in a single period's row.
func CheckRow(q models.QuarterData) []Anomaly {
	var out []Anomaly
	add := func(msg string, metrics ...string) {
		out = append(out, Anomaly{
			Year: q.Year, Quarter: q.Quarter, Label: qLabel(q.Year, q.Quarter),
			Metrics: metrics, Message: msg,
		})
	}

	// Unreported metrics are NaN, and every comparison with NaN is false, so
	// a missing figure never triggers a check.
	pe, roe := models.ValueOrNaN(q.PE), models.ValueOrNaN(q.ROE)
	revenue, capitalization := models.ValueOrNaN(q.Revenue), models.ValueOrNaN(q.Capitalization)
	netProfit := models.ValueOrNaN(q.NetProfit)
	equity := models.ValueOrNaN(q.Equity)

	// Negative P/E just means a loss-making period — normal, not suspicious.
	if pe > maxSanePE {
		add(fmt.Sprintf("P/E %.0f is above %.0f — earnings near zero or not the group's", pe, maxSanePE), "pe")
	}
	if roe > maxSaneROE || roe < -maxSaneROE {
		add(fmt.Sprintf("ROE %.0f%% is beyond ±%.0f%%", roe, maxSaneROE), "roe")
	}
	if equity < 0 {
		add("negative equity — P/B and ROE are not meaningful", "equity", "roe")
	}
	// A reported zero revenue counts: a holding with no sales is exactly the
	// case this check is for.
	if revenue >= 0 && capitalization > 0 && revenue < capitalization*minRevenueCapRatio {
		add(fmt.Sprintf("revenue is below %.0f%% of market cap — likely holding-level figures", minRevenueCapRatio*100),
			"revenue", "capitalization")
	}
	// A parent company living on dividends from subsidiaries books them below
	// revenue, so its net profit can exceed revenue. For an operating company
	// that essentially never happens. A bank's revenue is only a proxy (net
	// interest + fee income) that leaves out provision releases, FX/securities
	// gains and subsidiaries' dividends, so a quarter with such a gain can
	// legitimately out-earn it; the check is skipped for CBR rows.
	if q.Source != models.SourceCBR102 && revenue >= 0 && netProfit > revenue {
		add("net profit exceeds revenue — income is likely dividends from subsidiaries (holding-level figures)",
			"revenue", "net_profit", "pe", "roe")
	}
	return out
}

// CheckHistory runs CheckRow over a history, returning anomalies chronologically.
func CheckHistory(history []models.QuarterData) []Anomaly {
	var out []Anomaly
	for _, q := range SortHistory(history) {
		out = append(out, CheckRow(q)...)
	}
	return out
}

// metricInputs lists the raw metrics a (possibly derived) metric is computed from.
func metricInputs(metric string) []string {
	switch metric {
	case "net_margin":
		return []string{"net_profit", "revenue"}
	case "ebitda_margin":
		return []string{"ebitda", "revenue"}
	case "debt_ebitda":
		return []string{"debt", "ebitda"}
	case "pb":
		return []string{"capitalization", "equity"}
	case "div_yield":
		return []string{"dividends", "capitalization"}
	case "revenue_yoy", "revenue_cagr3", "revenue_cagr5":
		return []string{"revenue"}
	case "net_profit_yoy", "net_profit_cagr3", "net_profit_cagr5":
		return []string{"net_profit"}
	case "ebitda_yoy":
		return []string{"ebitda"}
	}
	return []string{metric}
}

// Concerns reports whether the anomaly affects the given (raw or derived) metric.
func (a Anomaly) Concerns(metric string) bool {
	for _, in := range metricInputs(metric) {
		for _, m := range a.Metrics {
			if m == in {
				return true
			}
		}
	}
	return false
}

// AnomaliesByLabel indexes the anomalies that concern metric by the Point label
// they fall on for the given period: "YYYY-Qn" for quarterly/TTM, "YYYY" for
// annual (any flagged quarter marks its year).
func AnomaliesByLabel(anomalies []Anomaly, metric string, period Period) map[string][]Anomaly {
	out := map[string][]Anomaly{}
	for _, a := range anomalies {
		if !a.Concerns(metric) {
			continue
		}
		label := a.Label
		if period == PeriodAnnual {
			label = formatYear(a.Year)
		}
		out[label] = append(out[label], a)
	}
	return out
}

// Sources returns the distinct non-empty sources present in a history, sorted.
func Sources(history []models.QuarterData) []string {
	seen := map[string]bool{}
	var out []string
	for _, q := range history {
		if q.Source != "" && !seen[q.Source] {
			seen[q.Source] = true
			out = append(out, q.Source)
		}
	}
	sort.Strings(out)
	return out
}

// SourceLabel is the short human name of a source.
func SourceLabel(source string) string {
	switch source {
	case models.SourceRSBU:
		return "RSBU (issuer)"
	case models.SourceCBR102:
		return "CBR forms 102/101"
	case models.SourceCSV:
		return "CSV import"
	case models.SourceSmartLab:
		return "smart-lab"
	case "":
		return "unknown"
	}
	return "other" // never echo an unrecognised DB value into the UI
}

// SourceNote explains how comparable a source's figures are.
func SourceNote(source string) string {
	switch source {
	case models.SourceRSBU:
		return "Annual standalone RSBU of the listed legal entity from ГИР БО, not consolidated IFRS. " +
			"For holding companies revenue/profit are mostly intra-group dividends, so P/E and ROE are not comparable."
	case models.SourceCBR102:
		return "Bank-only RSBU from CBR forms 102/101, not group IFRS. Revenue is net interest income + fee income (fee expense not netted)."
	case models.SourceCSV:
		return "Imported from a CSV file; accuracy depends on the file (usually group IFRS)."
	case models.SourceSmartLab:
		return "Group IFRS figures aggregated by smart-lab.ru."
	case "":
		return "Loaded before sources were tracked."
	}
	return ""
}

// IsComparable reports whether a source yields group-level figures that can be
// compared across companies (IFRS). RSBU-based sources are not. Unknown
// provenance (legacy rows, "") is treated as neutral — no warning — because
// pre-provenance data came from smart-lab/CSV imports.
func IsComparable(source string) bool {
	return source != models.SourceRSBU && source != models.SourceCBR102
}
