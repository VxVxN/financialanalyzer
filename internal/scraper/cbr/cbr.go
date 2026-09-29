// Package cbr builds quarterly net-profit figures for Russian banks from the
// Central Bank of Russia's published form 102 ("Отчёт о финансовых
// результатах") archives — the free, machine-readable source covering the
// credit institutions that are absent from ГИР БО.
//
// Banks file form 102 with the Central Bank, not the Federal Tax Service, so
// they never appear in ГИР БО. The CBR publishes the whole banking sector's
// form 102 for a period as a RAR archive of DBF tables:
//
//	https://www.cbr.ru/vfs/credit/forms/102-YYYYMMDD.rar   (>= 2009; .zip before)
//
// The date in the filename is the first day after the reported period, and the
// figures inside are year-to-date cumulative:
//
//	102-YYYY0401 -> Q1 of YYYY (3 months)
//	102-YYYY0701 -> H1 of YYYY (6 months)
//	102-YYYY1001 -> 9 months of YYYY
//	102-YYYY0101 -> full prior year (12 months)
//
// FetchPeriod returns one archive's cumulative net profit per bank; the caller
// differences consecutive periods to recover true single-quarter figures (see
// cmd/fetch). Net profit after tax is form 102 CODE 61101 (61102 is the loss
// counterpart). DBF values are in thousands of RUB, converted here to billions
// to match the rest of the codebase.
//
// Equity for ROE comes from form 123 ("Расчёт собственных средств (капитала)"),
// fetched by FetchCapital from the analogous archive
// (https://www.cbr.ru/vfs/credit/forms/123-YYYYMMDD.rar): total regulatory
// capital (Basel III own funds) is line "000". This is regulatory capital, not
// balance-sheet equity, so the ROE it backs is approximate. The caller divides
// full-year net profit by year-end capital.
//
// Banks are keyed by their CBR registration number (REGN), not ticker, so the
// caller supplies the REGN. A bank's revenue / EBITDA / debt have no clean form
// 102 line (smart-lab leaves them empty for banks too) and are left empty.
// Market cap and the derived P/E come from MOEX, computed by the caller.
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

	// Form 123 line code for total regulatory capital (Basel III own funds),
	// used as the equity base for ROE.
	capitalCode = "000" // Собственные средства (капитал), итого

	// DBF values are thousands of RUB; the codebase works in billions.
	thousandToBillion = 1e6

	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0 Safari/537.36"
	defaultDelay   = 1200 * time.Millisecond
	defaultTimeout = 60 * time.Second
)

// ErrNotPublished is returned by FetchPeriod when the CBR has no archive for the
// requested date (e.g. a quarter that has not been published yet). Callers match
// it with errors.Is and skip that period rather than failing the whole run.
var ErrNotPublished = errors.New("form 102 archive not published")

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

// FetchPeriod downloads and parses the form 102 archive for an archive date
// ("YYYYMMDD", the first day after the reported period) and returns each bank's
// cumulative net profit after tax, keyed by CBR registration number, in
// billions of RUB. Returns ErrNotPublished when the archive does not exist.
func (c *Client) FetchPeriod(ctx context.Context, archiveDate string) (map[int]float64, error) {
	raw, err := c.download(ctx, "102", archiveDate)
	if err != nil {
		return nil, err
	}
	dbf, err := extractDBF(raw, "_P1.DBF")
	if err != nil {
		return nil, fmt.Errorf("archive 102-%s: %w", archiveDate, err)
	}
	profits, err := parseForm102(dbf)
	if err != nil {
		return nil, fmt.Errorf("archive 102-%s: %w", archiveDate, err)
	}
	return profits, nil
}

// FetchCapital downloads and parses the form 123 ("Расчёт собственных средств
// (капитала)") archive for an archive date and returns each bank's total
// regulatory capital (Basel III own funds), keyed by CBR registration number,
// in billions of RUB. Form 123 is a point-in-time balance value (not
// cumulative), so the caller passes the period-end date. Returns ErrNotPublished
// when the archive does not exist.
func (c *Client) FetchCapital(ctx context.Context, archiveDate string) (map[int]float64, error) {
	raw, err := c.download(ctx, "123", archiveDate)
	if err != nil {
		return nil, err
	}
	dbf, err := extractDBF(raw, "_123D.DBF")
	if err != nil {
		return nil, fmt.Errorf("archive 123-%s: %w", archiveDate, err)
	}
	capital, err := parseForm123(dbf)
	if err != nil {
		return nil, fmt.Errorf("archive 123-%s: %w", archiveDate, err)
	}
	return capital, nil
}

// download fetches the RAR archive bytes for a form and archive date,
// rate-limited. form is the CBR form number, e.g. "102" or "123".
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
// "SPRAV1.dbf" are excluded); a form 123 archive's are in "*_123D.dbf".
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

// parseForm102 reads the form 102 DBF and returns net profit after tax per bank
// REGN, in billions of RUB. Net profit is CODE 61101 minus the loss CODE 61102
// (only one is non-zero for a given bank).
func parseForm102(dbf []byte) (map[int]float64, error) {
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

	out := make(map[int]float64)
	for _, row := range t.records {
		code := row[codeCol]
		if code != netProfitCode && code != netLossCode {
			continue
		}
		regn, err := strconv.Atoi(row[regnCol])
		if err != nil {
			continue
		}
		val := parseThousands(row[valCol])
		if code == netProfitCode {
			out[regn] += val
		} else {
			out[regn] -= val
		}
	}
	for regn := range out {
		out[regn] /= thousandToBillion
	}
	return out, nil
}

// parseForm123 reads the form 123 DBF and returns total regulatory capital per
// bank REGN, in billions of RUB. The table is keyed REGN + line code (column
// C1); the total own funds are line "000" with the value in column C3.
func parseForm123(dbf []byte) (map[int]float64, error) {
	t, err := parseDBF(dbf)
	if err != nil {
		return nil, err
	}
	regnCol := t.col("REGN")
	codeCol := t.col("C1")
	valCol := t.col("C3")
	if regnCol < 0 || codeCol < 0 || valCol < 0 {
		return nil, fmt.Errorf("form 123 dbf missing REGN/C1/C3 columns")
	}

	out := make(map[int]float64)
	for _, row := range t.records {
		if row[codeCol] != capitalCode {
			continue
		}
		regn, err := strconv.Atoi(row[regnCol])
		if err != nil {
			continue
		}
		out[regn] = parseThousands(row[valCol]) / thousandToBillion
	}
	return out, nil
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
		name := string(bytes.TrimRight(b[off:off+11], "\x00"))
		fields = append(fields, name)
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
