package main

import (
	"testing"
	"time"

	"github.com/VxVxN/financialanalyzer/internal/models"
	"github.com/VxVxN/financialanalyzer/internal/scheduler"
)

func TestDigestPending(t *testing.T) {
	quotes := scheduler.Spec{Hour: 7}
	// 2026-10-08 is a Thursday; that week's Monday is 2026-10-05.
	thu := time.Date(2026, 10, 8, 12, 0, 0, 0, models.Moscow)
	monEarly := time.Date(2026, 10, 5, 6, 30, 0, 0, models.Moscow)
	monSlot := time.Date(2026, 10, 5, 7, 0, 0, 0, models.Moscow)
	sun := time.Date(2026, 10, 11, 18, 0, 0, 0, models.Moscow)

	if d := weekMonday(thu); !d.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, models.Moscow)) {
		t.Fatalf("Thursday's Monday = %v", d)
	}
	if d := weekMonday(sun); !d.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, models.Moscow)) {
		t.Fatalf("Sunday's Monday = %v", d)
	}
	if digestPending(monEarly, quotes, false) {
		t.Error("before Monday's quotes slot the note is not owed")
	}
	if !digestPending(monSlot, quotes, false) {
		t.Error("at the Monday slot the note is owed")
	}
	if !digestPending(thu, quotes, false) {
		t.Error("a later restart still owes the unsent Monday note")
	}
	if digestPending(thu, quotes, true) {
		t.Error("a recorded Monday must not be sent again")
	}
	if !digestPending(sun, quotes, false) {
		t.Error("Sunday still owes an unsent Monday of the same week")
	}
}
