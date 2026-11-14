package secretscan_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/ci/secretscan"
)

const (
	testKey = "sandbox-key-0123456789abcdef"
	testPW  = "Gen3rated!pw/+&"
)

func TestVariants(t *testing.T) {
	got := secretscan.Variants(testPW)
	want := map[string]bool{
		testPW:                  true,
		jsonEscape(testPW):      true,
		url.PathEscape(testPW):  true,
		url.QueryEscape(testPW): true,
	}
	if len(got) != len(want) {
		t.Fatalf("Variants = %v, want %d unique forms", got, len(want))
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("unexpected variant %q", v)
		}
	}
}

func TestSecretsInDir(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, "it-tmp", "test_x0", ".cbctl", "credentials")
	if err := os.MkdirAll(creds, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "ci-01.ci_app.json"
	raw, _ := json.Marshal(map[string]string{"name": "ci_app", "password": testPW})
	if err := os.WriteFile(filepath.Join(creds, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(creds, "broken.json"), []byte("{"), 0o600)
	_ = os.WriteFile(filepath.Join(creds, "nopw.json"), []byte(`{"name":"x"}`), 0o600)

	got := secretscan.SecretsInDir(dir)
	if len(got) != 1 || got["password in "+name] != testPW {
		t.Fatalf("SecretsInDir = %v", got)
	}
}

func TestScanClean(t *testing.T) {
	texts := map[string]string{
		"integration.log": "DEBUG: GET /v4/organizations/o/projects 200 5ms\nAuthorization: Bearer ***\n",
	}
	secrets := map[string]string{"$IT_KEY": testKey, "password in ci-01.ci_app.json": testPW}
	if hits := secretscan.Scan(texts, secrets); len(hits) != 0 {
		t.Fatalf("clean log hits = %v", hits)
	}
}

func TestScanLeak(t *testing.T) {
	base := "DEBUG: GET /v4/organizations/o/projects 200 5ms\nAuthorization: Bearer ***\n"
	cases := []struct {
		leak  string
		label string
	}{
		{testKey, "$IT_KEY"},
		{testPW, "password in ci-01.ci_app.json"},
		{jsonEscape(testPW), "password in ci-01.ci_app.json"},
		{"Gen3rated%21pw%2F%2B%26", "password in ci-01.ci_app.json"},
	}
	for _, tc := range cases {
		t.Run(tc.label+"/"+tc.leak[:min(8, len(tc.leak))], func(t *testing.T) {
			texts := map[string]string{
				"integration.log": base + "request body: {'password': '" + tc.leak + "'}\n",
			}
			secrets := map[string]string{
				"$IT_KEY":                       testKey,
				"password in ci-01.ci_app.json": testPW,
			}
			hits := secretscan.Scan(texts, secrets)
			if len(hits) == 0 {
				t.Fatal("expected a hit")
			}
			found := false
			for _, h := range hits {
				if h.File == "integration.log" && h.Line == 3 && strings.Contains(h.Source, tc.label) {
					found = true
				}
				if strings.Contains(h.Source, testKey) || strings.Contains(h.Source, testPW) {
					t.Fatalf("hit source leaked a secret: %+v", h)
				}
			}
			if !found {
				t.Fatalf("hits = %v, want line 3 containing %q", hits, tc.label)
			}
		})
	}
}

func TestMinLength(t *testing.T) {
	if secretscan.MinLength != 8 {
		t.Fatalf("MinLength = %d", secretscan.MinLength)
	}
}

func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
}
