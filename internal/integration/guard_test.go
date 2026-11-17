//go:build integration

package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestPolicyFromEnv(t *testing.T) {
	p := PolicyFromEnv(envMap(map[string]string{
		"CBCTL_IT_REQUIRE_TARGET":  "1",
		"CBCTL_IT_ALLOWED_ORG_IDS": " a , ,B ",
		"GITHUB_ACTIONS":           "true",
	}))
	if !p.RequireTarget || !p.InCI || len(p.AllowedOrgs) != 2 || p.AllowedOrgs[1] != "B" {
		t.Fatalf("policy = %+v", p)
	}
	if p := PolicyFromEnv(envMap(map[string]string{"CBCTL_IT_REQUIRE_TARGET": "true"})); p.RequireTarget {
		t.Error("only the value 1 requires a target")
	}
}

func TestCheckOrg(t *testing.T) {
	tests := []struct {
		name   string
		policy GuardPolicy
		org    string
		want   error
	}{
		{"listed", GuardPolicy{AllowedOrgs: []string{"org-a", "org-b"}}, "org-b", nil},
		{"case and space", GuardPolicy{AllowedOrgs: []string{"ORG-A"}}, " org-a ", nil},
		{"not listed", GuardPolicy{AllowedOrgs: []string{"org-a"}}, "prod", ErrOrgNotAllowed},
		{"not listed in CI", GuardPolicy{AllowedOrgs: []string{"org-a"}, InCI: true}, "prod", ErrOrgNotAllowed},
		{"empty list in CI", GuardPolicy{InCI: true}, "org-a", ErrNoAllowlist},
		{"empty list locally", GuardPolicy{}, "anything", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.CheckOrg(tt.org)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("CheckOrg(%q) = %v, want %v", tt.org, err, tt.want)
			}
		})
	}
}

func TestValidatePrefix(t *testing.T) {
	tests := []struct {
		in    string
		inCI  bool
		want  string
		error bool
	}{
		{"", false, Prefix, false},
		{"", true, Prefix, false},
		{"cbctl-ci-", true, Prefix, false},
		{"cbctl-ci-mm", false, "cbctl-ci-mm-", false},
		{"cbctl-ci-mm-x1-", false, "cbctl-ci-mm-x1-", false},
		{"cbctl-ci-mm-", true, "", true},                         // CI only takes the default
		{"cbctl-", false, "", true},                              // shorter than the default: reaches other projects
		{"prod-", false, "", true},                               // not under cbctl-ci-
		{"cbctl-ci-MM-", false, "", true},                        // upper case
		{"cbctl-ci-a_b-", false, "", true},                       // underscore
		{"cbctl-ci--", false, "", true},                          // empty segment
		{"cbctl-ci-" + strings.Repeat("a", 32), false, "", true}, // 42 characters
	}
	for _, tt := range tests {
		got, err := ValidatePrefix(tt.in, tt.inCI)
		if tt.error {
			if !errors.Is(err, ErrBadPrefix) {
				t.Errorf("ValidatePrefix(%q, %v) err = %v, want ErrBadPrefix", tt.in, tt.inCI, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ValidatePrefix(%q, %v) = %q, %v; want %q", tt.in, tt.inCI, got, err, tt.want)
		}
	}
}

func TestSkipOrFail(t *testing.T) {
	// Run in a child test so the parent can observe the outcome without failing itself.
	outcome := func(p GuardPolicy) (skipped, failed bool) {
		ft := &fakeTB{TB: t}
		func() {
			defer func() { _ = recover() }()
			p.SkipOrFail(ft, "no target")
		}()
		return ft.skipped, ft.failed
	}
	if s, f := outcome(GuardPolicy{}); !s || f {
		t.Errorf("default: skipped=%v failed=%v, want a skip", s, f)
	}
	suiteMu.Lock()
	saved := requiredButMissing
	suiteMu.Unlock()
	t.Cleanup(func() { suiteMu.Lock(); requiredButMissing = saved; suiteMu.Unlock() })
	if s, f := outcome(GuardPolicy{RequireTarget: true}); s || !f {
		t.Errorf("required: skipped=%v failed=%v, want a failure", s, f)
	}
	suiteMu.Lock()
	got := requiredButMissing
	suiteMu.Unlock()
	if !got {
		t.Error("a required-but-missing target is not recorded for TestMain")
	}
}

// fakeTB records Skip/Fatal without stopping the real test; both end the call with a panic
// that the caller recovers, as runtime.Goexit would.
type fakeTB struct {
	testing.TB
	skipped, failed bool
}

func (f *fakeTB) Helper()               {}
func (f *fakeTB) Skip(...any)           { f.skipped = true; panic("skip") }
func (f *fakeTB) Fatalf(string, ...any) { f.failed = true; panic("fatal") }

func TestResolveArtifactPath(t *testing.T) {
	ws := t.TempDir()
	if got := resolveArtifactPath("integration.log", envMap(map[string]string{"GITHUB_WORKSPACE": ws})); got != filepath.Join(ws, "integration.log") {
		t.Errorf("workspace: %s", got)
	}
	abs := filepath.Join(ws, "x.log")
	if got := resolveArtifactPath(abs, envMap(nil)); got != abs {
		t.Errorf("absolute path changed: %s", got)
	}
	if got := resolveArtifactPath("", envMap(nil)); got != "" {
		t.Errorf("empty: %q", got)
	}
	// Without a workspace, a relative path lands at the module root, not the package directory.
	got := resolveArtifactPath("integration.log", envMap(nil))
	root, err := findModuleRoot(mustWd(t))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "integration.log") {
		t.Errorf("module root: %s, want under %s", got, root)
	}
	if _, err := findModuleRoot(string(filepath.Separator)); !errors.Is(err, ErrNoModuleRoot) {
		t.Errorf("findModuleRoot(/) err = %v", err)
	}
}

func mustWd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func TestRecordSuppliedSecret(t *testing.T) {
	tg := &Target{SecretsDir: filepath.Join(t.TempDir(), "secrets"), Run: "r1"}
	if err := RecordSuppliedSecret(tg, "app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tg.SecretsDir, "supplied-app.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	body, _ := os.ReadFile(path)
	if string(body) != "{\"password\":\"s3cret\"}\n" {
		t.Errorf("body = %q", body)
	}
	dir := tg.CredentialsDir("creds")
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("CredentialsDir %s: %v %v", dir, info, err)
	}
	if tg.RunPrefix() != Prefix+"r1-" {
		t.Errorf("RunPrefix = %q", tg.RunPrefix())
	}
}
