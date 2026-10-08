package analytics

import (
	"math"
	"strings"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func TestCapJumps(t *testing.T) {
	f := models.Float
	row := func(y int, q string, c float64) models.QuarterData {
		return models.QuarterData{Year: y, Quarter: q, Capitalization: f(c)}
	}
	// BELU before the split fix: 2023 cap 675.9 -> 2024 64.7.
	rows := []models.QuarterData{
		row(2025, "Q4", 51.6), row(2023, "Q4", 675.9), row(2024, "Q4", 64.7),
		row(2021, "Q4", 423), row(2022, "Q4", 350),
		{Year: 2020, Quarter: "Q4"},
	}
	got := CapJumps(rows)
	if len(got) != 1 || got[0].From != 2023 || got[0].To != 2024 || got[0].Quarter != "Q4" ||
		math.Abs(got[0].Ratio-64.7/675.9) > 1e-9 {
		t.Errorf("CapJumps = %+v, want one 2023->2024 jump", got)
	}
	// A gap year is not consecutive. A clean 1:2 is; a 1.5× move is not.
	if got := CapJumps([]models.QuarterData{row(2020, "Q4", 10), row(2022, "Q4", 100)}); len(got) != 0 {
		t.Errorf("gap years flagged: %+v", got)
	}
	if got := CapJumps([]models.QuarterData{row(2020, "Q4", 100), row(2021, "Q4", 200)}); len(got) != 1 || got[0].Ratio != 2 {
		t.Errorf("clean 1:2 = %+v, want one jump of 2", got)
	}
	if got := CapJumps([]models.QuarterData{row(2020, "Q4", 100), row(2021, "Q4", 150)}); len(got) != 0 {
		t.Errorf("1.5× flagged: %+v", got)
	}
	// Within a year the latest quarter wins, and that point is the one to mark.
	got = CapJumps([]models.QuarterData{row(2020, "Q4", 100), row(2021, "Q2", 50), row(2021, "Q4", 10)})
	if len(got) != 1 || got[0].Quarter != "Q4" || math.Abs(got[0].Ratio-0.1) > 1e-9 {
		t.Errorf("latest quarter = %+v, want Q4 at 0.1×", got)
	}
}

func TestCapJumpAnomalies(t *testing.T) {
	f := models.Float
	rows := []models.QuarterData{
		{Year: 2023, Quarter: "Q4", Capitalization: f(100)},
		{Year: 2024, Quarter: "Q4", Capitalization: f(250)},
	}
	anoms := CapJumpAnomalies(rows)
	if len(anoms) != 1 || anoms[0].Label != "2024-Q4" || len(anoms[0].Metrics) != 1 || anoms[0].Metrics[0] != "capitalization" {
		t.Fatalf("anomalies = %+v", anoms)
	}
	if !strings.Contains(anoms[0].Message, "выросла") || !strings.Contains(anoms[0].Message, "2023") {
		t.Errorf("message = %q", anoms[0].Message)
	}
	flags := AnomaliesByLabel(anoms, "capitalization", PeriodAnnual)
	if len(flags["2024"]) != 1 {
		t.Errorf("annual flags = %+v, want the 2024 point", flags)
	}
	if len(AnomaliesByLabel(anoms, "revenue", PeriodQuarter)) != 0 {
		t.Error("a cap jump must not flag revenue")
	}
}
