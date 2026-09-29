package moex

import (
	"os"
	"testing"
)

func TestParseIssueSize(t *testing.T) {
	body, err := os.ReadFile("testdata/lkoh_desc.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := ParseIssueSize(body)
	if err != nil {
		t.Fatalf("ParseIssueSize: %v", err)
	}
	const want = 692865762
	if got != want {
		t.Errorf("ISSUESIZE = %v, want %v", got, want)
	}
}

func TestParseLastClose(t *testing.T) {
	body, err := os.ReadFile("testdata/lkoh_history.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := ParseLastClose(body)
	if err != nil {
		t.Fatalf("ParseLastClose: %v", err)
	}
	// Last (chronologically latest) CLOSE in the Dec-2024 window fixture.
	const want = 7235
	if got != want {
		t.Errorf("last close = %v, want %v", got, want)
	}
}

func TestParseLastCloseEmpty(t *testing.T) {
	// No history rows -> 0, no error (security not traded in the window).
	got, err := ParseLastClose([]byte(`{"history":{"columns":["CLOSE"],"data":[]}}`))
	if err != nil {
		t.Fatalf("ParseLastClose: %v", err)
	}
	if got != 0 {
		t.Errorf("empty history close = %v, want 0", got)
	}
}
