package capellaapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
)

func TestResponseTooLarge(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		chunk := strings.Repeat("x", 1<<20)
		_, _ = w.Write([]byte(`{"data":"`))
		for i := 0; i <= maxResponseBody>>20; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return // client stopped reading at the limit
			}
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New("sk-test-key-1234", WithBaseURL(srv.URL), WithClock(engine.NewFakeClock(time.Time{})))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), http.MethodGet, "/v4/organizations", RequestOptions{}, nil)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("requests = %d, want 1 (an oversized body is not retried)", hits.Load())
	}
}

func TestResponseAtLimitIsRead(t *testing.T) {
	t.Parallel()
	body := `{"data":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := New("sk-test-key-1234", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(context.Background(), http.MethodGet, "/v4/organizations", RequestOptions{}, nil)
	if err != nil || string(resp.Body) != body {
		t.Fatalf("Do = %v, %v", resp, err)
	}
}
