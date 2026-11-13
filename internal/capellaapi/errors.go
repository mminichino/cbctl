package capellaapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/redact"
)

const (
	maxBodyKept     = 2048
	maxFallbackText = 200

	hintUnauthorized = "use the API key token (not the key ID) and check the key's IP allow-list"
)

// Sentinel errors matched through APIError.Is by HTTP status.
var (
	// ErrUnauthorized matches HTTP 401.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden matches HTTP 403.
	ErrForbidden = errors.New("forbidden")
	// ErrNotFound matches HTTP 404. It is the same value as engine.ErrNotFound so the cluster
	// waiter (which cannot import this package) recognizes it.
	ErrNotFound = engine.ErrNotFound
	// ErrConflict matches HTTP 409.
	ErrConflict = errors.New("conflict")
	// ErrPreconditionFailed matches HTTP 412.
	ErrPreconditionFailed = errors.New("precondition failed")
	// ErrValidation matches HTTP 400 and 422.
	ErrValidation = errors.New("validation failed")
	// ErrRateLimited matches HTTP 429.
	ErrRateLimited = errors.New("rate limited")
	// ErrServer matches HTTP 5xx.
	ErrServer = errors.New("server error")
)

// APIError is a non-2xx response from the Capella Management API.
type APIError struct {
	Status  int
	Code    int    // Capella "code" field, 0 if absent
	Message string // redacted
	Hint    string // redacted
	Method  string
	Path    string
	Body    string // raw body, redacted, truncated to 2 KiB; for debug/legacy compatibility only
}

// Error implements error as "<METHOD> <path>: <status> <message>" plus an optional hint.
func (e *APIError) Error() string {
	s := fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
	if e.Hint != "" {
		s += " (hint: " + e.Hint + ")"
	}
	// Masked again so a secret registered after the error was built still never leaks.
	return redact.String(s)
}

// Is maps the HTTP status to the package sentinel errors.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == 401
	case ErrForbidden:
		return e.Status == 403
	case ErrNotFound:
		return e.Status == 404
	case ErrConflict:
		return e.Status == 409
	case ErrPreconditionFailed:
		return e.Status == 412
	case ErrValidation:
		return e.Status == 400 || e.Status == 422
	case ErrRateLimited:
		return e.Status == 429
	case ErrServer:
		return e.Status >= 500
	}
	return false
}

// AmbiguousWriteError reports a non-idempotent request that was (at least partly) sent
// before the connection failed, so the server may or may not have applied it.
type AmbiguousWriteError struct {
	Method string
	Path   string
	Cause  error
}

// Error implements error.
func (e *AmbiguousWriteError) Error() string {
	return redact.String(fmt.Sprintf("%s %s: request was sent but the outcome is unknown (%v); check the resource before retrying",
		e.Method, e.Path, e.Cause))
}

// Unwrap returns the underlying transport error.
func (e *AmbiguousWriteError) Unwrap() error { return e.Cause }

// TransportError reports that Capella could not be reached after all attempts.
type TransportError struct {
	Msg   string
	Cause error
}

// Error implements error.
func (e *TransportError) Error() string { return redact.String(e.Msg) }

// Unwrap returns the underlying transport error.
func (e *TransportError) Unwrap() error { return e.Cause }

// newAPIError builds an APIError from a response, parsing Capella's
// {"httpStatusCode","code","message","hint"} envelope and enriching 401/403 hints.
func newAPIError(method, path string, status int, body []byte) *APIError {
	e := &APIError{Status: status, Method: method, Path: path}

	var env map[string]any
	if json.Unmarshal(body, &env) == nil {
		e.Message, _ = env["message"].(string)
		e.Hint, _ = env["hint"].(string)
		if n, ok := env["code"].(float64); ok {
			e.Code = int(n)
		}
	}
	if e.Message == "" {
		e.Message = fallbackMessage(status, body)
	}

	switch status {
	case 401:
		e.Hint = hintUnauthorized
	case 403:
		if e.Hint == "" {
			if roles := RequiredRoles(method, path); roles != "" {
				e.Hint = "requires: " + roles
			}
		}
	}

	e.Message = redact.String(e.Message)
	e.Hint = redact.String(e.Hint)
	e.Body = truncateUTF8(redact.String(string(body)), maxBodyKept)
	return e
}

// fallbackMessage is "HTTP <status>: <first 200 chars of body>" for bodies without a message.
func fallbackMessage(status int, body []byte) string {
	text := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(string(body))
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	if utf8.RuneCountInString(text) > maxFallbackText {
		text = string([]rune(text)[:maxFallbackText])
	}
	return fmt.Sprintf("HTTP %d: %s", status, text)
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
