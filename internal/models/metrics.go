package models

type CompanyMetric struct {
	Year    int
	Quarter string
	Company string
	Value   float64
}

type QuarterPoint struct {
	Key   string
	Value float64
}
