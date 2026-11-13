package capellaapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mminichino/cbctl/internal/logging"
)

const (
	backoffBase   = time.Second
	backoffCap    = 30 * time.Second
	retryAfterCap = 5 * time.Minute
)

// retryableStatus reports whether a response status is worth retrying.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// idempotent reports whether replaying the method is safe. Everything else (POST, PATCH) is
// treated like POST: retried only on 429 or when the request provably never left.
func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// backoff returns the full-jitter delay after the failedth attempt:
// rand * min(30s, 1s * 2^(failed-1)).
func (c *Client) backoff(failed int) time.Duration {
	ceiling := backoffCap
	if failed >= 1 && failed-1 < 5 { // 1,2,4,8,16 seconds; 2^5 would exceed the cap
		ceiling = backoffBase << uint(failed-1)
	}
	c.rngMu.Lock()
	f := c.rng.Float64()
	c.rngMu.Unlock()
	return time.Duration(f * float64(ceiling))
}

// parseRetryAfter reads a Retry-After header (integer seconds or HTTP-date) relative to now.
// The result is clamped to [0, 5m]. ok is false when the header is absent or unparseable.
func parseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(h, 10, 64); err == nil {
		switch {
		case n <= 0:
			return 0, true
		case n >= int64(retryAfterCap/time.Second):
			return retryAfterCap, true
		}
		return time.Duration(n) * time.Second, true
	} else if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(h, "-") {
		return retryAfterCap, true
	}
	t, err := http.ParseTime(h)
	if err != nil {
		return 0, false
	}
	return min(max(t.Sub(now), 0), retryAfterCap), true
}

// waitBeforeRetry logs and sleeps through the injected clock; it aborts on context cancellation.
func (c *Client) waitBeforeRetry(ctx context.Context, method, path string, attempt int, d time.Duration, reason string) error {
	logging.Warning("retrying %s %s (attempt %d/%d) after %s", method, path, attempt+1, c.maxAttempts, reason)
	return c.clock.Sleep(ctx, d)
}

// classifyTransport names the failure class for messages and logs without echoing raw errors.
func classifyTransport(err error) string {
	var (
		dns     *net.DNSError
		netErr  net.Error
		certErr *tls.CertificateVerificationError
		unknown x509.UnknownAuthorityError
		hostErr x509.HostnameError
		tlsRec  tls.RecordHeaderError
	)
	switch {
	case errors.As(err, &dns):
		return "dns lookup failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.As(err, &certErr), errors.As(err, &unknown), errors.As(err, &hostErr), errors.As(err, &tlsRec):
		return "tls error"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	}
	if strings.Contains(err.Error(), "tls:") {
		return "tls error"
	}
	return "network error"
}
