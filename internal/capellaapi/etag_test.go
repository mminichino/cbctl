package capellaapi

import (
	"net/http"
	"testing"
)

func TestResponseETag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header string
		set    bool
		want   string
	}{
		{"quoted version prefix", `"Version: 12"`, true, "12"},
		{"bare version prefix", `Version: 12`, true, "12"},
		{"quoted number", `"12"`, true, "12"},
		{"bare number", `12`, true, "12"},
		{"weak validator", `W/"Version: 7"`, true, "7"},
		{"lower case prefix and spaces", `  "version:   3 " `, true, "3"},
		{"missing", "", false, ""},
		{"empty", "", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tt.set {
				h.Set("ETag", tt.header)
			}
			if got := (&Response{Header: h}).ETag(); got != tt.want {
				t.Errorf("ETag() = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("nil response", func(t *testing.T) {
		t.Parallel()
		var r *Response
		if got := r.ETag(); got != "" {
			t.Errorf("ETag() on nil = %q", got)
		}
	})
}
