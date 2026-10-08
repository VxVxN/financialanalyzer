package analytics

import (
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func TestRSBUOnlyLatest(t *testing.T) {
	f := models.Float
	hist := map[string][]models.QuarterData{
		"LKOH": {{Year: 2024, Quarter: "Q4", Source: models.SourceRSBU, Revenue: f(1)}},
		"X5": {
			{Year: 2023, Quarter: "Q4", Source: models.SourceRSBU, Revenue: f(1)},
			{Year: 2024, Quarter: "Q4", Source: models.SourceManual, Revenue: f(2)},
		},
		"GAZP": {{Year: 2024, Quarter: "Q4", Source: models.SourceCSV, Revenue: f(1)}},
		"SBER": {{Year: 2024, Quarter: "Q4", Source: models.SourceCBR102, NetProfit: f(1)}},
	}
	got := RSBUOnlyLatest([]string{"LKOH", "X5", "GAZP", "SBER", "NVTK"}, hist)
	if len(got) != 1 || got[0].Company != "LKOH" || got[0].Year != 2024 {
		t.Fatalf("gaps = %+v, want LKOH 2024", got)
	}
}
