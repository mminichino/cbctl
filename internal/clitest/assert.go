package clitest

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
)

// updateGoldens rewrites GoldenText files when tests are run with -update.
var updateGoldens = flag.Bool("update", false, "rewrite clitest golden files under testdata/")

// AssertNoSecret fails when any of secrets (or their JSON-escaped / URL-encoded
// forms) appears in r.Stdout or r.Stderr.
func AssertNoSecret(t testing.TB, r Result, secrets ...string) {
	t.Helper()
	text := r.Stdout + r.Stderr
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for _, form := range secretVariants(secret) {
			if strings.Contains(text, form) {
				t.Errorf("secret value leaked into output (matched a %d-byte variant)", len(form))
			}
		}
	}
}

func secretVariants(v string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	add(v)
	if b, err := json.Marshal(v); err == nil && len(b) >= 2 {
		add(string(b[1 : len(b)-1]))
	}
	add(url.PathEscape(v))
	add(url.QueryEscape(v))
	return out
}

// AssertWrites checks that the fake's write requests (POST/PUT/PATCH/DELETE) match
// want as an exact multiset of "METHOD glob" entries. Globs use path.Match.
func AssertWrites(t testing.TB, f *capellafake.Fake, want ...string) {
	t.Helper()
	got := writeLabels(f)
	if !sameMultiset(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
}

// AssertNoWrites fails when the fake logged any write request.
func AssertNoWrites(t testing.TB, f *capellafake.Fake) {
	t.Helper()
	got := writeLabels(f)
	if len(got) > 0 {
		t.Fatalf("unexpected writes: %v", got)
	}
}

func writeLabels(f *capellafake.Fake) []string {
	var out []string
	for _, c := range f.Writes() {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func sameMultiset(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	// Prefer exact string match when want entries have no glob metacharacters;
	// otherwise match each want glob against an unused got entry.
	used := make([]bool, len(got))
	for _, w := range want {
		method, glob, ok := splitMethodGlob(w)
		if !ok {
			return false
		}
		found := false
		for i, g := range got {
			if used[i] {
				continue
			}
			gm, gp, gok := splitMethodGlob(g)
			if !gok || gm != method {
				continue
			}
			if matchPath(glob, gp) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func splitMethodGlob(s string) (method, glob string, ok bool) {
	method, glob, ok = strings.Cut(s, " ")
	method = strings.ToUpper(strings.TrimSpace(method))
	glob = strings.TrimSpace(glob)
	return method, glob, ok && method != "" && glob != ""
}

func matchPath(glob, p string) bool {
	if !strings.ContainsAny(glob, "*?[]") {
		return glob == p
	}
	ok, err := path.Match(glob, p)
	return err == nil && ok
}

// GoldenText compares got to testdata/<name>.golden. With -update the file is rewritten.
func GoldenText(t testing.TB, name, got string) {
	t.Helper()
	if name == "" {
		t.Fatal("GoldenText: empty name")
	}
	if filepath.Ext(name) == "" {
		name += ".golden"
	}
	path := filepath.Join("testdata", name)
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

// FormatWrites is a test helper that returns sorted write labels for debugging.
func FormatWrites(f *capellafake.Fake) string {
	labels := writeLabels(f)
	sort.Strings(labels)
	return fmt.Sprintf("%v", labels)
}
