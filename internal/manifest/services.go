package manifest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/yamlio"
)

// The one service vocabulary of both kinds. CouchbaseServer also accepts ServiceBackup and the
// alias "fts" for "search".
const (
	ServiceData      = "data"
	ServiceIndex     = "index"
	ServiceQuery     = "query"
	ServiceSearch    = "search"
	ServiceAnalytics = "analytics"
	ServiceEventing  = "eventing"
	ServiceBackup    = "backup"
)

var (
	commonServices = []string{ServiceData, ServiceIndex, ServiceQuery, ServiceSearch, ServiceAnalytics, ServiceEventing}
	// DefaultNodeServices is applied to Server nodes that set neither services nor arbiter.
	DefaultNodeServices = ServiceSet{ServiceData, ServiceIndex, ServiceQuery, ServiceSearch}
)

// Has reports whether the set contains service (compared as written).
func (s ServiceSet) Has(service string) bool {
	for _, x := range s {
		if x == service {
			return true
		}
	}
	return false
}

// String returns the comma-separated names.
func (s ServiceSet) String() string { return strings.Join(s, ",") }

// Canonical returns the set with names lower-cased and trimmed, "fts" mapped to "search",
// duplicates removed and the names sorted. A nil or empty set stays nil.
func (s ServiceSet) Canonical() ServiceSet {
	if len(s) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out ServiceSet
	for _, x := range s {
		x = strings.ToLower(strings.TrimSpace(x))
		if x == "fts" {
			x = ServiceSearch
		}
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// checkServices reports every name of s that the kind does not offer, with a suggestion.
func (c *collector) checkServices(pointer string, s ServiceSet, server bool) {
	allowed := append([]string(nil), commonServices...)
	if server {
		allowed = append(allowed, ServiceBackup)
	}
	for i, raw := range s {
		name := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case name == "fts" && server:
			continue
		case name == "fts":
			c.add(ptr(pointer, i), `"fts" is an alias that only CouchbaseServer accepts; use search`)
			continue
		}
		known := false
		for _, a := range allowed {
			known = known || a == name
		}
		if known {
			continue
		}
		msg := fmt.Sprintf("unknown service %q", raw)
		if name == ServiceBackup {
			msg = `the backup service exists only on CouchbaseServer`
		} else if g := yamlio.ClosestKey(name, allowed); g != "" {
			msg += fmt.Sprintf(" (did you mean %s?)", g)
		}
		c.add(ptr(pointer, i), msg, "use one of: "+strings.Join(allowed, ", "))
	}
}

// Duration is a wait timeout as written in the manifest: Go syntax with case-insensitive units
// ("45m", "1H30M") or a bare number of seconds ("90"). Decoding keeps the text so validation
// can report a bad value with its line.
type Duration string

// UnmarshalJSON keeps a string as written and any other JSON value as its text.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*d = Duration(s)
		return nil
	}
	*d = Duration(strings.TrimSpace(string(b)))
	return nil
}

// MaxWait is the longest wait timeout.
const MaxWait = 24 * time.Hour

// DefaultWait is the wait timeout when spec.wait.timeout is absent.
const DefaultWait = 60 * time.Minute

// Parse returns the duration. It must be positive and at most 24h.
func (d Duration) Parse() (time.Duration, error) {
	v := strings.ToLower(strings.TrimSpace(string(d)))
	if v != "" && strings.Trim(v, "0123456789") == "" {
		v += "s"
	}
	out, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration; use for example 45m, 2h or 90 (seconds)", string(d))
	}
	switch {
	case out <= 0:
		return 0, fmt.Errorf("must be positive (got %q)", string(d))
	case out > MaxWait:
		return 0, fmt.Errorf("must be at most 24h (got %q)", string(d))
	}
	return out, nil
}
