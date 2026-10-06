package moex

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Issuer lookup used when a requested ticker has no INN in the bundled registry.

// ErrNoEmitter means ISS knows the security but reports no issuer INN for it
// (foreign issuers and depositary receipts have none).
var ErrNoEmitter = errors.New("ISS reports no issuer INN")

// Emitter is the issuer of a security as ISS describes it.
type Emitter struct {
	SecID string
	ISIN  string
	INN   string
	Title string
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

// EmitterByISIN resolves the issuer of secid when its ISIN is already known,
// saving the description request.
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
