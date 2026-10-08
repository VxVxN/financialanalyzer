package ifrs

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

func TestParseX5PrefersIFRS16(t *testing.T) {
	const text = `
ОСНОВНЫЕ ПОКАЗАТЕЛИ ФИНАНСОВЫХ РЕЗУЛЬТАТОВ ( до применения МСФО ( IFRS) 16)
МЛН РУБ.
ВЫРУЧКА
1 237 965
1 077 780
14,9
4 642 034
3 908 047
18,8
ЧИСТАЯ ПРИБЫЛЬ
18 400
15 408
19,4
94 803
110 091
(13,9)
` + "\f" + `
ПРИЛОЖЕНИЕ
Основные показатели финансовых результатов (МСФО (
IFRS) 16)
МЛН РУБ.
ВЫРУЧКА
1 237 965
1 077 780
14,9
4 642 034
3 908 047
18,8
EBITDA
25
122 748
90 992
34,9
440 676
399 390
10,3
ОПЕРАЦИОННАЯ ПРИБЫЛЬ
69 835
42 912
62,7
247 644
218 138
13,5
ЧИСТАЯ ПРИБЫЛЬ
17 410
12 921
34,7
83 139
104 064
(20,1)
Чистые денежные потоки от операционной
деятельности
110 038
85 525
28,7
300 569
277 383
8,4
` + "\f" + `
ДО ПРИМЕНЕНИЯ МСФО (
IFRS)
16
Общий долг
20
406 962
288 767
229 907
`
	m, ok := Parse(text)
	if !ok {
		t.Fatal("expected figures")
	}
	assertNear(t, "revenue", m.Revenue, 4642.03)
	assertNear(t, "net_profit", m.NetProfit, 83.14)
	assertNear(t, "ebitda", m.EBITDA, 440.68)
	assertNear(t, "operating_profit", m.OperatingProfit, 247.64)
	assertNear(t, "ocf", m.OperatingCashFlow, 300.57)
	assertNear(t, "debt", m.Debt, 406.96)
}

func TestParseBeluMillions(t *testing.T) {
	const text = `
Выручка(млнруб.)
149266(+10%)
Чистаяприбыль(млнруб.)
5169(+13%)
` + "\f" + `
Продажи,млндекалитров
Выручка
149266
135464
+10%
EBITDA
21204
18658
+14%
Операционнаяприбыль
14143
12345
+15%
Чистаяприбыль
5169
4588
+13%
` + "\f" + `
(Всесуммыприведенывмлн.руб.,еслипрямонеуказанодругое)
Денежныесредстваиихэквиваленты
18322
22521
Всегокапиталирезервы
22363
26682
Долгосрочныекредитыиоблигации
30207
30610
Краткосрочныекредитыиоблигации
7382
3802
`
	m, ok := Parse(text)
	if !ok {
		t.Fatal("expected figures")
	}
	assertNear(t, "revenue", m.Revenue, 149.27)
	assertNear(t, "net_profit", m.NetProfit, 5.17)
	assertNear(t, "ebitda", m.EBITDA, 21.20)
	assertNear(t, "operating_profit", m.OperatingProfit, 14.14)
	assertNear(t, "equity", m.Equity, 22.36)
	assertNear(t, "cash", m.Cash, 18.32)
	assertNear(t, "debt", m.Debt, 37.59)
}

func TestParseOzonGroupAndSkipClientCash(t *testing.T) {
	const text = `
(млн руб.)
Итого выручка
309 352
215 834
43%
997 989
613 324
63%
Прибыль/(убыток) за период
3 659
(17 565)
н/п
(938)
(59 442)
(98%)
Чистый поток денежных
средств от операционной
деятельности
157 63
2
167 642
(6%)
503 6
29
286 283
76%
` + "\f" + `
(млн руб.)
Денежные средства и их эквиваленты
650 730
349 198
Итого капитал
(148 406)
(132 500)
Денежные средства и их эквиваленты, принадлежащие
кредитным организациям
`
	m, ok := Parse(text)
	if !ok {
		t.Fatal("expected figures")
	}
	assertNear(t, "revenue", m.Revenue, 997.99)
	assertNear(t, "net_profit", m.NetProfit, -0.94)
	assertNear(t, "ocf", m.OperatingCashFlow, 503.63)
	assertNear(t, "equity", m.Equity, -148.41)
	if m.Cash != nil {
		t.Errorf("client cash must not be stored, got %v", *m.Cash)
	}
}

func TestParseBankSkipsDeposits(t *testing.T) {
	const text = `
Млрд руб.
Чистая прибыль
72,1
38,7
86%
40,1
80%
192,4
122,2
57%
` + "\f" + `
Млрд руб.
Денежные средства и
их эквиваленты
1 235
1 427
13%
1 190
Капитал
805
521
54%
747
8%
Средства клиентов
4 412
4 010
10%
4 244
`
	m, ok := Parse(text)
	if !ok {
		t.Fatal("expected figures")
	}
	assertNear(t, "net_profit", m.NetProfit, 192.40)
	assertNear(t, "equity", m.Equity, 805)
	if m.Cash != nil {
		t.Errorf("bank cash must not be stored, got %v", *m.Cash)
	}
	if m.Debt != nil {
		t.Errorf("bank debt must not be stored, got %v", *m.Debt)
	}
	if m.Revenue != nil {
		t.Errorf("bank revenue is not a single line, got %v", *m.Revenue)
	}
}

func TestParseDividendsPaid(t *testing.T) {
	const text = `
МЛН РУБ.
Выручка
4 642 034
3 908 047
Чистая прибыль
83 139
104 064
Дивиденды выплаченные акционерам
(48 200)
(32 100)
Дивиденды полученные
1 500
900
Дивиденды на акцию
412
310
`
	m, ok := Parse(text)
	if !ok {
		t.Fatal("expected figures")
	}
	assertNear(t, "dividends", m.Dividends, 48.20)
}

func TestParseRejectsEmpty(t *testing.T) {
	if _, ok := Parse("нет таблицы"); ok {
		t.Fatal("prose must not become an entry")
	}
}

func TestParseExtractedPress(t *testing.T) {
	dir := os.Getenv("IFRS_TEXT_DIR")
	if dir == "" {
		t.Skip("IFRS_TEXT_DIR is not set")
	}
	checks := []struct {
		file string
		want map[string]float64
		none []string
	}{
		{"go_x5.txt", map[string]float64{"revenue": 4642.03, "net_profit": 83.14, "ebitda": 440.68, "operating_profit": 247.64, "ocf": 300.57, "debt": 406.96}, nil},
		{"go_belu.txt", map[string]float64{"revenue": 149.27, "net_profit": 5.17, "ebitda": 21.20, "operating_profit": 14.14, "equity": 22.36, "cash": 18.32, "debt": 37.59}, nil},
		{"go_ozon.txt", map[string]float64{
			"revenue": 997.99, "net_profit": -0.94, "equity": -148.41,
			"operating_profit": 71.37, "ocf": 503.63,
		}, []string{"cash", "ebitda"}},
		{"go_t.txt", map[string]float64{"net_profit": 192.40, "equity": 805}, []string{"cash", "revenue"}},
	}
	for _, tc := range checks {
		t.Run(tc.file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			m, ok := Parse(string(b))
			if !ok {
				t.Fatal("no figures")
			}
			for name, want := range tc.want {
				assertNear(t, name, field(m, name), want)
			}
			for _, name := range tc.none {
				if field(m, name) != nil {
					t.Errorf("%s = %v, want empty", name, *field(m, name))
				}
			}
		})
	}
}

func field(m models.ManualFinancials, name string) *float64 {
	switch name {
	case "revenue":
		return m.Revenue
	case "net_profit":
		return m.NetProfit
	case "ebitda":
		return m.EBITDA
	case "operating_profit":
		return m.OperatingProfit
	case "ocf":
		return m.OperatingCashFlow
	case "equity":
		return m.Equity
	case "cash":
		return m.Cash
	case "debt":
		return m.Debt
	case "dividends":
		return m.Dividends
	default:
		return nil
	}
}

func assertNear(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is empty, want %v", name, want)
		return
	}
	if math.Abs(*got-want) > 0.02 {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}
