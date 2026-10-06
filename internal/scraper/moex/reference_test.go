package moex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// The search fixtures follow the /iss/securities column layout seen live
// (secid, isin, emitent_inn, emitent_title).

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
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
