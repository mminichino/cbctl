package capellaapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

const pageSize = 100

// maxPages bounds the pages followed by Paginate; a variable so tests can lower it.
var maxPages = 10000

// Paginate GETs path with page/perPage=100 and follows cursor.pages.next until it is absent. key
// is the JSON field holding the items ("data" for almost everything; "scopes" for scope lists). A
// top-level JSON array response is returned as-is. A 404 is returned as an error; callers decide
// whether it means empty.
func Paginate[T any](ctx context.Context, c *Client, path, key string, q url.Values) ([]T, error) {
	var all []T
	page := 1
	for n := 0; n < maxPages; n++ {
		qq := make(url.Values, len(q)+2)
		for k, v := range q {
			qq[k] = append([]string(nil), v...)
		}
		qq.Set("page", strconv.Itoa(page))
		qq.Set("perPage", strconv.Itoa(pageSize))

		resp, err := c.Get(ctx, path, qq, nil)
		if err != nil {
			return nil, err
		}
		body := bytes.TrimSpace(resp.Body)
		if len(body) == 0 {
			return all, nil
		}

		if body[0] == '[' {
			var items []T
			if err := json.Unmarshal(body, &items); err != nil {
				return nil, fmt.Errorf("decode %s: %w", path, err)
			}
			return append(all, items...), nil
		}

		var env struct {
			Cursor struct {
				Pages struct {
					Next int `json:"next"`
				} `json:"pages"`
			} `json:"cursor"`
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		if raw, ok := fields[key]; ok {
			var items []T
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("decode %s %q: %w", path, key, err)
			}
			all = append(all, items...)
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("decode %s cursor: %w", path, err)
		}

		next := env.Cursor.Pages.Next
		if next <= page { // absent (0) or not advancing: done, and never loops
			return all, nil
		}
		page = next
	}
	return nil, fmt.Errorf("paginate %s: stopped after %d pages without reaching the end", path, maxPages)
}
