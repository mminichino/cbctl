// Package capellaapi is a hardened client for the Couchbase Capella Management API v4.
//
// It retries transient failures with jittered backoff, never blindly replays a POST that may
// already have been applied, masks secrets in every error and log line, and surfaces typed
// errors that callers match with errors.Is / errors.As.
package capellaapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/redact"
)

const (
	defaultBaseURL     = "https://cloudapi.cloud.couchbase.com"
	defaultMaxAttempts = 6
	defaultVersion     = "dev"
)

// Client talks to the Capella Management API. It is safe for concurrent use.
type Client struct {
	apiKey      string
	baseURL     string
	http        *http.Client
	clock       engine.Clock
	maxAttempts int
	userAgent   string
	optErr      error

	// math/rand is deliberate: jitter needs reproducibility in tests, not cryptographic strength.
	rngMu sync.Mutex
	rng   *rand.Rand

	orgMu sync.RWMutex
	orgID string

	writes atomic.Int64
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL sets the API base URL. It accepts a bare host, host:port or a URL, and mirrors
// settings.NormalizeBaseURL (https only, except loopback and test hosts). An empty value selects
// the default. An invalid value makes New fail.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		n, err := normalizeBaseURL(u)
		if err != nil {
			c.optErr = err
			return
		}
		c.baseURL = n
	}
}

// WithOrgID sets the organization ID used by OrgPath.
func WithOrgID(id string) Option { return func(c *Client) { c.orgID = id } }

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithClock sets the clock used to sleep between retries.
func WithClock(clk engine.Clock) Option {
	return func(c *Client) {
		if clk != nil {
			c.clock = clk
		}
	}
}

// WithMaxAttempts sets the maximum number of attempts per request (default 6; n<1 means 1).
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n < 1 {
			n = 1
		}
		c.maxAttempts = n
	}
}

// WithRand sets the random source for backoff jitter so tests are deterministic.
func WithRand(r *rand.Rand) Option {
	return func(c *Client) {
		if r != nil {
			c.rng = r
		}
	}
}

// WithUserAgent sets the User-Agent to "cbctl/<version> (go/<runtime.Version()>)".
func WithUserAgent(version string) Option {
	return func(c *Client) { c.userAgent = userAgentFor(version) }
}

func userAgentFor(version string) string {
	if version == "" {
		version = defaultVersion
	}
	return fmt.Sprintf("cbctl/%s (go/%s)", version, runtime.Version())
}

// New builds a Client for apiKey and registers the key with the redactor.
func New(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("capellaapi: API key is empty")
	}
	c := &Client{
		apiKey:      apiKey,
		baseURL:     defaultBaseURL,
		clock:       engine.RealClock{},
		maxAttempts: defaultMaxAttempts,
		userAgent:   userAgentFor(defaultVersion),
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec // jitter only
	}
	for _, o := range opts {
		o(c)
	}
	if c.optErr != nil {
		return nil, fmt.Errorf("capellaapi: %w", c.optErr)
	}
	if c.http == nil {
		c.http = defaultHTTPClient()
	}
	redact.Register(apiKey)
	return c, nil
}

// defaultHTTPClient has no overall timeout: the request context governs, while dial, TLS and
// response-header timeouts keep a dead connection from hanging a retry loop.
func defaultHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
	}}
}

// OrgID returns the configured organization ID, or "" when unset.
func (c *Client) OrgID() string {
	c.orgMu.RLock()
	defer c.orgMu.RUnlock()
	return c.orgID
}

// SetOrgID sets the organization ID used by OrgPath.
func (c *Client) SetOrgID(id string) {
	c.orgMu.Lock()
	c.orgID = id
	c.orgMu.Unlock()
}

// OrgPath returns "/v4/organizations/<id>" or an error when no organization is set.
func (c *Client) OrgPath() (string, error) {
	id := c.OrgID()
	if id == "" {
		return "", errors.New("organization ID is not set")
	}
	return "/v4/organizations/" + url.PathEscape(id), nil
}

// WritesAccepted returns how many non-GET requests received a 2xx response.
func (c *Client) WritesAccepted() int { return int(c.writes.Load()) }

// RequestOptions are the optional parts of a request.
type RequestOptions struct {
	Query   url.Values
	IfMatch string // bare version, e.g. "12"
	Body    any    // marshaled once to JSON; nil means no body
}

// Response is a completed 2xx HTTP exchange. Do also returns it alongside an *APIError.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// ETag returns the resource version from the ETag header: `"Version: 12"`, `Version: 12` and
// `"12"` all yield "12". It returns "" when the header is absent.
func (r *Response) ETag() string {
	if r == nil {
		return ""
	}
	v := strings.TrimSpace(r.Header.Get("ETag"))
	v = strings.TrimPrefix(v, "W/")
	v = strings.Trim(v, `"`)
	if len(v) >= 8 && strings.EqualFold(v[:8], "version:") {
		v = v[8:]
	}
	return strings.TrimSpace(v)
}

// Get performs a GET and decodes the JSON response into out (which may be nil).
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) (*Response, error) {
	return c.Do(ctx, http.MethodGet, path, RequestOptions{Query: q}, out)
}

// Post performs a POST with a JSON body and decodes the response into out (which may be nil).
func (c *Client) Post(ctx context.Context, path string, body, out any) (*Response, error) {
	return c.Do(ctx, http.MethodPost, path, RequestOptions{Body: body}, out)
}

// Put performs a PUT with a JSON body and an optional If-Match version.
func (c *Client) Put(ctx context.Context, path string, body any, ifMatch string, out any) (*Response, error) {
	return c.Do(ctx, http.MethodPut, path, RequestOptions{Body: body, IfMatch: ifMatch}, out)
}

// Delete performs a DELETE.
func (c *Client) Delete(ctx context.Context, path string, q url.Values) (*Response, error) {
	return c.Do(ctx, http.MethodDelete, path, RequestOptions{Query: q}, nil)
}

// Do sends one logical request, retrying per the policy in retry.go. A 2xx response is decoded
// into out when out is non-nil and the body is non-empty. A non-2xx final response returns both
// the Response and an *APIError. See *TransportError and *AmbiguousWriteError for network failures.
func (c *Client) Do(ctx context.Context, method, path string, opt RequestOptions, out any) (*Response, error) {
	method = strings.ToUpper(method)

	var payload []byte
	if opt.Body != nil {
		b, err := json.Marshal(opt.Body)
		if err != nil {
			return nil, fmt.Errorf("encode %s %s body: %w", method, path, err)
		}
		payload = b
	}
	target := c.baseURL + path
	if len(opt.Query) > 0 {
		target += "?" + opt.Query.Encode()
	}
	logBody := redactedBody(payload)

	for attempt := 1; ; attempt++ {
		start := time.Now()
		res, err := c.attempt(ctx, method, target, payload, opt.IfMatch)
		if err != nil {
			return nil, err
		}

		if res.transportErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			kind := classifyTransport(res.transportErr)
			logging.Debug("%s %s -> %s (%dms)%s", method, path, kind, time.Since(start).Milliseconds(), logBody)
			if !idempotent(method) && res.wrote {
				return nil, &AmbiguousWriteError{Method: method, Path: path, Cause: res.transportErr}
			}
			if attempt >= c.maxAttempts {
				return nil, &TransportError{
					Msg:   fmt.Sprintf("%s %s: could not reach Capella (%s)", method, path, kind),
					Cause: res.transportErr,
				}
			}
			if err := c.waitBeforeRetry(ctx, method, path, attempt, c.backoff(attempt), kind); err != nil {
				return nil, err
			}
			continue
		}

		resp := res.resp
		logging.Debug("%s %s -> %d (%dms)%s", method, path, resp.Status, time.Since(start).Milliseconds(), logBody)

		if resp.Status >= 200 && resp.Status < 300 {
			if method != http.MethodGet {
				c.writes.Add(1)
			}
			if out != nil && resp.Status != http.StatusNoContent && len(bytes.TrimSpace(resp.Body)) > 0 {
				if err := json.Unmarshal(resp.Body, out); err != nil {
					return resp, fmt.Errorf("decode %s %s: %w", method, path, err)
				}
			}
			return resp, nil
		}

		apiErr := newAPIError(method, path, resp.Status, resp.Body)
		retry := attempt < c.maxAttempts && retryableStatus(resp.Status) &&
			(idempotent(method) || resp.Status == http.StatusTooManyRequests)
		if !retry {
			return resp, apiErr
		}
		delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), c.clock.Now())
		if !ok {
			delay = c.backoff(attempt)
		}
		if err := c.waitBeforeRetry(ctx, method, path, attempt, delay, strconv.Itoa(resp.Status)); err != nil {
			return nil, err
		}
	}
}

// attemptResult is the outcome of a single HTTP round trip.
type attemptResult struct {
	resp         *Response
	transportErr error // set when no complete response was received
	wrote        bool  // the request was fully or partly written to the connection
}

// attempt performs one round trip. The returned error is non-retryable (a malformed request);
// network failures are reported through attemptResult.transportErr instead.
func (c *Client) attempt(ctx context.Context, method, target string, payload []byte, ifMatch string) (attemptResult, error) {
	var wrote atomic.Bool
	trace := &httptrace.ClientTrace{
		// Fires once the request has been handed to the connection, even if the write failed.
		WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) },
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload) // fresh reader per attempt
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, target, body)
	if err != nil {
		return attemptResult{}, fmt.Errorf("build request: %w", redactErr(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}

	hr, err := c.http.Do(req)
	if err != nil {
		return attemptResult{transportErr: redactErr(err), wrote: wrote.Load()}, nil
	}
	defer func() { _ = hr.Body.Close() }() // body fully read below; close errors carry no information
	data, err := io.ReadAll(hr.Body)
	if err != nil {
		return attemptResult{transportErr: redactErr(err), wrote: wrote.Load()}, nil
	}
	return attemptResult{resp: &Response{Status: hr.StatusCode, Header: hr.Header, Body: data}}, nil
}

// redactedBody renders the request body for debug logs with secrets masked.
func redactedBody(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return ""
	}
	b, err := json.Marshal(redact.Object(v))
	if err != nil {
		return ""
	}
	return " body=" + string(b)
}

// maskedError keeps the error chain intact while masking secrets in the message.
type maskedError struct {
	msg   string
	cause error
}

func (e *maskedError) Error() string { return e.msg }
func (e *maskedError) Unwrap() error { return e.cause }

func redactErr(err error) error {
	if msg := redact.String(err.Error()); msg != err.Error() {
		return &maskedError{msg: msg, cause: err}
	}
	return err
}

// normalizeBaseURL mirrors settings.NormalizeBaseURL (copied to avoid importing settings).
func normalizeBaseURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultBaseURL, nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid API base URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("invalid API base URL: missing host")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && (scheme != "http" || !isLocalHost(host)) {
		return "", fmt.Errorf("API base URL must use https (got %s)", scheme)
	}
	return strings.TrimRight(scheme+"://"+u.Host, "/"), nil
}

func isLocalHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]" {
		return true
	}
	if strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".test") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
