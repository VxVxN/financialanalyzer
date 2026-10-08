package models

// DigestIFRSYear is a Monday-note event: the IFRS pull wrote a company-year
// that had no manual row before.
const DigestIFRSYear = "ifrs_year"

// DigestEvent is one operator event waiting for the next Monday note.
type DigestEvent struct {
	ID      int64
	Kind    string
	Company string
	Detail  string
}
