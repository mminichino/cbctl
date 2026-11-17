package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeServerREST is a minimal Couchbase Server management API on an httptest listener. When
// initialized is false every call answers 401, which the provisioning code reads as "management
// API up, cluster not initialized" (so no retry loop runs).
type fakeServerREST struct {
	*httptest.Server
	initialized bool

	mu   sync.Mutex
	hits []string // "METHOD /path"
}

func newFakeServerREST(t *testing.T, initialized bool) *fakeServerREST {
	t.Helper()
	f := &fakeServerREST{initialized: initialized}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// hostPort is the listener address, usable as --host / --ip-address / --rally-ip-address.
func (f *fakeServerREST) hostPort() string { return strings.TrimPrefix(f.URL, "http://") }

func (f *fakeServerREST) sawRequest(method, path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.hits {
		if h == method+" "+path {
			return true
		}
	}
	return false
}

func (f *fakeServerREST) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	f.mu.Lock()
	f.hits = append(f.hits, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if !f.initialized {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusOK)
		return
	}
	switch r.URL.Path {
	case "/pools":
		_, _ = io.WriteString(w, `{"pools":[{"name":"default"}]}`)
	case "/pools/default":
		_, _ = io.WriteString(w, `{"nodes":[{"otpNode":"ns_1@127.0.0.1","hostname":"`+f.hostPort()+`",`+
			`"services":["kv","index","n1ql","fts"],"version":"7.6.0"}],`+
			`"memoryQuota":2048,"indexMemoryQuota":2048,"ftsMemoryQuota":2048,"rebalanceStatus":"none"}`)
	case "/pools/default/rebalanceProgress":
		_, _ = io.WriteString(w, `{"status":"none"}`)
	case "/nodes/self":
		_, _ = io.WriteString(w, `{"memoryTotal":8589934592,"alternateAddresses":{}}`)
	default:
		_, _ = io.WriteString(w, `{}`)
	}
}

func TestServerClusterCommandsAgainstInitializedNode(t *testing.T) {
	srv := newFakeServerREST(t, true)
	hp := srv.hostPort()
	auth := []string{"-u", "Administrator", "-p", "password", "--no-ssl"}
	tests := []struct {
		name    string
		args    []string
		wantOut string
		wantReq string // "METHOD /path" that must have been sent, if any
	}{
		{"exists", []string{"cluster", "exists", "--host", hp}, "true", "GET /pools/default"},
		{"map", []string{"cluster", "map", "--host", hp}, "Cluster Host List:", "GET /pools/default"},
		{"create single node", []string{"cluster", "create", "--host", hp, "--ram", "4"}, "Cluster already configured", ""},
		{"create node spec", []string{"cluster", "create", "--node", hp + "=data,index@4"}, "Cluster already configured", ""},
		{"create alternate", []string{"cluster", "create", "--host", hp, "--external-ip-address", "203.0.113.10"},
			"Cluster already configured", ""},
		{"create provisioner", []string{"cluster", "create", "--ip-address", hp, "--name", "c1", "--ram", "4"},
			"Cluster already configured", ""},
		{"create provisioner external", []string{"cluster", "create", "--ip-address", hp,
			"--external-ip-address", "203.0.113.10", "--services", "data"}, "Cluster already configured", "GET /nodes/self"},
		{"join member", []string{"cluster", "join", "--rally-ip-address", hp, "--ip-address", hp, "--ram", "4"},
			"Node already configured", ""},
		{"add member via host", []string{"cluster", "add", "--rally-ip-address", hp, "--host", hp, "-a", "203.0.113.11;kv:9000"},
			"Node already configured", ""},
		{"join arbiter member", []string{"cluster", "join", "--rally-ip-address", hp, "--ip-address", hp, "--arbiter"},
			"Node already configured", ""},
		{"rebalance", []string{"cluster", "rebalance", "--rally-ip-address", hp}, "Cluster rebalanced", "POST /controller/rebalance"},
		{"rebalance ip alias", []string{"cluster", "rebalance", "--ip-address", hp}, "Cluster rebalanced", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, code := runCLI(t, context.Background(), append(tt.args, auth...)...)
			if code != 0 {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
			}
			if !strings.Contains(out+errOut, tt.wantOut) {
				t.Errorf("output missing %q\nstdout: %s\nstderr: %s", tt.wantOut, out, errOut)
			}
			if tt.wantReq != "" {
				method, path, _ := strings.Cut(tt.wantReq, " ")
				if !srv.sawRequest(method, path) {
					t.Errorf("request %s not sent", tt.wantReq)
				}
			}
		})
	}
}

func TestServerClusterCommandsAgainstUninitializedNode(t *testing.T) {
	srv := newFakeServerREST(t, false)
	hp := srv.hostPort()
	auth := []string{"-u", "Administrator", "-p", "password", "--no-ssl"}
	tests := []struct {
		name    string
		args    []string
		code    int
		wantOut string
	}{
		{"exists is false", []string{"cluster", "exists", "--host", hp}, 0, "false"},
		{"map fails", []string{"cluster", "map", "--host", hp}, 1, "Failed to read cluster map"},
		{"create cannot read memory", []string{"cluster", "create", "--host", hp}, 1, "Failed to create cluster"},
		{"join needs initialized rally", []string{"cluster", "join", "--rally-ip-address", hp, "--ip-address", hp}, 1,
			"not initialized"},
		{"rebalance needs initialized rally", []string{"cluster", "rebalance", "--rally-ip-address", hp}, 1, "not initialized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, code := runCLI(t, context.Background(), append(tt.args, auth...)...)
			if code != tt.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tt.code, out, errOut)
			}
			if !strings.Contains(out+errOut, tt.wantOut) {
				t.Errorf("output missing %q\nstdout: %s\nstderr: %s", tt.wantOut, out, errOut)
			}
		})
	}
}

// TestServerClusterFlagValidation covers the flag checks that run before any request is sent.
func TestServerClusterFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"create bad ram", []string{"cluster", "create", "--ram", "0"}, "--ram must be a positive"},
		{"create node with provisioner flag", []string{"cluster", "create", "--node", "10.0.0.1", "--name", "c"},
			"cannot be combined with --node"},
		{"create node with alternate", []string{"cluster", "create", "--node", "10.0.0.1", "-a", "ext.example.com"},
			"use #ALTERNATE in --node"},
		{"create bad node spec", []string{"cluster", "create", "--node", "10.0.0.1@x"}, "invalid RAM in node spec"},
		{"create no services", []string{"cluster", "create", "--services", ","}, "at least one service is required"},
		{"create provisioner no services", []string{"cluster", "create", "--ip-address", "10.0.0.1", "--services", ","},
			"at least one service is required"},
		{"create ext-api without alternate", []string{"cluster", "create", "--host", "10.0.0.1", "--ext-api"},
			"--ext-api requires an alternate address"},
		{"join bad ram", []string{"cluster", "join", "--ram", "-1"}, "--ram must be a positive"},
		{"join no rally", []string{"cluster", "join", "--ip-address", "10.0.0.2"}, "--rally-ip-address is required"},
		{"join arbiter with services", []string{"cluster", "join", "--ip-address", "10.0.0.2", "--rally-ip-address", "10.0.0.1",
			"--arbiter", "--services", "data"}, "--arbiter cannot be combined with --services"},
		{"join no services", []string{"cluster", "add", "--ip-address", "10.0.0.2", "--rally-ip-address", "10.0.0.1",
			"--services", ","}, "at least one service is required"},
		{"test bad timeout", []string{"cluster", "test", "--timeout", "0"}, "--timeout must be a positive integer"},
		{"test external and internal", []string{"cluster", "test", "--external", "--internal"}, "use either --external or --internal"},
		{"import bad keyspace", []string{"import", "docs.jsonl", "b.s"}, "bucket.scope.collection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, code := runCLI(t, context.Background(), tt.args...)
			if code == 0 {
				t.Fatalf("exit 0, want failure\nstdout: %s", out)
			}
			if !strings.Contains(errOut, tt.wantErr) {
				t.Errorf("stderr missing %q\n%s", tt.wantErr, errOut)
			}
		})
	}
}

// TestServerSDKCommandsHonorCancellation checks every SDK-backed Server command maps a cancelled
// context to exit 130 instead of a generic connect failure.
func TestServerSDKCommandsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := [][]string{
		{"bucket", "create", "b1"},
		{"scope", "create", "s1", "--bucket", "b1"},
		{"scope", "exists", "s1", "--bucket", "b1"},
		{"collection", "create", "c1", "--bucket", "b1", "--scope", "s1"},
		{"collection", "exists", "c1", "--bucket", "b1", "--scope", "s1"},
		{"cluster", "test"},
		{"cluster", "test", "--bucket", "b1", "--external"},
		{"cluster", "test", "--internal"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, errOut, code := runCLI(t, ctx, append(args, "--host", "127.0.0.1")...)
			if code != int(ExitInterrupted) {
				t.Fatalf("exit %d, want 130\n%s", code, errOut)
			}
		})
	}
}
