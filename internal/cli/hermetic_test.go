package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
)

// TestRunIsHermetic checks that consecutive and concurrent Run calls with separate Deps do not
// leak flag or output state (Phase 4 §2.3).
func TestRunIsHermetic(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := func(k string) string {
		switch k {
		case "CBCTL_HOME":
			return home
		case "CAPELLA_API_KEY", "CAPELLA_ORG_ID", "CAPELLA_TOKEN":
			return ""
		default:
			return os.Getenv(k)
		}
	}
	lookup := func(k string) (string, bool) {
		v := env(k)
		if v == "" && (k == "CAPELLA_API_KEY" || k == "CAPELLA_ORG_ID" || k == "CAPELLA_TOKEN" || k == "CBCTL_HOME") {
			return v, k == "CBCTL_HOME"
		}
		return os.LookupEnv(k)
	}

	runOnce := func(args ...string) (stdout, stderr string, code int) {
		var out, errBuf bytes.Buffer
		d := cli.DefaultDeps()
		d.Env, d.LookupEnv = env, lookup
		d.Stdout, d.Stderr = &out, &errBuf
		d.Stdin = strings.NewReader("")
		d.Home = home
		d.IsTTY = func(io.Reader) bool { return false }
		d.ResetRedaction = true
		code = cli.Run(context.Background(), args, d)
		return out.String(), errBuf.String(), code
	}

	// JSON then text: the second run must not keep --output json.
	out1, _, code1 := runOnce("capella", "project", "list", "--output", "json")
	if code1 == 0 && strings.TrimSpace(out1) != "" && !strings.HasPrefix(strings.TrimSpace(out1), "[") && !strings.HasPrefix(strings.TrimSpace(out1), "{") {
		// Without credentials list fails; still assert no panic and independent second run.
		t.Logf("first list exit %d stdout %q (expected without credentials)", code1, out1)
	}
	out2, _, _ := runOnce("capella", "project", "list")
	if strings.HasPrefix(strings.TrimSpace(out2), "[") || strings.HasPrefix(strings.TrimSpace(out2), "{") {
		t.Fatalf("second run still looks like JSON:\n%s", out2)
	}

	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := []string{"--help"}
			if i%2 == 0 {
				args = []string{"capella", "guide", "topics"}
			}
			var out, errBuf bytes.Buffer
			d := cli.DefaultDeps()
			d.Env, d.LookupEnv = env, lookup
			d.Stdout, d.Stderr = &out, &errBuf
			d.Stdin = strings.NewReader("")
			d.Home = home
			d.IsTTY = func(io.Reader) bool { return false }
			d.ResetRedaction = true
			code := cli.Run(context.Background(), args, d)
			if code != 0 && !strings.Contains(errBuf.String(), "API key") && args[0] != "--help" {
				// guide topics is offline and should succeed
				if args[0] == "capella" {
					errs <- errBuf.String() + out.String()
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("parallel Run failed: %s", e)
	}
}

// TestHermeticGrep fails when listed packages introduce new direct process globals outside the
// allow-list. This is a soft gate: allow known legacy files while Phase 4 finishes the migration.
func TestHermeticGrep(t *testing.T) {
	root := filepath.Join("..", "..")
	pkgs := []string{
		"internal/cli",
		"internal/config/capellafile",
		"internal/settings",
		"internal/plan",
		"internal/resources",
		"internal/importer",
		"internal/secrets",
	}
	forbidden := []string{"os.Getenv", "os.LookupEnv", "os.Setenv", "os.Stdin", "os.Stdout", "os.Stderr", "os.Getwd", "time.Now()", "time.Sleep("}
	// Known legacy call sites still migrating onto Deps/Clock (Phase 4 §2). Expand only with review.
	allow := map[string]bool{
		"deps.go": true, "run.go": true, "root.go": true, "configure.go": true,
		"capella_globals.go": true, "capella_output.go": true, "capella_init.go": true,
		"capella_managed.go": true, "capella_sync.go": true, "capella_env.go": true,
		"capella_cluster_util.go": true, "capella_credential.go": true,
		"errors.go": true, "flags.go": true,
		"cluster.go": true, "keyspace.go": true, "keyspace_cmd.go": true,
		"warnings.go": true, "dotenv.go": true, "profiles.go": true, "settings.go": true,
		"render.go": true, "files.go": true,
	}
	for _, pkg := range pkgs {
		dir := filepath.Join(root, pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if allow[name] {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, needle := range forbidden {
				if strings.Contains(text, needle) {
					t.Errorf("%s/%s contains %s (migrate to Deps/Clock or add to hermetic allow-list)", pkg, name, needle)
				}
			}
		}
	}
}
