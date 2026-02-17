package models

type QuarterData struct {
	Year           int
	Quarter        string
	Company        string
	Category       string
	Capitalization float64
	Revenue        float64
	NetProfit      float64
	EBITDA         float64
	Debt           float64
	PE             float64
	ROE            float64
}

func (q *QuarterData) IsEmpty() bool {
	return q.Capitalization == 0 && q.Revenue == 0 &&
		q.NetProfit == 0 && q.EBITDA == 0 &&
		q.Debt == 0 && q.PE == 0 && q.ROE == 0
}
