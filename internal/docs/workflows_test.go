package docs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestWorkflowsLint(t *testing.T) {
	root := repoRoot(t)
	wfDir := filepath.Join(root, ".github", "workflows")

	release := loadYAML(t, filepath.Join(wfDir, "release.yml"))
	jobs, _ := release["jobs"].(map[string]any)
	goreleaser, _ := jobs["goreleaser"].(map[string]any)
	needs := goreleaser["needs"]
	if !needsContains(needs, "gate") {
		t.Fatalf("release.yml goreleaser job must need gate, got %#v", needs)
	}

	for _, name := range []string{"ci.yml", "release.yml"} {
		raw := readFile(t, filepath.Join(wfDir, name))
		if strings.Contains(raw, "CAPELLA_SANDBOX_") {
			t.Fatalf("%s must not reference CAPELLA_SANDBOX_* secrets", name)
		}
	}
	ciRaw := readFile(t, filepath.Join(wfDir, "ci.yml"))
	for _, want := range []string{"make test-integration-fake", "make docs-check"} {
		if !strings.Contains(ciRaw, want) {
			t.Fatalf("ci.yml must run %s", want)
		}
	}
}

// TestNightlyWorkflow pins the sandbox safety of nightly.yml (Phase 5).
func TestNightlyWorkflow(t *testing.T) {
	wfDir := filepath.Join(repoRoot(t), ".github", "workflows")
	raw := readFile(t, filepath.Join(wfDir, "nightly.yml"))
	wf := loadYAML(t, filepath.Join(wfDir, "nightly.yml"))

	if crons := cronsOf(wf); len(crons) != 1 || crons[0] != "0 7 * * *" {
		t.Errorf("nightly.yml crons = %v, want only the nightly (the janitor is janitor.yml)", crons)
	}
	conc, _ := wf["concurrency"].(map[string]any)
	if conc["group"] != "nightly-sandbox" || conc["cancel-in-progress"] != false {
		t.Errorf("concurrency = %#v, want the constant group nightly-sandbox without cancel-in-progress", conc)
	}
	if perms, _ := wf["permissions"].(map[string]any); perms["contents"] != "read" || len(perms) != 1 {
		t.Errorf("workflow permissions = %#v, want contents: read only", perms)
	}

	jobs, _ := wf["jobs"].(map[string]any)
	dedupe, _ := jobs["dedupe"].(map[string]any)
	if dedupe == nil {
		t.Fatal("nightly.yml needs a dedupe job")
	}
	if !strings.Contains(stepRuns(dedupe), "cbdev ci dedupe") {
		t.Error("the dedupe job must run cbdev ci dedupe")
	}
	if p, _ := dedupe["permissions"].(map[string]any); p["actions"] != "read" {
		t.Errorf("dedupe permissions = %#v, want actions: read", p)
	}
	for name, j := range jobs {
		if name == "dedupe" {
			continue
		}
		job, _ := j.(map[string]any)
		if !needsContains(job["needs"], "dedupe") || !strings.Contains(asString(job["if"]), "needs.dedupe.outputs.skip != 'true'") {
			t.Errorf("job %s must need dedupe and skip when it says so", name)
		}
	}

	it, _ := jobs["integration"].(map[string]any)
	env, _ := it["env"].(map[string]any)
	wantEnv := map[string]string{
		"CAPELLA_API_KEY":          "${{ secrets.CAPELLA_SANDBOX_API_KEY }}", // D1
		"CAPELLA_ORG_ID":           "${{ vars.CAPELLA_SANDBOX_ORG_ID }}",     // D4
		"CBCTL_IT_ALLOWED_ORG_IDS": "${{ vars.CAPELLA_SANDBOX_ORG_ID }}",
		"CBCTL_IT_REQUIRE_TARGET":  "1",
		"CBCTL_IT_TARGET":          "sandbox",
	}
	for k, v := range wantEnv {
		if asString(env[k]) != v {
			t.Errorf("integration env %s = %v, want %s", k, env[k], v)
		}
	}
	for _, bad := range []string{"secrets.CAPELLA_SANDBOX_ORG_ID", "|| true", "CBCTL_IT_SECRETS_DIR: ${{ runner."} {
		if strings.Contains(raw, bad) {
			t.Errorf("nightly.yml must not contain %q", bad)
		}
	}
	runs := stepRuns(it)
	// The probe dispatch: its name marks it for dedupe, its input reaches the shell only
	// through env (no ${{ }} expansion inside run:), and the default runs the whole suite.
	if !strings.Contains(asString(wf["run-name"]), "probe: ") {
		t.Errorf("run-name = %v, want a probe dispatch named \"probe: KIND\" (dedupe.ProbeTitlePrefix)", wf["run-name"])
	}
	for _, want := range []string{`CBCTL_IT_PROBE: ${{ inputs.probe }}`, `"") go test -tags=integration -timeout 290m -v ./internal/integration/... ;;`} {
		if !strings.Contains(raw, want) {
			t.Errorf("nightly.yml must contain %q", want)
		}
	}
	if strings.Contains(runs, "inputs.") {
		t.Error("nightly.yml run: scripts must not expand inputs directly (use env)")
	}
	for _, want := range []string{
		"::error::secret CAPELLA_SANDBOX_API_KEY is empty", // preflight
		"CBCTL_IT_LOG=$GITHUB_WORKSPACE/",                  // absolute artifact paths
		"CBCTL_IT_REPORT=$GITHUB_WORKSPACE/",
		"CBCTL_IT_SECRETS_DIR=$RUNNER_TEMP/",
		`--prefix "cbctl-ci-${{ github.run_id }}-${{ github.run_attempt }}-"`, // run-only sweep
		"--require-secrets",
	} {
		if !strings.Contains(runs, want) {
			t.Errorf("integration steps must contain %q", want)
		}
	}
	steps, _ := it["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		name := asString(step["name"])
		if strings.HasPrefix(name, "Sweep") || strings.HasPrefix(name, "No secret") || strings.HasPrefix(name, "Upload") {
			if asString(step["if"]) != "always()" {
				t.Errorf("step %q must run if: always()", name)
			}
		}
		if with, _ := step["with"].(map[string]any); with != nil && with["if-no-files-found"] != nil && with["if-no-files-found"] != "error" {
			t.Errorf("step %q: if-no-files-found must be error", name)
		}
	}
}

// TestJanitorWorkflow pins janitor.yml: its own schedule and group, the D1/D4 env, the CI prefix.
func TestJanitorWorkflow(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "janitor.yml")
	wf := loadYAML(t, path)
	if crons := cronsOf(wf); len(crons) != 1 || crons[0] != "0 9 * * 0" {
		t.Errorf("janitor.yml crons = %v", crons)
	}
	conc, _ := wf["concurrency"].(map[string]any)
	if conc["group"] != "sandbox-janitor" {
		t.Errorf("janitor concurrency = %#v", conc)
	}
	jobs, _ := wf["jobs"].(map[string]any)
	job, _ := jobs["janitor"].(map[string]any)
	env, _ := job["env"].(map[string]any)
	if asString(env["CAPELLA_API_KEY"]) != "${{ secrets.CAPELLA_SANDBOX_API_KEY }}" || asString(env["CAPELLA_ORG_ID"]) != "${{ vars.CAPELLA_SANDBOX_ORG_ID }}" {
		t.Errorf("janitor env = %#v", env)
	}
	runs := stepRuns(job)
	for _, want := range []string{"--prefix cbctl-ci-", "--older-than-hours 24", "--max-total-minutes", "::error::"} {
		if !strings.Contains(runs, want) {
			t.Errorf("janitor steps must contain %q", want)
		}
	}
}

// cronsOf returns the workflow's cron strings; yaml.v3 may read the "on" key as true.
func cronsOf(wf map[string]any) []string {
	on, _ := wf["on"].(map[string]any)
	if on == nil {
		on, _ = wf["true"].(map[string]any)
	}
	var out []string
	list, _ := on["schedule"].([]any)
	for _, s := range list {
		m, _ := s.(map[string]any)
		out = append(out, asString(m["cron"]))
	}
	return out
}

// stepRuns joins every step's run script of a job.
func stepRuns(job map[string]any) string {
	var b strings.Builder
	steps, _ := job["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		b.WriteString(asString(step["run"]))
		b.WriteString("\n")
	}
	return b.String()
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func TestNOTICEMentionsCapellactl(t *testing.T) {
	raw := readFile(t, filepath.Join(repoRoot(t), "NOTICE"))
	if !strings.Contains(raw, "capellactl") {
		t.Fatal("NOTICE must mention capellactl")
	}
	if !strings.Contains(raw, "Apache-2.0") && !strings.Contains(raw, "Apache 2.0") && !strings.Contains(raw, "Apache License") {
		t.Fatal("NOTICE must mention Apache license")
	}
}

func TestLinksBacklog(t *testing.T) {
	// Imported via shell-free package boundary: call links from here would create a cycle with docs tests.
	// Keep a smoke assertion that generated indexes exist after make docs.
	root := repoRoot(t)
	for _, p := range []string{
		"docs/backlog/CAP-ID-MAP.md",
		"docs/backlog/release-1.5.0/README.md",
		"docs/backlog/release-1.0.0/API-COVERAGE.md",
		"docs/capella/reference.md",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Fatalf("missing generated file %s: %v", p, err)
		}
	}
}

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	raw := readFile(t, path)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func needsContains(needs any, want string) bool {
	switch v := needs.(type) {
	case string:
		return v == want
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}
