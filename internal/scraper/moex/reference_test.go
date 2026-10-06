package moex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

// The search fixtures follow the /iss/securities column layout seen live
// (secid, isin, emitent_inn, emitent_title); the board listing and index
// composition fixtures are hand-made in the documented ISS shapes and cut
// down to the columns the parsers read.

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func TestParseBoardSecurities(t *testing.T) {
	got, err := ParseBoardSecurities(readFixture(t, "tqbr_securities.json"))
	if err != nil {
		t.Fatalf("ParseBoardSecurities: %v", err)
	}
	want := []BoardSecurity{
		{SecID: "MGNT", ShortName: "Магнит", ISIN: "RU000A0JKQU8", SecType: "1"},
		{SecID: "SBER", ShortName: "Сбербанк", ISIN: "RU0009029540", SecType: "1"},
		{SecID: "SBERP", ShortName: "Сбербанк-п", ISIN: "RU0009029557", SecType: "2"},
		{SecID: "FIXR", ShortName: "ФИКС ПРАЙС", ISIN: "RU000A108X38", SecType: "D"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	shares := 0
	for _, s := range got {
		if s.IsShare() {
			shares++
		}
	}
	if shares != 3 {
		t.Errorf("IsShare count = %d, want 3 (the receipt is not a share)", shares)
	}
}

func TestParseBoardSecuritiesNumericSecType(t *testing.T) {
	body := []byte(`{"securities":{"columns":["SECID","ISIN","SECTYPE"],"data":[["GAZP","RU0007661625",1]]}}`)
	got, err := ParseBoardSecurities(body)
	if err != nil {
		t.Fatalf("ParseBoardSecurities: %v", err)
	}
	if len(got) != 1 || !got[0].IsShare() {
		t.Errorf("got %+v, want one common share", got)
	}
}

func TestParseBoardSecuritiesMissingColumns(t *testing.T) {
	if _, err := ParseBoardSecurities([]byte(`{"securities":{"columns":["SECID"],"data":[]}}`)); err == nil {
		t.Error("want an error for a listing without ISIN/SECTYPE")
	}
}

func TestParseEmitter(t *testing.T) {
	e, err := ParseEmitter(readFixture(t, "search_mgnt.json"), "mgnt", "RU000A0JKQU8")
	if err != nil {
		t.Fatalf("ParseEmitter: %v", err)
	}
	want := Emitter{SecID: "MGNT", ISIN: "RU000A0JKQU8", INN: "2309085638", Title: `Публичное акционерное общество "Магнит"`}
	if e != want {
		t.Errorf("got %+v, want %+v", e, want)
	}
}

func TestParseEmitterByISIN(t *testing.T) {
	// The row is matched by ISIN when its secid differs (e.g. a renamed one).
	e, err := ParseEmitter(readFixture(t, "search_mgnt.json"), "OLD", "RU000A0JKQU8")
	if err != nil || e.INN != "2309085638" {
		t.Errorf("got %+v, %v; want the MGNT row by ISIN", e, err)
	}
}

func TestParseEmitterNoINN(t *testing.T) {
	_, err := ParseEmitter(readFixture(t, "search_foreign.json"), "FIXR", "RU000A108X38")
	if !errors.Is(err, ErrNoEmitter) {
		t.Errorf("err = %v, want ErrNoEmitter", err)
	}
}

func TestParseEmitterNotFound(t *testing.T) {
	_, err := ParseEmitter(readFixture(t, "search_mgnt.json"), "GAZP", "RU0007661625")
	if err == nil || errors.Is(err, ErrNoEmitter) {
		t.Errorf("err = %v, want a not-found error", err)
	}
}

func TestEmitterOf(t *testing.T) {
	desc := []byte(`{"description":{"columns":["name","title","value","type","sort_order","is_hidden","precision"],"data":[
		["SECID","Код ценной бумаги","MGNT","string",1,0,null],
		["ISIN","ISIN код","RU000A0JKQU8","string",5,0,null]]}}`)
	search := readFixture(t, "search_mgnt.json")
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/iss/securities/MGNT.json":
			_, _ = w.Write(desc)
		case "/iss/securities.json":
			gotQ = r.URL.Query().Get("q")
			_, _ = w.Write(search)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL, c.Delay = srv.URL, 0
	e, err := c.EmitterOf(context.Background(), "mgnt")
	if err != nil {
		t.Fatalf("EmitterOf: %v", err)
	}
	if gotQ != "RU000A0JKQU8" {
		t.Errorf("search q = %q, want the ISIN", gotQ)
	}
	if e.INN != "2309085638" {
		t.Errorf("INN = %q, want 2309085638", e.INN)
	}
}

func TestIndexConstituentsPages(t *testing.T) {
	pages := map[string][]byte{
		"0": readFixture(t, "index_moexeu_p0.json"),
		"2": readFixture(t, "index_moexeu_p1.json"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/iss/statistics/engines/stock/markets/index/analytics/MOEXEU.json" {
			http.NotFound(w, r)
			return
		}
		body, ok := pages[r.URL.Query().Get("start")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewClient()
	c.BaseURL, c.Delay = srv.URL, 0
	got, err := c.IndexConstituents(context.Background(), "MOEXEU")
	if err != nil {
		t.Fatalf("IndexConstituents: %v", err)
	}
	if want := []string{"IRAO", "HYDR", "FEES"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseIndexPageWithoutCursor(t *testing.T) {
	// No cursor: one page, paging stops (total = page size = 0).
	secids, total, size, err := ParseIndexPage([]byte(`{"analytics":{"columns":["ticker"],"data":[["IRAO"]]}}`))
	if err != nil || !reflect.DeepEqual(secids, []string{"IRAO"}) || total != 0 || size != 0 {
		t.Errorf("got %v %d %d %v", secids, total, size, err)
	}
}
