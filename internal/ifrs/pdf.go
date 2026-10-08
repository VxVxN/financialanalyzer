package ifrs

import (
	"bytes"
	"errors"
	"strings"

	"github.com/ledongthuc/pdf"
)

// extractText reads a PDF's text layer. Statement pages that are scans come
// back empty; the caller then tries another file for the same year.
func extractText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		t, err := p.GetPlainText(nil)
		if err != nil || strings.TrimSpace(t) == "" {
			continue
		}
		b.WriteByte('\f')
		b.WriteByte('\n')
		b.WriteString(t)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return "", errors.New("pdf has no text")
	}
	return b.String(), nil
}
