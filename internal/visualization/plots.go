package visualization

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func PlotMetricComparison(data []models.CompanyMetric, metric string) error {
	p := plot.New()

	p.Title.Text = fmt.Sprintf("%s Comparison by Quarter", metric)
	p.X.Label.Text = "Quarter"
	p.Y.Label.Text = metric
	p.X.Tick.Marker = quarterTicks{}

	if err := plotutil.AddLinePoints(p, preparePlotData(data)...); err != nil {
		return err
	}

	outputDir := "charts"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	filename := filepath.Join(outputDir, fmt.Sprintf("%s_comparison_%d.png", metric, time.Now().Unix()))
	return p.Save(10*vg.Inch, 6*vg.Inch, filename)
}

func preparePlotData(data []models.CompanyMetric) []interface{} {
	companyData := make(map[string][]models.QuarterPoint)

	for _, item := range data {
		key := fmt.Sprintf("%d-%s", item.Year, item.Quarter)
		companyData[item.Company] = append(companyData[item.Company], models.QuarterPoint{
			Key:   key,
			Value: item.Value,
		})
	}

	var result []interface{}
	for company, points := range companyData {
		pts := make(plotter.XYs, len(points))
		for i, p := range points {
			pts[i].X = float64(i)
			pts[i].Y = p.Value
		}
		result = append(result, company, pts)
	}

	return result
}

type quarterTicks struct{}

func (quarterTicks) Ticks(min, max float64) []plot.Tick {
	ticks := []plot.Tick{
		{Value: 0, Label: "Q1-2023"},
		{Value: 1, Label: "Q2-2023"},
		{Value: 2, Label: "Q3-2023"},
		{Value: 3, Label: "Q4-2023"},
		{Value: 4, Label: "Q1-2024"},
		{Value: 5, Label: "Q2-2024"},
	}
	return ticks
}
