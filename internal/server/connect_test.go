package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/config"
)

func TestBuildConnString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"plain", config.Config{Hostname: "10.0.0.1"}, "couchbase://10.0.0.1"},
		{"trimmed", config.Config{Hostname: "  db.example.com \n"}, "couchbase://db.example.com"},
		{"tls", config.Config{Hostname: "db", SSL: true}, "couchbases://db"},
		{"network", config.Config{Hostname: "db", Network: "external"}, "couchbase://db?network=external"},
		{"tls network", config.Config{Hostname: "db", SSL: true, Network: "default"}, "couchbases://db?network=default"},
		{"scheme kept", config.Config{Hostname: "couchbases://db", SSL: false}, "couchbases://db"},
		{"scheme adds network", config.Config{Hostname: "couchbase://db", Network: "external"}, "couchbase://db?network=external"},
		{"scheme appends network", config.Config{Hostname: "couchbase://db?timeout=5", Network: "external"}, "couchbase://db?timeout=5&network=external"},
		{"scheme network not duplicated", config.Config{Hostname: "couchbase://db?network=default", Network: "external"}, "couchbase://db?network=default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := buildConnString(&tc.cfg); got != tc.want {
				t.Fatalf("buildConnString = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadCACertPool(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing.pem")
	if _, err := loadCACertPool(missing); err == nil || !strings.Contains(err.Error(), "read CA cert") {
		t.Fatalf("missing file: err = %v, want read CA cert error", err)
	}

	junk := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCACertPool(junk); err == nil || !strings.Contains(err.Error(), "no certificates found") {
		t.Fatalf("junk file: err = %v, want no certificates error", err)
	}

	good := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(good, selfSignedPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := loadCACertPool(good)
	if err != nil {
		t.Fatalf("valid cert: %v", err)
	}
	if pool == nil {
		t.Fatal("valid cert: nil pool")
	}
}

func selfSignedPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cbctl test CA"},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Unix(0, 0).Add(100 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// Every SDK-backed operation must refuse cleanly, not panic, before Connect succeeds.
func TestServer_NotConnected(t *testing.T) {
	t.Parallel()
	var nilServer *Server
	if err := nilServer.requireCluster(); err == nil {
		t.Fatal("nil server: requireCluster should fail")
	}
	s := &Server{}
	ops := map[string]func() error{
		"DropBucket": func() error { return s.DropBucket("b") },
		"IsBucket":   func() error { _, err := s.IsBucket("b"); return err },
		"ScopeExists": func() error {
			_, err := s.ScopeExists("b", "s")
			return err
		},
		"CollectionExists": func() error {
			_, err := s.CollectionExists("b", "s", "c")
			return err
		},
		"Upsert": func() error { return s.Upsert("id", map[string]any{"a": 1}) },
		"Get":    func() error { _, err := s.Get("id"); return err },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := op()
			if err == nil || !strings.Contains(err.Error(), "not connected") {
				t.Fatalf("%s: err = %v, want not connected", name, err)
			}
		})
	}
}

func TestServer_OnServerNeedsConfig(t *testing.T) {
	t.Parallel()
	s := &Server{}
	if _, err := s.CreateClusterOnServer(nil, nil); err == nil || !strings.Contains(err.Error(), "config is required") {
		t.Fatalf("CreateClusterOnServer: err = %v", err)
	}
	if _, err := s.ClusterMapOnServer(nil); err == nil || !strings.Contains(err.Error(), "config is required") {
		t.Fatalf("ClusterMapOnServer: err = %v", err)
	}
	if s.ClusterExistsOnServer(nil, nil) {
		t.Fatal("ClusterExistsOnServer without config should be false")
	}
}
