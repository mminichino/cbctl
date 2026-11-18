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

// TestHermeticGrep fails when listed packages read process globals (environment, stdio, working
// directory) or the wall clock directly instead of through Deps / engine.Clock. Each exception is
// one package/file + needle pair with the reason it is allowed; anything else fails hard.
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
		"internal/capella",
		"internal/capellaapi",
		"internal/rest",
		"internal/redact",
	}
	forbidden := []string{"os.Getenv", "os.LookupEnv", "os.Setenv", "os.Stdin", "os.Stdout", "os.Stderr", "os.Getwd", "time.Now()", "time.Sleep("}
	type exception struct{ file, needle string }
	allow := map[exception]string{}
	allowAll := func(file, reason string) {
		for _, n := range forbidden {
			allow[exception{file, n}] = reason
		}
	}
	// Process entry: the only place the real environment and stdio are wired into Deps.
	allowAll("internal/cli/deps.go", "process entry builds Deps from the real process")
	allowAll("internal/cli/run.go", "process entry")
	allowAll("internal/cli/root.go", "process entry")
	allowAll("internal/settings/osenv.go", "settings.ProcessEnv / ApplyEnv / ProcessGetwd adapter")
	allow[exception{"internal/capella/random.go", "time.Now()"}] = "seeds the random-name generator; not a wait"
	allow[exception{"internal/capellaapi/client.go", "time.Now()"}] = "seeds retry jitter and times requests for debug logs; waits use the injected clock"

	used := map[exception]bool{}
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
			body, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, needle := range forbidden {
				if !strings.Contains(text, needle) {
					continue
				}
				key := exception{pkg + "/" + name, needle}
				if _, ok := allow[key]; ok {
					used[key] = true
					continue
				}
				t.Errorf("%s/%s contains %s (use Deps / engine.Clock, or add a file+needle exception with a reason)", pkg, name, needle)
			}
		}
	}
	// A stale single-needle exception would silently allow a future regression in that file.
	for key, reason := range allow {
		if !used[key] && !strings.HasPrefix(reason, "process entry") && !strings.HasPrefix(reason, "settings.") {
			t.Errorf("hermetic exception %s %q is no longer needed (%s); remove it", key.file, key.needle, reason)
		}
	}
}
