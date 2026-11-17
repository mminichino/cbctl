package capellaapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
)

type item struct {
	ID string `json:"id"`
}

// pageServer serves handler and records every request query.
func pageServer(t *testing.T, handler func(w http.ResponseWriter, page int)) (*Client, func() []url.Values) {
	t.Helper()
	var (
		mu      sync.Mutex
		queries []url.Values
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query())
		mu.Unlock()
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		handler(w, page)
	}))
	t.Cleanup(srv.Close)
	c := testClient(t, srv.URL, engine.NewFakeClock(time.Time{}))
	return c, func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		return append([]url.Values(nil), queries...)
	}
}

func envelope(key string, ids []string, next int) string {
	items := ""
	for i, id := range ids {
		if i > 0 {
			items += ","
		}
		items += fmt.Sprintf(`{"id":%q}`, id)
	}
	cursor := `"cursor":{"pages":{"page":1}}`
	if next > 0 {
		cursor = fmt.Sprintf(`"cursor":{"pages":{"next":%d}}`, next)
	}
	return fmt.Sprintf(`{%q:[%s],%s}`, key, items, cursor)
}

func ids(items []item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestPaginateThreePages(t *testing.T) {
	c, queries := pageServer(t, func(w http.ResponseWriter, page int) {
		switch page {
		case 1:
			fmt.Fprint(w, envelope("data", []string{"a", "b"}, 2))
		case 2:
			fmt.Fprint(w, envelope("data", []string{"c"}, 3))
		default:
			fmt.Fprint(w, envelope("data", []string{"d"}, 0))
		}
	})
	got, err := Paginate[item](context.Background(), c, "/v4/organizations", "data", url.Values{"filter": {"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(got)) != "[a b c d]" {
		t.Errorf("items = %v", ids(got))
	}
	qs := queries()
	if len(qs) != 3 {
		t.Fatalf("requests = %d, want 3", len(qs))
	}
	for i, q := range qs {
		if q.Get("perPage") != "100" || q.Get("page") != strconv.Itoa(i+1) || q.Get("filter") != "x" {
			t.Errorf("request %d query = %v", i+1, q)
		}
	}
}

func TestPaginateSinglePage(t *testing.T) {
	c, queries := pageServer(t, func(w http.ResponseWriter, _ int) {
		fmt.Fprint(w, envelope("data", []string{"only"}, 0))
	})
	got, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil)
	if err != nil || fmt.Sprint(ids(got)) != "[only]" || len(queries()) != 1 {
		t.Fatalf("got=%v err=%v requests=%d", got, err, len(queries()))
	}
}

func TestPaginateTopLevelArray(t *testing.T) {
	c, queries := pageServer(t, func(w http.ResponseWriter, _ int) {
		fmt.Fprint(w, `[{"id":"x"},{"id":"y"}]`)
	})
	got, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil)
	if err != nil || fmt.Sprint(ids(got)) != "[x y]" || len(queries()) != 1 {
		t.Fatalf("got=%v err=%v requests=%d", got, err, len(queries()))
	}
}

func TestPaginateCustomKey(t *testing.T) {
	c, _ := pageServer(t, func(w http.ResponseWriter, page int) {
		if page == 1 {
			fmt.Fprint(w, envelope("scopes", []string{"s1"}, 2))
			return
		}
		fmt.Fprint(w, envelope("scopes", []string{"s2"}, 0))
	})
	got, err := Paginate[item](context.Background(), c, "/v4/scopes", "scopes", nil)
	if err != nil || fmt.Sprint(ids(got)) != "[s1 s2]" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	// An absent key yields no items rather than an error.
	empty, err := Paginate[item](context.Background(), c, "/v4/scopes", "data", nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("missing key: got=%v err=%v", empty, err)
	}
}

func TestPaginateDoesNotMutateCallerQuery(t *testing.T) {
	c, _ := pageServer(t, func(w http.ResponseWriter, _ int) { fmt.Fprint(w, envelope("data", nil, 0)) })
	q := url.Values{"a": {"1"}}
	if _, err := Paginate[item](context.Background(), c, "/v4/x", "data", q); err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || q.Get("a") != "1" {
		t.Errorf("caller query mutated: %v", q)
	}
}

func TestPaginateStopsWhenNextDoesNotAdvance(t *testing.T) {
	c, queries := pageServer(t, func(w http.ResponseWriter, _ int) {
		fmt.Fprint(w, envelope("data", []string{"a"}, 1)) // next never moves past the page asked for
	})
	got, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil)
	if err != nil || fmt.Sprint(ids(got)) != "[a]" || len(queries()) != 1 {
		t.Fatalf("got=%v err=%v requests=%d", got, err, len(queries()))
	}
}

func TestPaginatePageLimit(t *testing.T) {
	old := maxPages
	maxPages = 5
	t.Cleanup(func() { maxPages = old })

	c, queries := pageServer(t, func(w http.ResponseWriter, page int) {
		fmt.Fprint(w, envelope("data", []string{"p" + strconv.Itoa(page)}, page+1)) // endless
	})
	got, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil)
	if err == nil {
		t.Fatalf("expected an error after the page limit, got %d items", len(got))
	}
	if got != nil {
		t.Errorf("items should be nil on error, got %v", got)
	}
	if n := len(queries()); n != 5 {
		t.Errorf("requests = %d, want 5", n)
	}
}

func TestPaginateErrors(t *testing.T) {
	t.Run("404 is surfaced, not swallowed", func(t *testing.T) {
		c, _ := pageServer(t, func(w http.ResponseWriter, _ int) {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"project not found"}`)
		})
		got, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil)
		if !errors.Is(err, ErrNotFound) || got != nil {
			t.Fatalf("got=%v err=%v, want ErrNotFound", got, err)
		}
	})
	t.Run("error on a later page", func(t *testing.T) {
		c, _ := pageServer(t, func(w http.ResponseWriter, page int) {
			if page == 1 {
				fmt.Fprint(w, envelope("data", []string{"a"}, 2))
				return
			}
			w.WriteHeader(403)
		})
		got, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil)
		if !errors.Is(err, ErrForbidden) || got != nil {
			t.Fatalf("got=%v err=%v, want ErrForbidden", got, err)
		}
	})
	t.Run("malformed items", func(t *testing.T) {
		c, _ := pageServer(t, func(w http.ResponseWriter, _ int) { fmt.Fprint(w, `{"data":"nope"}`) })
		if _, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil); err == nil {
			t.Fatal("expected a decode error")
		}
	})
	t.Run("not json", func(t *testing.T) {
		c, _ := pageServer(t, func(w http.ResponseWriter, _ int) { fmt.Fprint(w, `<html>`) })
		if _, err := Paginate[item](context.Background(), c, "/v4/x", "data", nil); err == nil {
			t.Fatal("expected a decode error")
		}
	})
}
