package handlers

import (
	"bytes"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestImportManual(t *testing.T) {
	repo := &fakeRepo{companies: []string{"X5"}}
	c := NewController(repo, slog.New(slog.DiscardHandler))
	c.now = func() time.Time { return testNow }
	r := chi.NewRouter()
	r.Post("/api/import-manual", c.ImportManual)

	body := "компания;год;выручка;долг;денежные средства\nX5;2024;3500;200;50\nNOPE;2024;1;0;0\n"
	rec := postManualFile(t, r, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(repo.savedManual) != 1 || repo.savedManual[0].Company != "X5" || *repo.savedManual[0].Revenue != 3500 || *repo.savedManual[0].Debt != 200 {
		t.Fatalf("saved = %+v", repo.savedManual)
	}
	if !strings.Contains(rec.Body.String(), `"saved":1`) || !strings.Contains(rec.Body.String(), "company not found") {
		t.Errorf("body = %s", rec.Body)
	}

	rec = postManualFile(t, r, "компания;год;выручка\n")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("header only: %d %s", rec.Code, rec.Body)
	}
	rec = postManualFile(t, r, "выручка\n1\n")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad header: %d", rec.Code)
	}
}

func postManualFile(t *testing.T, h http.Handler, csv string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "ifrs.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(csv))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import-manual", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
