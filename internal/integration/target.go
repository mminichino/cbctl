//go:build integration

package integration

import (
	"fmt"
	"os"
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
	sandboxInitErr string

	logOnce sync.Once
	logPath string

	secretsOnce sync.Once
	secretsDir  string

	sharedSandboxOnce sync.Once
	sharedSandbox     *Shared
	sharedSandboxErr  string
	sharedSandboxTG   *Target
)

// TargetFromEnv selects the Capella backend from CBCTL_IT_TARGET / sandbox env vars.
// It skips the calling test when neither fake nor sandbox credentials are available.
func TargetFromEnv(t *testing.T) *Target {
	t.Helper()
	kind := resolveKind(t)
	if kind == "" {
		t.Skip(skipMsg)
	}
	if kind == "sandbox" {
		return sandboxFromEnv(t)
	}
	return fakeFromEnv(t)
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
		noteSuite(sandboxTarget)
	})
	if sandboxInitErr != "" {
		t.Skip(sandboxInitErr)
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
}

func suiteLogPath() string {
	logOnce.Do(func() {
		if p := os.Getenv("CBCTL_IT_LOG"); p != "" {
			logPath = p
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
			secretsDir = p
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
	return projectPrefix() + tg.Run + "-" + purpose
}

func projectPrefix() string {
	p := os.Getenv("CBCTL_TEST_PREFIX")
	if p == "" {
		return Prefix
	}
	if !strings.HasSuffix(p, "-") {
		p += "-"
	}
	return p
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
