package moex

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Reference data used to build the ticker registry (POST /api/registry) and to
// resolve a ticker's INN on the fly: which shares trade on the board, who
// issued each one, and which sector index lists it.

// ErrNoEmitter means ISS knows the security but reports no issuer INN for it
// (foreign issuers and depositary receipts have none).
var ErrNoEmitter = errors.New("ISS reports no issuer INN")

// Security types in the board listing's SECTYPE column.
const (
	SecTypeCommon    = "1"
	SecTypePreferred = "2"
)

// BoardSecurity is one security listed on a board (TQBR by default).
type BoardSecurity struct {
	SecID     string
	ShortName string
	ISIN      string
	SecType   string // SecTypeCommon, SecTypePreferred, or another ISS code
}

// IsShare reports whether the security is a Russian common or preferred share
// (not a depositary receipt, fund unit or other instrument).
func (s BoardSecurity) IsShare() bool {
	return s.SecType == SecTypeCommon || s.SecType == SecTypePreferred
}

// Emitter is the issuer of a security as ISS describes it.
type Emitter struct {
	SecID string
	ISIN  string
	INN   string
	Title string
}

// BoardSecurities lists every security on the client's board.
func (c *Client) BoardSecurities(ctx context.Context) ([]BoardSecurity, error) {
	u := fmt.Sprintf("%s/iss/engines/stock/markets/shares/boards/%s/securities.json?iss.meta=off&iss.only=securities&securities.columns=SECID,SHORTNAME,ISIN,SECTYPE",
		c.BaseURL, url.PathEscape(c.Board))
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("board %s securities: %w", c.Board, err)
	}
	return ParseBoardSecurities(body)
}

// ParseBoardSecurities reads the "securities" block of a board listing.
func ParseBoardSecurities(body []byte) ([]BoardSecurity, error) {
	b, err := decodeBlock(body, "securities")
	if err != nil {
		return nil, err
	}
	secCol, nameCol, isinCol, typeCol := b.col("SECID"), b.col("SHORTNAME"), b.col("ISIN"), b.col("SECTYPE")
	if secCol < 0 || isinCol < 0 || typeCol < 0 {
		return nil, fmt.Errorf("securities block missing SECID/ISIN/SECTYPE columns")
	}
	out := make([]BoardSecurity, 0, len(b.Data))
	for _, row := range b.Data {
		s := BoardSecurity{
			SecID:   cell(row, secCol),
			ISIN:    cell(row, isinCol),
			SecType: cell(row, typeCol),
		}
		if nameCol >= 0 {
			s.ShortName = cell(row, nameCol)
		}
		if s.SecID != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// EmitterOf resolves secid's issuer: the ISIN from the security description,
// then the issuer from a search by that ISIN (ISINs are unique, while a
// search by secid also matches other instruments that mention it).
func (c *Client) EmitterOf(ctx context.Context, secid string) (Emitter, error) {
	secid = strings.ToUpper(secid)
	desc, err := c.get(ctx, fmt.Sprintf("%s/iss/securities/%s.json?iss.meta=off&iss.only=description", c.BaseURL, url.PathEscape(secid)))
	if err != nil {
		return Emitter{}, fmt.Errorf("describe %s: %w", secid, err)
	}
	isin, err := ParseDescriptionField(desc, "ISIN")
	if err != nil {
		return Emitter{}, fmt.Errorf("describe %s: %w", secid, err)
	}
	return c.EmitterByISIN(ctx, secid, isin)
}

// EmitterByISIN resolves the issuer of secid when its ISIN is already known
// (a board listing carries it), saving the description request.
func (c *Client) EmitterByISIN(ctx context.Context, secid, isin string) (Emitter, error) {
	u := fmt.Sprintf("%s/iss/securities.json?iss.meta=off&iss.only=securities&q=%s&securities.columns=secid,isin,emitent_inn,emitent_title",
		c.BaseURL, url.QueryEscape(isin))
	body, err := c.get(ctx, u)
	if err != nil {
		return Emitter{}, fmt.Errorf("search %s: %w", isin, err)
	}
	return ParseEmitter(body, secid, isin)
}

// ParseEmitter picks the row for secid (or, failing that, isin) from a
// /iss/securities search and returns its issuer.
func ParseEmitter(body []byte, secid, isin string) (Emitter, error) {
	b, err := decodeBlock(body, "securities")
	if err != nil {
		return Emitter{}, err
	}
	secCol, isinCol, innCol, titleCol := b.col("secid"), b.col("isin"), b.col("emitent_inn"), b.col("emitent_title")
	if secCol < 0 || innCol < 0 {
		return Emitter{}, fmt.Errorf("securities block missing secid/emitent_inn columns")
	}
	pick := -1
	for i, row := range b.Data {
		if strings.EqualFold(cell(row, secCol), secid) {
			pick = i
			break
		}
		if pick < 0 && isin != "" && isinCol >= 0 && cell(row, isinCol) == isin {
			pick = i
		}
	}
	if pick < 0 {
		return Emitter{}, fmt.Errorf("security %s (%s) not found in ISS search", secid, isin)
	}
	row := b.Data[pick]
	e := Emitter{SecID: strings.ToUpper(secid), ISIN: isin, INN: cell(row, innCol)}
	if titleCol >= 0 {
		e.Title = cell(row, titleCol)
	}
	if isinCol >= 0 && e.ISIN == "" {
		e.ISIN = cell(row, isinCol)
	}
	if e.INN == "" {
		return e, fmt.Errorf("%s: %w", secid, ErrNoEmitter)
	}
	return e, nil
}

// ParseDescriptionField returns one field of a /iss/securities/{SECID}.json
// description (a vertical name/value table).
func ParseDescriptionField(body []byte, name string) (string, error) {
	b, err := decodeBlock(body, "description")
	if err != nil {
		return "", err
	}
	nameCol, valCol := b.col("name"), b.col("value")
	if nameCol < 0 || valCol < 0 {
		return "", fmt.Errorf("description block missing name/value columns")
	}
	for _, row := range b.Data {
		if cell(row, nameCol) == name {
			if v := cell(row, valCol); v != "" {
				return v, nil
			}
			break
		}
	}
	return "", fmt.Errorf("%s not found in description", name)
}

// maxIndexPages bounds IndexConstituents' paging (a sector index has a few
// dozen members; the cap only guards against a cursor that never ends).
const maxIndexPages = 20

// IndexConstituents lists the secids of an index's current members (e.g.
// "MOEXEU", the electric utilities sector index). ISS pages the list; the
// "analytics.cursor" block gives the total and page size.
func (c *Client) IndexConstituents(ctx context.Context, index string) ([]string, error) {
	var out []string
	start := 0
	for page := 0; page < maxIndexPages; page++ {
		u := fmt.Sprintf("%s/iss/statistics/engines/stock/markets/index/analytics/%s.json?iss.meta=off&limit=100&start=%d",
			c.BaseURL, url.PathEscape(index), start)
		body, err := c.get(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", index, err)
		}
		secids, total, pageSize, err := ParseIndexPage(body)
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", index, err)
		}
		out = append(out, secids...)
		start += pageSize
		if pageSize <= 0 || len(secids) == 0 || start >= total {
			return out, nil
		}
	}
	return nil, fmt.Errorf("index %s: more than %d pages", index, maxIndexPages)
}

// ParseIndexPage reads one page of an index composition: the member secids
// and the cursor's TOTAL and PAGESIZE (both 0 when the cursor is absent, which
// ends the paging).
func ParseIndexPage(body []byte) (secids []string, total, pageSize int, err error) {
	b, err := decodeBlock(body, "analytics")
	if err != nil {
		return nil, 0, 0, err
	}
	col := b.col("secids")
	if col < 0 {
		col = b.col("ticker")
	}
	if col < 0 {
		return nil, 0, 0, fmt.Errorf("analytics block missing secids/ticker column")
	}
	for _, row := range b.Data {
		if s := cell(row, col); s != "" {
			secids = append(secids, s)
		}
	}
	if cur, err := decodeBlock(body, "analytics.cursor"); err == nil && len(cur.Data) > 0 {
		row := cur.Data[0]
		if i := cur.col("TOTAL"); i >= 0 && i < len(row) {
			if v, ok := asFloat(row[i]); ok {
				total = int(v)
			}
		}
		if i := cur.col("PAGESIZE"); i >= 0 && i < len(row) {
			if v, ok := asFloat(row[i]); ok {
				pageSize = int(v)
			}
		}
	}
	return secids, total, pageSize, nil
}

// cell returns row[i] as a trimmed string: "" when out of range or null, a
// JSON number in its shortest form (a code such as SECTYPE may arrive as one).
func cell(row []interface{}, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	if n, ok := row[i].(float64); ok {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return strings.TrimSpace(asString(row[i]))
}
