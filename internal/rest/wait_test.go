package rest

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestWaitForNodeAPI(t *testing.T) {
	tests := []struct {
		name   string
		status int
		ready  bool
	}{
		{name: "uninitialized", status: http.StatusOK, ready: true},
		{name: "provisioned unauthorized", status: http.StatusUnauthorized, ready: true},
		{name: "server error", status: http.StatusInternalServerError, ready: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/pools" {
					http.NotFound(w, r)
					return
				}
				if tt.status == http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					_, err := w.Write([]byte(`{"pools":[]}`))
					if err != nil {
						return
					}
					return
				}
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(srv.Close)

			host, portText, err := net.SplitHostPort(srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			ep := Endpoint{Host: host, AdminPort: port}
			err = NewClient().WaitForNodeAPI(ep, 1)
			if tt.ready && err != nil {
				t.Fatal(err)
			}
			if !tt.ready && err == nil {
				t.Fatal("expected node API to be not ready")
			}
		})
	}
}
