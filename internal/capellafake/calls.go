package capellafake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/redact"
)

// Call is one served request in the Phase 4 inspection API (alias over RequestLog entries).
type Call struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   any // decoded JSON when the body was a JSON value; else the raw string
	Status int
	Time   time.Time
}

// String redacts secret-bearing bodies for safe failure output.
func (c Call) String() string {
	return fmt.Sprintf("%s %s status=%d body=%v", c.Method, c.Path, c.Status, redact.Object(c.Body))
}

// Calls returns logged requests matching method ("" or "*" = any) and a path.Match glob.
// Glob patterns may use "{id}"-style segments, which are treated as "*".
func (f *Fake) Calls(method, pathGlob string) []Call {
	method = strings.ToUpper(method)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, 0)
	for _, r := range f.log {
		if !(method == "" || method == "*" || r.Method == method) {
			continue
		}
		if !matchPathGlob(pathGlob, r.Path) {
			continue
		}
		out = append(out, requestToCall(r))
	}
	return out
}

// Writes returns logged mutating requests (POST/PUT/PATCH/DELETE).
func (f *Fake) Writes() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, 0)
	for _, r := range f.log {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			out = append(out, requestToCall(r))
		}
	}
	return out
}

// ResetCalls clears the request log (alias of ResetLog).
func (f *Fake) ResetCalls() { f.ResetLog() }

func requestToCall(r Request) Call {
	q, _ := url.ParseQuery(r.RawQuery)
	var body any = r.Body
	if strings.TrimSpace(r.Body) != "" {
		var decoded any
		if err := json.Unmarshal([]byte(r.Body), &decoded); err == nil {
			body = decoded
		}
	}
	return Call{
		Method: r.Method,
		Path:   r.Path,
		Query:  q,
		Header: r.Header,
		Body:   body,
		Status: r.Status,
		Time:   r.Time,
	}
}
