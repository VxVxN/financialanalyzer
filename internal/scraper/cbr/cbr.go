// Package cbr builds quarterly bank financials — net profit, an operating
// revenue proxy and balance-sheet equity — from the Central Bank of Russia's
// published form 102 ("Отчёт о финансовых результатах") and form 101
// ("Оборотная ведомость по счетам бухгалтерского учёта") archives: the free,
// machine-readable source covering the credit institutions that are absent
// from ГИР БО.
//
// Banks file their statements with the Central Bank, not the Federal Tax
// Service, so they never appear in ГИР БО. The CBR publishes the whole banking
// sector's report for a period as a RAR archive of DBF tables:
//
//	https://www.cbr.ru/vfs/credit/forms/102-YYYYMMDD.rar   (>= 2009; .zip before)
//	https://www.cbr.ru/vfs/credit/forms/101-YYYYMMDD.rar
//
// The date in the filename is the first day after the reported period. Form 102
// figures are year-to-date cumulative:
//
//	102-YYYY0401 -> Q1 of YYYY (3 months)
//	102-YYYY0701 -> H1 of YYYY (6 months)
//	102-YYYY1001 -> 9 months of YYYY
//	102-YYYY0101 -> full prior year (12 months)
//
// FetchPeriod returns one form 102 archive's cumulative figures per bank; the
// caller differences consecutive periods to recover true single-quarter figures
// (see the fetcher). Net profit after tax is CODE 61101 (61102 is the loss
// counterpart). Revenue has no single form 102 line; it is approximated as net
// interest income (Part 1 sections 1-4 and 6 minus Part 3 sections 1-6, i.e.
// interest income and expense with their effective-rate commission and
// adjustment sections, provisions excluded) plus gross fee and commission
// income (Part 2 section 7, CODE 27000; fee expense has no separate section
// total, so it is not netted). The code layout is the one in force since the
// 2016 chart of accounts (section names checked identical in the 2017, 2018,
// 2019 and 2024-2026 archives' SPRAV1.dbf dictionaries); older archives carry
// no CODE 61101, so they yield no banks at all rather than misread figures.
//
// FetchEquity returns balance-sheet equity from a form 101 archive (a
// point-in-time balance, not cumulative): the outgoing balances of the capital
// accounts (chapter А, accounts 1xx: charter and additional capital, reserve
// fund, retained earnings, less treasury shares, uncovered loss and
// dividends) plus the not-yet-closed financial result (accounts 706-708),
// passive balances counted positive and active ones negative. Archives up to
// 2021 list second-order (5-digit) accounts, later ones first-order (3-digit)
// aggregates; both are handled. This is RSBU equity of the bank itself, not the
// IFRS equity of its group.
//
// All DBF values are in thousands of RUB, converted here to billions to match
// the rest of the codebase. Banks are keyed by their CBR registration number
// (REGN), not ticker, so the caller supplies the REGN. EBITDA and debt have no
// meaningful bank equivalent and are left empty. Market cap and the derived
// P/E come from MOEX, computed by the caller.
package cbr

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nwaples/rardecode/v2"

	"github.com/VxVxN/financialanalyzer/internal/scraper/httpx"
)

const (
	BaseURL = "https://www.cbr.ru"

	// Form 102 line codes for the financial result after tax.
	netProfitCode = "61101" // Прибыль после налогообложения
	netLossCode   = "61102" // Убыток после налогообложения

	// Form 102 code for gross fee and commission income (Part 2 section 7,
	// "Комиссионные и аналогичные доходы").
	feeIncomeCode = "27000"

	// Form 101 plan "А" (balance-sheet accounts) in cp866.
	balancePlan = "\x80"

	// DBF values are thousands of RUB; the codebase works in billions.
	thousandToBillion = 1e6

	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0 Safari/537.36"
	defaultDelay   = 1200 * time.Millisecond
	defaultTimeout = 60 * time.Second
)

// ErrNotPublished is returned by FetchPeriod and FetchEquity when the CBR has no
// archive for the requested date (e.g. a quarter that has not been published
// yet). Callers match
// it with errors.Is and skip that period rather than failing the whole run.
var ErrNotPublished = errors.New("CBR archive not published")

type Client struct {
	HTTP      *http.Client
	UserAgent string
	BaseURL   string
	Delay     time.Duration
	Retry     httpx.Policy // retries for transient failures (network, 429, 5xx)
	lastReq   time.Time
}

func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: defaultTimeout},
		UserAgent: defaultUserAgent,
		BaseURL:   BaseURL,
		Delay:     defaultDelay,
		Retry:     httpx.DefaultPolicy,
	}
}

// Form102 is one bank's year-to-date cumulative figures from a form 102
// archive, in billions of RUB.
type Form102 struct {
	NetProfit float64 // after tax; negative for a loss
	Revenue   float64 // net interest income + gross fee and commission income
}

// FetchPeriod downloads and parses the form 102 archive for an archive date
// ("YYYYMMDD", the first day after the reported period) and returns each bank's
// cumulative figures, keyed by CBR registration number. Only banks reporting a
// financial result after tax are included. Returns ErrNotPublished when the
// archive does not exist.
func (c *Client) FetchPeriod(ctx context.Context, archiveDate string) (map[int]Form102, error) {
	raw, err := c.download(ctx, "102", archiveDate)
	if err != nil {
		return nil, err
	}
	dbf, err := extractDBF(raw, "_P1.DBF")
	if err != nil {
		return nil, fmt.Errorf("archive 102-%s: %w", archiveDate, err)
	}
	figures, err := parseForm102(dbf)
	if err != nil {
		return nil, fmt.Errorf("archive 102-%s: %w", archiveDate, err)
	}
	return figures, nil
}

// FetchEquity downloads and parses the form 101 archive for an archive date
// (the first day after the balance date, e.g. "20240101" for 31 Dec 2023) and
// returns each bank's balance-sheet equity, keyed by CBR registration number,
// in billions of RUB. Returns ErrNotPublished when the archive does not exist.
func (c *Client) FetchEquity(ctx context.Context, archiveDate string) (map[int]float64, error) {
	raw, err := c.download(ctx, "101", archiveDate)
	if err != nil {
		return nil, err
	}
	dbf, err := extractDBF(raw, "B1.DBF")
	if err != nil {
		return nil, fmt.Errorf("archive 101-%s: %w", archiveDate, err)
	}
	equity, err := parseForm101(dbf)
	if err != nil {
		return nil, fmt.Errorf("archive 101-%s: %w", archiveDate, err)
	}
	return equity, nil
}

// download fetches the RAR archive bytes for a form and archive date,
// rate-limited. form is the CBR form number, e.g. "102" or "101".
func (c *Client) download(ctx context.Context, form, archiveDate string) ([]byte, error) {
	if d := time.Until(c.lastReq.Add(c.Delay)); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c.lastReq = time.Now()

	url := fmt.Sprintf("%s/vfs/credit/forms/%s-%s.rar", c.BaseURL, form, archiveDate)
	body, err := httpx.Get(ctx, c.HTTP, url, http.Header{
		"User-Agent": {c.UserAgent},
		"Accept":     {"application/rar, application/octet-stream, */*"},
	}, c.Retry)
	c.lastReq = time.Now() // pace from the last attempt, retries included
	var se *httpx.StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil, ErrNotPublished
	}
	return body, err
}

// ---- Archive & DBF parsing --------------------------------------------------

// extractDBF pulls the data table out of a CBR RAR archive — the DBF entry whose
// (upper-cased) name ends in suffix. A form 102 archive's per-bank values live
// in "*_P1.dbf" (the bank-name directory "*NP1.dbf" and the code dictionary
// "SPRAV1.dbf" are excluded); a form 101 archive's are in "*B1.dbf" (the
// bank-name directory "*N1.dbf" and the account dictionary "NAMES.dbf" are
// excluded).
func extractDBF(archive []byte, suffix string) ([]byte, error) {
	r, err := rardecode.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open rar: %w", err)
	}
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read rar entry: %w", err)
		}
		if !strings.HasSuffix(strings.ToUpper(h.Name), suffix) {
			continue
		}
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", h.Name, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("no *%s data table in archive", suffix)
}

// Form 102 section totals making up net interest income: interest income with
// the commission, adjustment and premium sections that the effective-rate
// method books into it, minus the matching interest expense sections.
// Provisions (Part 1 sections 5 and 7, Part 3 sections 7-8) are excluded.
var (
	interestIncomeCodes = map[string]bool{
		"11000": true, // Part 1 s.1: процентные доходы
		"12000": true, // s.2: комиссионные доходы (в составе процентных)
		"13000": true, // s.3: корректировки, увеличивающие процентные доходы
		"14000": true, // s.4: корректировки, уменьшающие процентные расходы
		"16000": true, // s.6: премии, уменьшающие процентные расходы
	}
	interestExpenseCodes = map[string]bool{
		"31000": true, // Part 3 s.1: процентные расходы
		"32000": true, // s.2: комиссионные расходы, увеличивающие процентные расходы
		"33000": true, // s.3: комиссионные расходы, уменьшающие процентные доходы
		"34000": true, // s.4: премии, уменьшающие процентные доходы
		"35000": true, // s.5: корректировки, уменьшающие процентные доходы
		"36000": true, // s.6: корректировки, увеличивающие процентные расходы
	}
)

// parseForm102 reads the form 102 DBF and returns each bank's net profit after
// tax (CODE 61101 minus the loss CODE 61102; only one is non-zero for a given
// bank) and revenue (net interest income + fee income), in billions of RUB.
// Banks without a financial-result row are omitted.
func parseForm102(dbf []byte) (map[int]Form102, error) {
	t, err := parseDBF(dbf)
	if err != nil {
		return nil, err
	}
	regnCol := t.col("REGN")
	codeCol := t.col("CODE")
	valCol := t.col("SIM_ITOGO")
	if regnCol < 0 || codeCol < 0 || valCol < 0 {
		return nil, fmt.Errorf("form 102 dbf missing REGN/CODE/SIM_ITOGO columns")
	}

	figures := make(map[int]*Form102)
	hasResult := make(map[int]bool)
	for _, row := range t.records {
		regn, err := strconv.Atoi(row[regnCol])
		if err != nil {
			continue
		}
		f := figures[regn]
		if f == nil {
			f = &Form102{}
			figures[regn] = f
		}
		val := parseThousands(row[valCol])
		switch code := row[codeCol]; {
		case code == netProfitCode:
			f.NetProfit += val
			hasResult[regn] = true
		case code == netLossCode:
			f.NetProfit -= val
			hasResult[regn] = true
		case code == feeIncomeCode, interestIncomeCodes[code]:
			f.Revenue += val
		case interestExpenseCodes[code]:
			f.Revenue -= val
		}
	}

	out := make(map[int]Form102, len(hasResult))
	for regn := range hasResult {
		f := figures[regn]
		out[regn] = Form102{
			NetProfit: f.NetProfit / thousandToBillion,
			Revenue:   f.Revenue / thousandToBillion,
		}
	}
	return out, nil
}

// parseForm101 reads the form 101 DBF and returns balance-sheet equity per bank
// REGN, in billions of RUB: the outgoing balance (IITG) of every chapter А
// capital account (1xx) and financial-result account (706-708), passive (A_P
// "2") counted positive and active (A_P "1") negative. A first-order (3-digit)
// row, where present, is used in preference to the second-order (5-digit) rows
// under it, so a table listing both is not counted twice.
func parseForm101(dbf []byte) (map[int]float64, error) {
	t, err := parseDBF(dbf)
	if err != nil {
		return nil, err
	}
	regnCol := t.col("REGN")
	planCol := t.col("PLAN")
	acctCol := t.col("NUM_SC")
	sideCol := t.col("A_P")
	valCol := t.col("IITG")
	if regnCol < 0 || planCol < 0 || acctCol < 0 || sideCol < 0 || valCol < 0 {
		return nil, fmt.Errorf("form 101 dbf missing REGN/PLAN/NUM_SC/A_P/IITG columns")
	}

	type key struct {
		regn  int
		first string // first-order account
	}
	firstOrder := make(map[key]float64)
	secondOrder := make(map[key]float64)
	for _, row := range t.records {
		if row[planCol] != balancePlan {
			continue
		}
		acct, _, _ := strings.Cut(row[acctCol], "\x00") // older archives leave junk after a NUL
		if !equityAccount(acct) {
			continue
		}
		var sign float64
		switch row[sideCol] {
		case "1": // active
			sign = -1
		case "2": // passive
			sign = 1
		default:
			continue
		}
		regn, err := strconv.Atoi(row[regnCol])
		if err != nil {
			continue
		}
		k := key{regn, acct[:3]}
		v := sign * parseThousands(row[valCol])
		if len(acct) == 3 {
			firstOrder[k] += v
		} else {
			secondOrder[k] += v
		}
	}

	out := make(map[int]float64)
	for k, v := range secondOrder {
		if _, ok := firstOrder[k]; !ok {
			out[k.regn] += v / thousandToBillion
		}
	}
	for k, v := range firstOrder {
		out[k.regn] += v / thousandToBillion
	}
	return out, nil
}

// equityAccount reports whether a form 101 account number (3-digit first-order
// or 5-digit second-order) belongs to equity: the capital accounts 1xx or the
// financial-result accounts 706-708.
func equityAccount(acct string) bool {
	if len(acct) != 3 && len(acct) != 5 {
		return false
	}
	for _, r := range acct {
		if r < '0' || r > '9' {
			return false
		}
	}
	switch first := acct[:3]; {
	case first[0] == '1':
		return true
	case first == "706", first == "707", first == "708":
		return true
	}
	return false
}

// parseThousands parses a DBF numeric field (ASCII, may be blank) to a float.
func parseThousands(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// dbfTable is a minimally-parsed dBASE III table: field names plus the
// whitespace-trimmed string cells of every non-deleted record. Only ASCII
// fields (REGN/CODE/SIM_ITOGO) are read, so the cp866 text encoding of the
// Cyrillic columns is irrelevant here.
type dbfTable struct {
	fields  []string
	records [][]string
}

func (t *dbfTable) col(name string) int {
	for i, f := range t.fields {
		if f == name {
			return i
		}
	}
	return -1
}

// parseDBF parses the dBASE III header and fixed-width records.
func parseDBF(b []byte) (*dbfTable, error) {
	const headerStart = 32
	if len(b) < headerStart+1 {
		return nil, fmt.Errorf("dbf too short: %d bytes", len(b))
	}
	numRecords := int(binary.LittleEndian.Uint32(b[4:8]))
	headerSize := int(binary.LittleEndian.Uint16(b[8:10]))
	recordSize := int(binary.LittleEndian.Uint16(b[10:12]))
	if headerSize <= 0 || recordSize <= 0 || headerSize > len(b) {
		return nil, fmt.Errorf("dbf bad header: size=%d record=%d", headerSize, recordSize)
	}

	var (
		fields  []string
		lengths []int
	)
	for off := headerStart; off < len(b) && b[off] != 0x0D; off += 32 {
		if off+32 > len(b) {
			return nil, fmt.Errorf("dbf truncated field descriptor at %d", off)
		}
		// The name is NUL-terminated; older CBR archives (2021 and earlier)
		// leave junk after the terminator instead of zero-filling it.
		name := b[off : off+11]
		if i := bytes.IndexByte(name, 0); i >= 0 {
			name = name[:i]
		}
		fields = append(fields, string(name))
		lengths = append(lengths, int(b[off+16]))
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("dbf has no fields")
	}

	records := make([][]string, 0, numRecords)
	for i := 0; i < numRecords; i++ {
		start := headerSize + i*recordSize
		if start+recordSize > len(b) {
			break // truncated file; return what parsed
		}
		rec := b[start : start+recordSize]
		if rec[0] == 0x2A { // 0x2A '*' marks a deleted record
			continue
		}
		row := make([]string, len(fields))
		pos := 1 // skip the deletion flag
		for j, l := range lengths {
			if pos+l > len(rec) {
				break
			}
			row[j] = strings.TrimSpace(string(bytes.TrimRight(rec[pos:pos+l], "\x00")))
			pos += l
		}
		records = append(records, row)
	}
	return &dbfTable{fields: fields, records: records}, nil
}

// ---- Period <-> archive-date mapping ----------------------------------------

// ArchiveDate returns the form 102 archive date ("YYYYMMDD") whose cumulative
// figures cover the given stored (year, quarter): the first day after the
// period. Q4 maps to 1 January of the following year (the full-year report).
func ArchiveDate(year int, quarter string) (string, error) {
	switch quarter {
	case "Q1":
		return fmt.Sprintf("%04d0401", year), nil
	case "Q2":
		return fmt.Sprintf("%04d0701", year), nil
	case "Q3":
		return fmt.Sprintf("%04d1001", year), nil
	case "Q4":
		return fmt.Sprintf("%04d0101", year+1), nil
	default:
		return "", fmt.Errorf("invalid quarter %q", quarter)
	}
}
