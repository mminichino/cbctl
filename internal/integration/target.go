//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/redact"
)

// Prefix is the default project-name prefix for integration runs (CAP-032).
const Prefix = "cbctl-ci-"

const (
	fakeAPIKey = "fake-it-api-key-0123456789"
	fakeOrgID  = "org-it"

	skipMsg = "needs CAPELLA_SANDBOX_API_KEY and CAPELLA_SANDBOX_ORG_ID, or CBCTL_IT_TARGET=fake"
)

// Target is one Capella backend (in-memory fake or real sandbox) for the integration suite.
type Target struct {
	Kind    string // "fake" | "sandbox"
	APIKey  string
	OrgID   string
	Fake    *capellafake.Fake // nil on sandbox
	Clock   engine.Clock      // FakeClock (fake) or RealClock
	LogPath string
	Run     string
	Timeout time.Duration // 60m; fake waits are driven by FakeClock

	Clusters   bool // CBCTL_IT_CLUSTERS effective value
	Home       string
	SecretsDir string
	Prefix     string // validated project-name prefix (CBCTL_TEST_PREFIX or Prefix)

	mu     sync.Mutex
	report []ReportEntry
	env    map[string]string
	shared *Shared // per-target shared project (fake)
}

var (
	suiteMu     sync.Mutex
	suiteKind   string
	suiteRun    string
	suiteReport []ReportEntry
	teardownBad bool

	liveClusterDeprecation sync.Once

	sandboxOnce    sync.Once
	sandboxTarget  *Target
	sandboxInitErr string // no credentials: skip (or fail when required)
	sandboxFatal   string // a guard refused the organization: always fail

	logOnce sync.Once
	logPath string

	secretsOnce sync.Once
	secretsDir  string

	sharedSandboxOnce sync.Once
	sharedSandbox     *Shared
	sharedSandboxErr  string
	sharedSandboxTG   *Target
	// sharedProjectName is recorded before the shared apply starts, so teardown finds a
	// project that apply created before it failed.
	sharedProjectName string

	requiredButMissing bool
	suiteTargetRef     *Target // the target the suite used, for SweepRun
)

// noteRequiredButMissing records that a test needed a target the environment did not give.
func noteRequiredButMissing() {
	suiteMu.Lock()
	requiredButMissing = true
	suiteMu.Unlock()
}

// SuiteRequiredButUnused reports whether CBCTL_IT_REQUIRE_TARGET=1 was set and the suite ran
// without a target (every test skipped), or a test found its target missing. TestMain then
// exits 1: a nightly whose credentials vanished must not be green.
func SuiteRequiredButUnused() bool {
	suiteMu.Lock()
	defer suiteMu.Unlock()
	return suitePolicy().RequireTarget && (suiteKind == "" || requiredButMissing)
}

// TargetFromEnv selects the Capella backend from CBCTL_IT_TARGET / sandbox env vars.
// It skips the calling test when neither fake nor sandbox credentials are available.
func TargetFromEnv(t *testing.T) *Target {
	t.Helper()
	kind := resolveKind(t)
	if kind == "" {
		suitePolicy().SkipOrFail(t, skipMsg)
	}
	prefix, err := ValidatePrefix(os.Getenv("CBCTL_TEST_PREFIX"), suitePolicy().InCI)
	if err != nil {
		t.Fatalf("%v", err)
	}
	var tg *Target
	if kind == "sandbox" {
		tg = sandboxFromEnv(t)
	} else {
		tg = fakeFromEnv(t)
	}
	tg.Prefix = prefix
	return tg
}

func resolveKind(t *testing.T) string {
	t.Helper()
	kind := strings.ToLower(strings.TrimSpace(os.Getenv("CBCTL_IT_TARGET")))
	switch kind {
	case "fake", "sandbox":
		return kind
	case "":
		if os.Getenv("CAPELLA_SANDBOX_API_KEY") != "" && os.Getenv("CAPELLA_SANDBOX_ORG_ID") != "" {
			return "sandbox"
		}
		return ""
	default:
		t.Fatalf("CBCTL_IT_TARGET=%q: want fake or sandbox", kind)
		return ""
	}
}

func fakeFromEnv(t *testing.T) *Target {
	t.Helper()
	clk := engine.NewFakeClock(time.Time{})
	fake := capellafake.New(t,
		capellafake.WithClock(clk),
		capellafake.WithOrgID(fakeOrgID),
		capellafake.WithAPIKey(fakeAPIKey),
	)
	fake.SettlePolls(0)
	redact.Register(fake.APIKey)
	home := t.TempDir()
	tg := &Target{
		Kind:       "fake",
		APIKey:     fake.APIKey,
		OrgID:      fake.OrgID,
		Fake:       fake,
		Clock:      clk,
		LogPath:    suiteLogPath(),
		Run:        runID(),
		Timeout:    60 * time.Minute,
		Clusters:   true, // always on fake
		Home:       home,
		SecretsDir: suiteSecretsDir(t),
		env: map[string]string{
			"CAPELLA_API_BASE_URL": fake.URL,
			"CAPELLA_API_KEY":      fake.APIKey,
			"CAPELLA_ORG_ID":       fake.OrgID,
			"CAPELLA_TOKEN":        "",
			"CBCTL_HOME":           home,
			"CBCTL_PROFILE":        "",
			"CBCTL_CONFIG":         "",
			"CBCTL_QUIET":          "",
			"CI":                   "1",
		},
	}
	noteSuite(tg)
	applyProcessEnv(t, tg)
	return tg
}

func sandboxFromEnv(t *testing.T) *Target {
	t.Helper()
	sandboxOnce.Do(func() {
		key := os.Getenv("CAPELLA_SANDBOX_API_KEY")
		org := os.Getenv("CAPELLA_SANDBOX_ORG_ID")
		if key == "" || org == "" {
			key = os.Getenv("CAPELLA_API_KEY")
			org = os.Getenv("CAPELLA_ORG_ID")
			if key == "" || org == "" {
				sandboxInitErr = skipMsg
				return
			}
			t.Log("using CAPELLA_API_KEY as sandbox credentials")
		}
		redact.Register(key)
		policy := suitePolicy()
		if err := policy.CheckOrg(org); err != nil {
			sandboxFatal = err.Error()
			return
		}
		if len(policy.AllowedOrgs) == 0 {
			t.Log("CBCTL_IT_ALLOWED_ORG_IDS is not set; any organization is allowed outside CI")
		}
		home, err := os.MkdirTemp("", "cbctl-it-home-*")
		if err != nil {
			sandboxInitErr = err.Error()
			return
		}
		sandboxTarget = &Target{
			Kind:       "sandbox",
			APIKey:     key,
			OrgID:      org,
			Clock:      engine.RealClock{},
			LogPath:    suiteLogPath(),
			Run:        runID(),
			Timeout:    60 * time.Minute,
			Clusters:   clustersEnabled(t),
			Home:       home,
			SecretsDir: suiteSecretsDir(t),
			env: map[string]string{
				"CAPELLA_API_KEY": key,
				"CAPELLA_ORG_ID":  org,
				"CAPELLA_TOKEN":   "",
				"CBCTL_HOME":      home,
				"CBCTL_PROFILE":   "",
				"CBCTL_CONFIG":    "",
				"CBCTL_QUIET":     "",
				"CI":              "1",
			},
		}
		if base := os.Getenv("CAPELLA_API_BASE_URL"); base != "" {
			sandboxTarget.env["CAPELLA_API_BASE_URL"] = base
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := policy.VerifyOrgReachable(ctx, sandboxTarget.Client()); err != nil {
			sandboxFatal = err.Error()
			sandboxTarget = nil
			return
		}
		noteSuite(sandboxTarget)
	})
	if sandboxFatal != "" {
		t.Fatalf("sandbox guard: %s", sandboxFatal)
	}
	if sandboxInitErr != "" {
		suitePolicy().SkipOrFail(t, sandboxInitErr)
	}
	applyProcessEnv(t, sandboxTarget)
	return sandboxTarget
}

// applyProcessEnv mirrors the Target env map into the process environment. Capella settings
// resolution still falls back to os.Getenv when Inputs.Env is unset; t.Setenv keeps each test
// hermetic the same way clitest.Scenario does.
func applyProcessEnv(t *testing.T, tg *Target) {
	t.Helper()
	for k, v := range tg.env {
		t.Setenv(k, v)
	}
}

func noteSuite(tg *Target) {
	suiteMu.Lock()
	defer suiteMu.Unlock()
	suiteKind = tg.Kind
	suiteRun = tg.Run
	suiteTargetRef = tg
}

func suiteLogPath() string {
	logOnce.Do(func() {
		if p := os.Getenv("CBCTL_IT_LOG"); p != "" {
			logPath = resolveArtifactPath(p, os.Getenv)
		} else {
			dir, err := os.MkdirTemp("", "cbctl-it-log-*")
			if err != nil {
				logPath = "integration.log"
			} else {
				logPath = dir + "/integration.log"
			}
		}
		_ = os.WriteFile(logPath, nil, 0o600)
	})
	return logPath
}

func suiteSecretsDir(t *testing.T) string {
	t.Helper()
	secretsOnce.Do(func() {
		if p := os.Getenv("CBCTL_IT_SECRETS_DIR"); p != "" {
			secretsDir = resolveArtifactPath(p, os.Getenv)
			_ = os.MkdirAll(secretsDir, 0o700)
			return
		}
		dir, err := os.MkdirTemp("", "cbctl-it-secrets-*")
		if err != nil {
			secretsDir = t.TempDir()
			return
		}
		secretsDir = dir
	})
	return secretsDir
}

func runID() string {
	if gh := os.Getenv("GITHUB_RUN_ID"); gh != "" {
		attempt := os.Getenv("GITHUB_RUN_ATTEMPT")
		if attempt == "" {
			attempt = "1"
		}
		return gh + "-" + attempt
	}
	return fmt.Sprintf("local%d", time.Now().Unix())
}

func clustersEnabled(t *testing.T) bool {
	t.Helper()
	v := os.Getenv("CBCTL_IT_CLUSTERS")
	if v == "" {
		if os.Getenv("CBCTL_LIVE_CLUSTER") == "1" {
			liveClusterDeprecation.Do(func() {
				t.Log("CBCTL_LIVE_CLUSTER=1 is deprecated; use CBCTL_IT_CLUSTERS=1")
			})
			return true
		}
		return os.Getenv("GITHUB_ACTIONS") == "true"
	}
	return v == "1"
}

// ProjectName returns a run-unique project name: <prefix><run>-<purpose>.
func (tg *Target) ProjectName(purpose string) string {
	return tg.RunPrefix() + purpose
}

// RunPrefix is the prefix of every project this run creates: <prefix><run>-. A sweep by it
// can only ever reach this run's projects.
func (tg *Target) RunPrefix() string {
	p := tg.Prefix
	if p == "" {
		p = Prefix
	}
	return p + tg.Run + "-"
}

// ErrNoModuleRoot is returned when no go.mod is found above the working directory.
var ErrNoModuleRoot = errors.New("no go.mod above the working directory")

// resolveArtifactPath makes a relative CBCTL_IT_* path absolute against $GITHUB_WORKSPACE, or
// else the module root, so "integration.log" lands where the upload step looks (go test runs
// in the package directory, not the repository root).
func resolveArtifactPath(p string, env func(string) string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	if ws := env("GITHUB_WORKSPACE"); ws != "" {
		return filepath.Join(ws, p)
	}
	if wd, err := os.Getwd(); err == nil {
		if root, err := findModuleRoot(wd); err == nil {
			return filepath.Join(root, p)
		}
	}
	return p
}

// findModuleRoot walks up from start to the directory holding go.mod.
func findModuleRoot(start string) (string, error) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoModuleRoot
		}
		dir = parent
	}
}

// suiteReportPath is CBCTL_IT_REPORT resolved like the log path ("" when unset).
func suiteReportPath() string { return resolveArtifactPath(os.Getenv("CBCTL_IT_REPORT"), os.Getenv) }

// SuiteReportPath is suiteReportPath for TestMain.
func SuiteReportPath() string { return suiteReportPath() }

// CredentialsDir returns a per-purpose directory for generated passwords under SecretsDir, so
// the secret scanner finds every password the run created.
func (tg *Target) CredentialsDir(purpose string) string {
	dir := filepath.Join(tg.SecretsDir, tg.Run+"-"+purpose)
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// RecordSuppliedSecret writes a password the test supplied (not generated) into SecretsDir as
// {"password": ...} with mode 0600, so the log scanner checks for it too.
func RecordSuppliedSecret(tg *Target, name, value string) error {
	if err := os.MkdirAll(tg.SecretsDir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"password": value})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tg.SecretsDir, "supplied-"+name+".json"), append(body, '\n'), 0o600)
}

// RequireClusters skips on sandbox when cluster-creating scenarios are disabled.
func (tg *Target) RequireClusters(t *testing.T) {
	t.Helper()
	if tg.Kind == "sandbox" && !tg.Clusters {
		t.Skip("CBCTL_IT_CLUSTERS=0")
	}
}

// Client returns a Capella API client for this target.
func (tg *Target) Client() *capellaapi.Client {
	if tg.Fake != nil {
		return tg.Fake.Client()
	}
	opts := []capellaapi.Option{
		capellaapi.WithOrgID(tg.OrgID),
		capellaapi.WithClock(tg.Clock),
	}
	if base := tg.env["CAPELLA_API_BASE_URL"]; base != "" {
		opts = append(opts, capellaapi.WithBaseURL(base))
	}
	c, err := capellaapi.New(tg.APIKey, opts...)
	if err != nil {
		panic(err)
	}
	return c
}

// Env returns a copy of the process environment overrides for CLI invocations.
func (tg *Target) Env() map[string]string {
	out := make(map[string]string, len(tg.env))
	for k, v := range tg.env {
		out[k] = v
	}
	return out
}

// appendReport records a cleanup entry for the suite report.
func (tg *Target) appendReport(e ReportEntry) {
	tg.mu.Lock()
	tg.report = append(tg.report, e)
	tg.mu.Unlock()
	suiteMu.Lock()
	suiteReport = append(suiteReport, e)
	suiteMu.Unlock()
}

// markTeardownFailed records that a teardown failed after a green test.
func markTeardownFailed() {
	suiteMu.Lock()
	teardownBad = true
	suiteMu.Unlock()
}

// SuiteTeardownFailed reports whether any teardown failed a previously green test.
func SuiteTeardownFailed() bool {
	suiteMu.Lock()
	defer suiteMu.Unlock()
	return teardownBad
}

// SuiteKindRun returns the suite target kind and run id (empty when no target was used).
func SuiteKindRun() (kind, run string) {
	suiteMu.Lock()
	defer suiteMu.Unlock()
	return suiteKind, suiteRun
}

// SuiteReportEntries returns a copy of every cleanup report entry.
func SuiteReportEntries() []ReportEntry {
	suiteMu.Lock()
	defer suiteMu.Unlock()
	out := make([]ReportEntry, len(suiteReport))
	copy(out, suiteReport)
	return out
}
