// Package version exposes build metadata stamped into the binary at link time
// via -ldflags "-X github.com/VxVxN/financialanalyzer/internal/version.Version=...".
package version

// These are overridden at build time; the defaults apply to `go run` / `go build`
// without ldflags so the values are always meaningful.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info is the structured form returned by the /version endpoint.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Get returns the current build info.
func Get() Info {
	return Info{Version: Version, Commit: Commit, Date: Date}
}
