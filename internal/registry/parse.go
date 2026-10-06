package registry

import "strings"

// ParseTickers turns a comma- or whitespace-separated secid list into a set
// (nil when empty, which Build treats as "the whole board").
func ParseTickers(raw string) map[string]struct{} {
	raw = strings.ReplaceAll(raw, ",", " ")
	var out map[string]struct{}
	for _, t := range strings.Fields(raw) {
		t = strings.ToUpper(t)
		if t == "" {
			continue
		}
		if out == nil {
			out = make(map[string]struct{})
		}
		out[t] = struct{}{}
	}
	return out
}
