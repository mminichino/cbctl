package docs_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/docgen/reference"
)

// runbookSections splits docs/capella/runbook.md into its "## " sections (heading → body).
func runbookSections(t *testing.T, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	parts := regexp.MustCompile(`(?m)^## `).Split(text, -1)
	for _, p := range parts[1:] {
		heading, body, _ := strings.Cut(p, "\n")
		// A "# Part" heading ends the section too.
		if i := regexp.MustCompile(`(?m)^# `).FindStringIndex(body); i != nil {
			body = body[:i[0]]
		}
		out[heading] = body
	}
	return out
}

var reBashFence = regexp.MustCompile("(?s)```bash\n(.*?)```")

// TestDocs_CAP033_AC4_RunbookSections: the runbook has a section for every day-2 task, each with
// the cbctl command to run and a text block with the output to expect.
func TestDocs_CAP033_AC4_RunbookSections(t *testing.T) {
	text := readFile(t, filepath.Join(repoRoot(t), "docs", "capella", "runbook.md"))
	sections := runbookSections(t, text)
	// capellactl's "bucket create" / "collection create" are "bucket add" / "collection add" in cbctl.
	tasks := map[string]string{
		"Scale out": "cluster scale", "Scale in": "cluster scale", "Scale up": "cluster update",
		"Power off and on": "cluster power", "Set up a hibernation schedule": "cluster schedule set",
		"Pause and resume the schedule": "cluster schedule pause", "Add a bucket": "bucket add",
		"Add a collection": "collection add", "Add an app credential": "credential create",
		"Rotate a password": "--rotate-password", "Add an office IP": "cidr create",
		"Recover from deploymentFailed": "cluster delete", "Tear down": "destroy",
		"Change a disk type": "cluster update", "Set a cluster backup schedule": "cluster backup schedule set",
		"Set a bucket backup schedule":          "bucket backup schedule set",
		"Take a cluster backup before a change": "cluster backup create",
		"Restore a cluster from a backup":       "cluster backup restore",
		"Restore a bucket from a backup":        "bucket backup restore", "Download a bucket backup": "bucket backup export",
	}
	for heading, command := range tasks {
		t.Run(heading, func(t *testing.T) {
			body, ok := sections[heading]
			if !ok {
				t.Fatalf("no '## %s' section", heading)
			}
			var cmds strings.Builder
			for _, m := range reBashFence.FindAllStringSubmatch(body, -1) {
				cmds.WriteString(m[1])
			}
			if !strings.Contains(cmds.String(), "cbctl capella "+command) && !strings.Contains(cmds.String(), command) {
				t.Errorf("%s: no `%s` command in a bash block", heading, command)
			}
			if !strings.Contains(body, "```text\n") {
				t.Errorf("%s: no expected output (```text block)", heading)
			}
		})
	}
}

// TestCI_CAP042_AC8_RunbookCISection: the runbook's introduction points at -y, --quiet and the CI
// guide, and its command reference has a CI/CD section with the pipeline commands.
func TestCI_CAP042_AC8_RunbookCISection(t *testing.T) {
	text := readFile(t, filepath.Join(repoRoot(t), "docs", "capella", "runbook.md"))
	intro, _, _ := strings.Cut(text, "\n# Part 1")
	for _, s := range []string{"-y", "--quiet", "cbctl capella guide ci"} {
		if !strings.Contains(intro, s) {
			t.Errorf("runbook introduction lacks %q", s)
		}
	}
	section, ok := runbookSections(t, text)["CI/CD"]
	if !ok {
		t.Fatal("no CI/CD section in the command reference")
	}
	for _, s := range []string{
		"cbctl capella -y -q apply", "CBCTL_QUIET=true", "plan --detailed-exitcode", "cbctl capella guide ci",
		"no environment variable for approval", "exit 4",
	} {
		if !strings.Contains(section, s) {
			t.Errorf("CI/CD section lacks %q", s)
		}
	}
}

// resolve returns the command a cbctl line runs (global flags may precede subcommands).
func resolve(root *cobra.Command, toks []string) *cobra.Command {
	cmd := root
	for i := 0; i < len(toks); i++ {
		word := strings.SplitN(toks[i], "|", 2)[0]
		if strings.HasPrefix(word, "-") {
			if fl := lookupFlag(cmd, word); fl != nil && takesValue(fl, word) {
				i++
			}
			continue
		}
		if next := findChild(cmd, word); next != nil {
			cmd = next
		}
	}
	return cmd
}

// TestDocs_CAP018_AC7_MigrationGuideCommandsValid: every cbctl command in the scale-script
// migration guide names a real, runnable command and only its real options.
func TestDocs_CAP018_AC7_MigrationGuideCommandsValid(t *testing.T) {
	path := filepath.Join(repoRoot(t), "docs", "capella", "migrating-from-scale-script.md")
	cmds := extractCommands(t, path)
	if len(cmds) < 5 {
		t.Fatalf("found %d cbctl commands in the migration guide, want at least 5", len(cmds))
	}
	deps := cli.DefaultDeps()
	deps.Version = "doc-check"
	root := cli.NewRootCmd(deps)
	for _, c := range cmds {
		if problem := checkCommand(c); problem != "" {
			t.Errorf("line %d: %s\n    %s", c.line, problem, c.text)
			continue
		}
		toks := tokens(c.text)
		if cmd := resolve(root, toks[1:]); !cmd.Runnable() || cmd == root {
			t.Errorf("line %d: %q does not name a runnable command (resolved %q)", c.line, c.text, cmd.CommandPath())
		}
	}
}

// TestLicence_CAP044_AC1_FileIsApache20: LICENSE is the unmodified Apache License 2.0
// (apache.org LICENSE-2.0.txt, byte for byte).
func TestLicence_CAP044_AC1_FileIsApache20(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	const apacheSHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != apacheSHA256 {
		t.Fatalf("LICENSE sha256 = %s, want the Apache-2.0 text %s", got, apacheSHA256)
	}
}

// TestLicence_CAP044_AC2_NoticeAndVersionLine: NOTICE names cbctl and its copyright and credits
// capellactl under the Apache License 2.0; --version's second line states the licence.
func TestLicence_CAP044_AC2_NoticeAndVersionLine(t *testing.T) {
	notice := readFile(t, filepath.Join(repoRoot(t), "NOTICE"))
	lines := strings.Split(notice, "\n")
	if len(lines) < 2 || lines[0] != "cbctl" || lines[1] != "Copyright Couchbase, Inc. and contributors." {
		t.Errorf("NOTICE header = %q", lines[:min(2, len(lines))])
	}
	for _, s := range []string{"capellactl", "Apache License,\nVersion 2.0", "http://www.apache.org/licenses/LICENSE-2.0"} {
		if !strings.Contains(notice, s) {
			t.Errorf("NOTICE lacks %q", s)
		}
	}

	deps := cli.DefaultDeps()
	deps.Version = "1.2.3"
	root := cli.NewRootCmd(deps)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(got) != 3 || got[0] != "cbctl version 1.2.3" || got[1] != "Capella Management API v4.0" || got[2] != "Licensed under Apache-2.0 · Copyright Couchbase, Inc. and contributors" {
		t.Fatalf("--version = %q", out.String())
	}
}

// TestGate_CAP032_AC5_ReleaseWorkflowRunsTheGate: release.yml runs the release gate on the
// release commit with the workflow token and actions: read, and the release job needs it.
func TestGate_CAP032_AC5_ReleaseWorkflowRunsTheGate(t *testing.T) {
	wf := loadYAML(t, filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	jobs, _ := wf["jobs"].(map[string]any)
	gate, _ := jobs["gate"].(map[string]any)
	if gate == nil {
		t.Fatal("release.yml has no gate job")
	}
	if p, _ := gate["permissions"].(map[string]any); p["actions"] != "read" {
		t.Errorf("gate permissions = %#v, want actions: read", p)
	}
	var step map[string]any
	steps, _ := gate["steps"].([]any)
	for _, s := range steps {
		m, _ := s.(map[string]any)
		if strings.Contains(asString(m["run"]), "cbdev ci release-gate") {
			step = m
		}
	}
	if step == nil {
		t.Fatal("the gate job does not run cbdev ci release-gate")
	}
	run := asString(step["run"])
	if !strings.Contains(run, `--commit "${GITHUB_SHA}"`) && !strings.Contains(run, "--commit ${{ github.sha }}") {
		t.Errorf("release gate does not check the release commit: %s", run)
	}
	if strings.Contains(run, "--report-only") {
		t.Errorf("release gate must block (Phase 7): drop --report-only: %s", run)
	}
	if env, _ := step["env"].(map[string]any); asString(env["GH_TOKEN"]) != "${{ github.token }}" {
		t.Errorf("release gate GH_TOKEN = %v, want ${{ github.token }}", env["GH_TOKEN"])
	}
	release, _ := jobs["goreleaser"].(map[string]any)
	if !needsContains(release["needs"], "gate") {
		t.Errorf("goreleaser needs = %v, want gate", release["needs"])
	}
}

// TestIntegration_CAP032_RehearsedOnEveryPush: CI and make check run the integration suite
// against the fake, so a broken integration test is caught before the nightly.
func TestIntegration_CAP032_RehearsedOnEveryPush(t *testing.T) {
	root := repoRoot(t)
	wf := loadYAML(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	jobs, _ := wf["jobs"].(map[string]any)
	found := false
	for _, j := range jobs {
		job, _ := j.(map[string]any)
		if strings.Contains(stepRuns(job), "make test-integration-fake") {
			found = true
		}
	}
	if !found {
		t.Error("no ci.yml job runs make test-integration-fake")
	}
	mk := readFile(t, filepath.Join(root, "Makefile"))
	target := regexp.MustCompile(`(?ms)^test-integration-fake:\n(.*?)\n\S`).FindStringSubmatch(mk)
	if target == nil || !strings.Contains(target[1], "CBCTL_IT_TARGET=fake") || !strings.Contains(target[1], "-tags=integration") {
		t.Errorf("test-integration-fake must run the integration tag with CBCTL_IT_TARGET=fake: %v", target)
	}
	check := regexp.MustCompile(`(?m)^check:(.*)$`).FindStringSubmatch(mk)
	if check == nil || !strings.Contains(" "+check[1]+" ", " test-integration-fake ") {
		t.Error("make check must run test-integration-fake")
	}
}

// TestDocs_CAP033_AC3_ReferenceUpToDate: docs/capella/reference.md is exactly what the config
// schema generates, documents the key fields, and make check runs docs-check.
func TestDocs_CAP033_AC3_ReferenceUpToDate(t *testing.T) {
	root := repoRoot(t)
	if err := reference.Write(root, true); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, filepath.Join(root, "docs", "capella", "reference.md"))
	for _, field := range []string{
		"`deletionProtection`", "`node.compute.cpu`", "`hibernation.weekdays`", "`memoryAllocationInMb`",
		"**immutable**", "deletionProtection (Update Deletion Protection)",
	} {
		if !strings.Contains(text, field) {
			t.Errorf("reference.md lacks %s", field)
		}
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	mk := string(makefile)
	check := regexp.MustCompile(`(?m)^check:(.*)$`).FindStringSubmatch(mk)
	if !strings.Contains(mk, "\ndocs:") || !strings.Contains(mk, "docs reference --check") ||
		check == nil || !strings.Contains(check[1], "docs-check") {
		t.Error("the Makefile must have docs and docs-check targets (with docs reference --check), and check must run docs-check")
	}
}
