package capellaapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
)

const testKey = "sk-retry-test-KEY-4471"

// testClient builds a Client against base using a FakeClock and a seeded jitter source.
func testClient(t *testing.T, base string, clk engine.Clock, opts ...Option) *Client {
	t.Helper()
	all := append([]Option{WithBaseURL(base), WithClock(clk), WithRand(rand.New(rand.NewSource(42)))}, opts...)
	c, err := New(testKey, all...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// jitter replays the seeded sequence the client draws from, returning the delay before attempt n+1.
func jitter(seed int64, failures ...int) []time.Duration {
	r := rand.New(rand.NewSource(seed))
	out := make([]time.Duration, 0, len(failures))
	for _, n := range failures {
		ceiling := backoffCap
		if n-1 < 5 {
			ceiling = time.Second << uint(n-1)
		}
		out = append(out, time.Duration(r.Float64()*float64(ceiling)))
	}
	return out
}

// scripted serves the given handlers in order, repeating the last one, and counts hits.
func scripted(t *testing.T, steps ...http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(hits.Add(1)) - 1
		if i >= len(steps) {
			i = len(steps) - 1
		}
		steps[i](w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, `{"message":"status `+fmt.Sprint(code)+`"}`)
	}
}

func okJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// drop reads the request, then closes the connection without answering.
func drop(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func TestRetryGETThenSuccessUsesJitterCeilings(t *testing.T) {
	srv, hits := scripted(t, status(503), status(503), okJSON(`{"ok":true}`))
	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, clk)

	var out struct{ OK bool }
	if _, err := c.Get(context.Background(), "/v4/organizations", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || hits.Load() != 3 {
		t.Fatalf("out=%+v hits=%d", out, hits.Load())
	}
	sleeps := clk.Sleeps()
	want := jitter(42, 1, 2)
	if len(sleeps) != 2 || sleeps[0] != want[0] || sleeps[1] != want[1] {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	if sleeps[0] > time.Second || sleeps[1] > 2*time.Second {
		t.Errorf("sleeps %v exceed ceilings 1s, 2s", sleeps)
	}
}

func TestBackoffCeilings(t *testing.T) {
	t.Parallel()
	c := testClient(t, "http://127.0.0.1:1", engine.NewFakeClock(time.Time{}), WithRand(rand.New(rand.NewSource(7))))
	wantCeil := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		30 * time.Second, 30 * time.Second}
	for i, ceil := range wantCeil {
		for range 50 {
			if d := c.backoff(i + 1); d < 0 || d > ceil {
				t.Fatalf("backoff(%d) = %v, want within [0,%v]", i+1, d, ceil)
			}
		}
	}
}

func TestRetryAfterHeader(t *testing.T) {
	httpDate := func(clk *engine.FakeClock, d time.Duration) string {
		return clk.Now().Add(d).UTC().Format(http.TimeFormat)
	}
	tests := []struct {
		name   string
		header func(*engine.FakeClock) string
		want   time.Duration
	}{
		{"seconds", func(*engine.FakeClock) string { return "7" }, 7 * time.Second},
		{"zero", func(*engine.FakeClock) string { return "0" }, 0},
		{"negative clamps to zero", func(*engine.FakeClock) string { return "-5" }, 0},
		{"clamped to five minutes", func(*engine.FakeClock) string { return "99999" }, 5 * time.Minute},
		{"overflow clamps", func(*engine.FakeClock) string { return "99999999999999999999999" }, 5 * time.Minute},
		{"http date", func(c *engine.FakeClock) string { return httpDate(c, 12*time.Second) }, 12 * time.Second},
		{"http date in the past", func(c *engine.FakeClock) string { return httpDate(c, -time.Hour) }, 0},
		{"http date far future clamps", func(c *engine.FakeClock) string { return httpDate(c, 24*time.Hour) }, 5 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := engine.NewFakeClock(time.Time{})
			header := tt.header(clk)
			srv, _ := scripted(t,
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", header)
					w.WriteHeader(429)
				},
				okJSON(`{}`))
			c := testClient(t, srv.URL, clk)
			if _, err := c.Get(context.Background(), "/v4/organizations", nil, nil); err != nil {
				t.Fatal(err)
			}
			if got := clk.Sleeps(); len(got) != 1 || got[0] != tt.want {
				t.Fatalf("sleeps = %v, want [%v]", got, tt.want)
			}
		})
	}
}

func TestRetryAfterBeatsBackoffOn503(t *testing.T) {
	srv, _ := scripted(t,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(503)
		},
		okJSON(`{}`))
	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, clk)
	if _, err := c.Get(context.Background(), "/v4/organizations", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := clk.Sleeps(); len(got) != 1 || got[0] != 7*time.Second {
		t.Fatalf("sleeps = %v, want [7s]", got)
	}
}

func TestGETRetriedOnConnectionReset(t *testing.T) {
	srv, hits := scripted(t, drop, okJSON(`{"ok":true}`))
	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, clk)
	var out struct{ OK bool }
	if _, err := c.Get(context.Background(), "/v4/organizations", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || hits.Load() != 2 || len(clk.Sleeps()) != 1 {
		t.Fatalf("out=%+v hits=%d sleeps=%v", out, hits.Load(), clk.Sleeps())
	}
}

func TestIdempotentMethodsRetryTransportErrors(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			srv, hits := scripted(t, drop, status(204))
			clk := engine.NewFakeClock(time.Time{})
			c := testClient(t, srv.URL, clk)
			_, err := c.Do(context.Background(), method, "/v4/x", RequestOptions{Body: map[string]string{"a": "b"}}, nil)
			if err != nil || hits.Load() != 2 {
				t.Fatalf("err=%v hits=%d", err, hits.Load())
			}
		})
	}
}

func TestPOSTServerErrorNotRetried(t *testing.T) {
	srv, hits := scripted(t, status(500), okJSON(`{}`))
	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, clk)
	_, err := c.Post(context.Background(), "/v4/organizations/o/projects", map[string]string{"name": "p"}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 500 || !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v, want 500 *APIError", err)
	}
	if hits.Load() != 1 || len(clk.Sleeps()) != 0 {
		t.Fatalf("hits=%d sleeps=%v, want no retry", hits.Load(), clk.Sleeps())
	}
}

func TestPOSTRetriedOn429(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	record := func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}
	srv, hits := scripted(t,
		func(w http.ResponseWriter, r *http.Request) {
			record(r)
			w.WriteHeader(429)
		},
		func(w http.ResponseWriter, r *http.Request) {
			record(r)
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"abc"}`)
		})
	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, clk)
	var out IDResponse
	if _, err := c.Post(context.Background(), "/v4/organizations/o/projects", map[string]string{"name": "p"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "abc" || hits.Load() != 2 || len(clk.Sleeps()) != 1 {
		t.Fatalf("out=%+v hits=%d sleeps=%v", out, hits.Load(), clk.Sleeps())
	}
	// A fresh reader per attempt means the retry carries the whole body again.
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || bodies[0] != `{"name":"p"}` || bodies[1] != bodies[0] {
		t.Errorf("bodies = %q", bodies)
	}
}

func TestPOSTAmbiguousAfterRequestSent(t *testing.T) {
	srv, hits := scripted(t, drop, okJSON(`{"id":"never"}`))
	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, clk)
	_, err := c.Post(context.Background(), "/v4/organizations/o/projects", map[string]string{"name": "p"}, nil)
	var amb *AmbiguousWriteError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %T %v, want *AmbiguousWriteError", err, err)
	}
	if amb.Method != "POST" || amb.Path != "/v4/organizations/o/projects" || amb.Cause == nil {
		t.Errorf("unexpected %+v", amb)
	}
	if hits.Load() != 1 || len(clk.Sleeps()) != 0 {
		t.Fatalf("hits=%d sleeps=%v, want no retry", hits.Load(), clk.Sleeps())
	}
}

func TestPOSTToClosedPortRetriedThenTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // nothing listens here any more: dial fails before any byte is written

	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, base, clk)
	_, err := c.Post(context.Background(), "/v4/organizations/o/projects", map[string]string{"name": "p"}, nil)
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T %v, want *TransportError", err, err)
	}
	want := "POST /v4/organizations/o/projects: could not reach Capella (connection refused)"
	if te.Msg != want || te.Cause == nil {
		t.Errorf("Msg = %q, want %q (cause %v)", te.Msg, want, te.Cause)
	}
	if got := len(clk.Sleeps()); got != defaultMaxAttempts-1 {
		t.Errorf("sleeps = %d, want %d", got, defaultMaxAttempts-1)
	}
	var amb *AmbiguousWriteError
	if errors.As(err, &amb) {
		t.Errorf("a dial failure must not be reported as ambiguous")
	}
}

func TestGETTransportErrorExhausted(t *testing.T) {
	srv, hits := scripted(t, drop)
	clk := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, clk, WithMaxAttempts(3))
	_, err := c.Get(context.Background(), "/v4/organizations", nil, nil)
	var te *TransportError
	if !errors.As(err, &te) || !strings.Contains(te.Msg, "GET /v4/organizations: could not reach Capella (") {
		t.Fatalf("err = %v, want *TransportError", err)
	}
	if hits.Load() != 3 || len(clk.Sleeps()) != 2 {
		t.Fatalf("hits=%d sleeps=%v", hits.Load(), clk.Sleeps())
	}
}

func TestMaxAttempts(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want int32
	}{
		{"default is 6", nil, 6},
		{"custom", []Option{WithMaxAttempts(2)}, 2},
		{"below one means one", []Option{WithMaxAttempts(0)}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, hits := scripted(t, status(503))
			clk := engine.NewFakeClock(time.Time{})
			c := testClient(t, srv.URL, clk, tt.opts...)
			_, err := c.Get(context.Background(), "/v4/organizations", nil, nil)
			if !errors.Is(err, ErrServer) {
				t.Fatalf("err = %v, want ErrServer from the last response", err)
			}
			if hits.Load() != tt.want || int32(len(clk.Sleeps())) != tt.want-1 {
				t.Fatalf("hits=%d sleeps=%d, want %d attempts", hits.Load(), len(clk.Sleeps()), tt.want)
			}
		})
	}
}

func TestNonRetryableStatusesReturnImmediately(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 412, 422} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			srv, hits := scripted(t, status(code))
			clk := engine.NewFakeClock(time.Time{})
			c := testClient(t, srv.URL, clk)
			resp, err := c.Get(context.Background(), "/v4/organizations", nil, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Status != code {
				t.Fatalf("err = %v", err)
			}
			if resp == nil || resp.Status != code {
				t.Errorf("the Response should accompany the APIError, got %+v", resp)
			}
			if hits.Load() != 1 || len(clk.Sleeps()) != 0 {
				t.Fatalf("hits=%d sleeps=%v", hits.Load(), clk.Sleeps())
			}
		})
	}
}

// cancelClock cancels the request context the moment the client starts backing off.
type cancelClock struct {
	*engine.FakeClock
	cancel context.CancelFunc
}

func (c cancelClock) Sleep(ctx context.Context, d time.Duration) error {
	c.cancel()
	return c.FakeClock.Sleep(ctx, d)
}

func TestContextCancelDuringBackoff(t *testing.T) {
	srv, hits := scripted(t, status(503))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := engine.NewFakeClock(time.Time{})
	c := testClient(t, srv.URL, cancelClock{FakeClock: fake, cancel: cancel})

	_, err := c.Get(ctx, "/v4/organizations", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if hits.Load() != 1 || len(fake.Sleeps()) != 0 {
		t.Fatalf("hits=%d sleeps=%v: a cancelled backoff must not retry", hits.Load(), fake.Sleeps())
	}
}

func TestContextCancelledBeforeRequest(t *testing.T) {
	srv, hits := scripted(t, okJSON(`{}`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := testClient(t, srv.URL, engine.NewFakeClock(time.Time{}))
	_, err := c.Get(ctx, "/v4/organizations", nil, nil)
	if !errors.Is(err, context.Canceled) || hits.Load() != 0 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
}

func TestParseRetryAfterInvalid(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"", "   ", "soon", "1.5", "Tue, 99 Foo 2026"} {
		if d, ok := parseRetryAfter(h, time.Now()); ok {
			t.Errorf("parseRetryAfter(%q) = %v, true; want not ok", h, d)
		}
	}
}

func TestClassifyTransport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"eof", io.EOF, "connection closed"},
		{"unexpected eof", fmt.Errorf("read: %w", io.ErrUnexpectedEOF), "connection closed"},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"other", errors.New("weird"), "network error"},
		{"tls text", errors.New("tls: handshake failure"), "tls error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyTransport(tt.err); got != tt.want {
				t.Errorf("classifyTransport = %q, want %q", got, tt.want)
			}
		})
	}
}
