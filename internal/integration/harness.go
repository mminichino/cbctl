//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/settings"
)

// debugRequestLine is the contract between capellaapi debug logs and Result.Requests
// (Phase 1 §2.2 / Phase 4 §5.2): `[debug] - METHOD /v4/... -> status (Nms)`.
// Optional trailing ` body=…` is allowed (mutating requests log a redacted body).
var debugRequestLine = regexp.MustCompile(`(?m)^\[debug\] - (GET|POST|PUT|PATCH|DELETE) (/v4/\S+) -> (\d{3}) \((\d+)ms\)`)

var writeMethods = map[string]bool{
	"POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// RequestLine is one Capella API call parsed from debug log output.
type RequestLine struct {
	Method string
	Path   string
	Status int
	Ms     int
}

// Result is the outcome of one Target.CLI invocation.
type Result struct {
	Code   int
	Stdout string
	Stderr string
	Output string // stdout+stderr as seen by Requests()
}

// Requests returns every Capella debug request line in the combined output.
func (r Result) Requests() []RequestLine {
	matches := debugRequestLine.FindAllStringSubmatch(r.Output, -1)
	out := make([]RequestLine, 0, len(matches))
	for _, m := range matches {
		status, _ := strconv.Atoi(m[3])
		ms, _ := strconv.Atoi(m[4])
		out = append(out, RequestLine{Method: m[1], Path: m[2], Status: status, Ms: ms})
	}
	return out
}

// Writes returns the mutating requests (POST/PUT/PATCH/DELETE).
func (r Result) Writes() []RequestLine {
	var out []RequestLine
	for _, req := range r.Requests() {
		if writeMethods[req.Method] {
			out = append(out, req)
		}
	}
	return out
}

type cliOpts struct {
	config    string
	noApprove bool
	debug     bool
	env       map[string]string
	cwd       string
}

// CLIOption customizes Target.CLI.
type CLIOption func(*cliOpts)

// WithConfig passes -c PATH.
func WithConfig(path string) CLIOption {
	return func(o *cliOpts) { o.config = path }
}

// NoApprove omits --yes (for read-only commands that must not auto-approve).
func NoApprove() CLIOption {
	return func(o *cliOpts) { o.noApprove = true }
}

// Debug controls whether --log-level debug is appended (default true).
func Debug(on bool) CLIOption {
	return func(o *cliOpts) { o.debug = on }
}

// WithEnv sets an extra environment variable for this invocation.
func WithEnv(k, v string) CLIOption {
	return func(o *cliOpts) {
		if o.env == nil {
			o.env = map[string]string{}
		}
		o.env[k] = v
	}
}

// WithCwd sets the working directory for config discovery (default: dir of WithConfig, else process cwd).
func WithCwd(dir string) CLIOption {
	return func(o *cliOpts) { o.cwd = dir }
}

// CLI runs `cbctl capella <args>` in-process against this target.
func (tg *Target) CLI(tb testing.TB, args ...string) Result {
	tb.Helper()
	return tg.CLIOpts(tb, nil, args...)
}

// CLIOpts is CLI with options (config path, approve, debug, env).
func (tg *Target) CLIOpts(tb testing.TB, opts []CLIOption, args ...string) Result {
	tb.Helper()
	r, err := tg.runCLI(opts, args...)
	if err != nil {
		tb.Fatalf("integration CLI: %v", err)
	}
	return r
}

// MustCLI runs CLI and fails the test on a non-zero exit.
func (tg *Target) MustCLI(tb testing.TB, opts []CLIOption, args ...string) Result {
	tb.Helper()
	r := tg.CLIOpts(tb, opts, args...)
	if r.Code != 0 {
		tb.Fatalf("cbctl capella %s: exit %d\n%s", strings.Join(args, " "), r.Code, r.Output)
	}
	return r
}

func (tg *Target) runCLI(opts []CLIOption, args ...string) (Result, error) {
	o := cliOpts{debug: true}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	argv := []string{"capella"}
	if o.debug {
		argv = append(argv, "--log-level", "debug")
	}
	if !o.noApprove {
		argv = append(argv, "--yes")
	}
	if o.config != "" {
		argv = append(argv, "-c", o.config)
	}
	argv = append(argv, args...)

	envMap := tg.Env()
	for k, v := range o.env {
		envMap[k] = v
	}

	cwd := o.cwd
	if cwd == "" && o.config != "" {
		cwd = filepath.Dir(o.config)
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Result{}, fmt.Errorf("getcwd: %w", err)
		}
	}

	var stdout, stderr bytes.Buffer
	d := cli.Deps{
		Env: func(k string) string {
			if v, ok := envMap[k]; ok {
				return v
			}
			return os.Getenv(k)
		},
		LookupEnv: func(k string) (string, bool) {
			if v, ok := envMap[k]; ok {
				return v, true
			}
			return os.LookupEnv(k)
		},
		Stdin:          strings.NewReader(""),
		Stdout:         &stdout,
		Stderr:         &stderr,
		IsTTY:          func(io.Reader) bool { return false },
		Cwd:            func() (string, error) { return cwd, nil },
		Home:           tg.Home,
		Keyring:        settings.NewMemKeyring(),
		Clock:          tg.Clock,
		ResetRedaction: false,
	}
	code := cli.Run(context.Background(), argv, d)
	out := stdout.String() + stderr.String()
	r := Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String(), Output: out}

	line := fmt.Sprintf("$ cbctl %s\n%s[exit %d]\n\n", strings.Join(argv, " "), redact.String(out), code)
	f, err := os.OpenFile(tg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return r, fmt.Errorf("open integration log: %w", err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return r, fmt.Errorf("write integration log: %w", err)
	}
	_ = f.Close()
	return r, nil
}

// Shared is the project every per-feature test works in: one 3-node AWS cluster + bucket.
type Shared struct {
	Cfg     string
	Project string
	Cluster string
	Bucket  string
}

// SharedProject returns (and on first use applies) the shared multi-node project.
// On fake each Target gets its own shared project; on sandbox it is created once per suite.
func SharedProject(t *testing.T, tg *Target) *Shared {
	t.Helper()
	tg.RequireClusters(t)
	if tg.Kind == "fake" {
		if tg.shared != nil {
			return tg.shared
		}
		tg.shared = applyShared(t, tg)
		return tg.shared
	}
	sharedSandboxOnce.Do(func() {
		sharedSandboxTG = tg
		suiteMu.Lock()
		sharedProjectName = tg.ProjectName("shared")
		suiteMu.Unlock()
		s, err := tryApplyShared(tg)
		if err != nil {
			sharedSandboxErr = err.Error()
			return
		}
		sharedSandbox = s
	})
	if sharedSandboxErr != "" {
		t.Fatalf("shared project: %s", sharedSandboxErr)
	}
	return sharedSandbox
}

func applyShared(t *testing.T, tg *Target) *Shared {
	t.Helper()
	s, err := tryApplyShared(tg)
	if err != nil {
		t.Fatalf("shared project: %v", err)
	}
	tg.Cleanup(t, s.Project)
	return s
}

func tryApplyShared(tg *Target) (*Shared, error) {
	dir, err := os.MkdirTemp("", "cbctl-it-shared-*")
	if err != nil {
		return nil, err
	}
	s := &Shared{
		Cfg:     filepath.Join(dir, "capella.yaml"),
		Project: tg.ProjectName("shared"),
		// Run-unique cluster name: Capella rename-detection blocks apply when another
		// project already owns a fixed name like ci-shared-01.
		Cluster: "ci-sh-" + tg.Run,
		Bucket:  "it-data",
	}
	body := MultiNodeConfig(s.Project, s.Cluster, s.Bucket, UniqueCIDR(tg.Run, 's'), os.Getenv("CAPELLA_SANDBOX_CLIENT_CIDR"))
	if err := os.WriteFile(s.Cfg, []byte(body), 0o600); err != nil {
		return nil, err
	}
	r, err := tg.runCLI([]CLIOption{WithConfig(s.Cfg)}, "apply")
	if err != nil {
		return nil, err
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("apply shared: exit %d\n%s", r.Code, r.Output)
	}
	return s, nil
}

// ImportInto writes a sync of the shared project (and optional cluster) into cfg.
func (s *Shared) ImportInto(tb testing.TB, tg *Target, cfg string, cluster bool) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		tb.Fatalf("mkdir: %v", err)
	}
	args := []string{"sync", "--project", s.Project, "--file", cfg}
	if cluster {
		args = append(args, "--cluster", s.Cluster)
	}
	r := tg.CLIOpts(tb, []CLIOption{NoApprove(), WithCwd(filepath.Dir(cfg))}, args...)
	if r.Code != 0 {
		tb.Fatalf("sync shared into %s: exit %d\n%s", cfg, r.Code, r.Output)
	}
}

// UniqueCIDR returns a run-unique RFC1918 /24 for Capella VPC CIDRs.
// purpose salts shared vs lifecycle clusters in the same run so they do not collide.
func UniqueCIDR(run string, purpose byte) string {
	var sum uint32 = 2166136261
	for i := 0; i < len(run); i++ {
		sum ^= uint32(run[i])
		sum *= 16777619
	}
	sum ^= uint32(purpose)
	sum *= 16777619
	// 10.16.0.0–10.239.255.0/24 keeps clear of very common low 10.x leftovers.
	a := 16 + int(sum%224)
	b := int((sum / 224) % 256)
	return fmt.Sprintf("10.%d.%d.0/24", a, b)
}

// SingleNodeConfig is the cheapest single-node AWS layout (AC1).
func SingleNodeConfig(project, cluster, cidr, extra string) string {
	return fmt.Sprintf(`apiVersion: cbctl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: %s
  description: cbctl nightly integration suite (CAP-032)
clusters:
  - name: %s
    description: cbctl nightly
    configurationType: singleNode
    cloudProvider: { type: aws, region: us-east-1, cidr: %s }
    availability: { type: single }
    support: { plan: basic }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 1
        services: [data, index, query]
%s`, project, cluster, cidr, extra)
}

// MultiNodeConfig is the shared 3-node AWS layout used by per-feature tests (AC2).
func MultiNodeConfig(project, cluster, bucket, vpcCIDR, clientCIDR string) string {
	cidrs := ""
	if clientCIDR != "" {
		cidrs = fmt.Sprintf("    allowedCidrs:\n      - cidr: %s\n        comment: this CI runner (CAP-032)\n", clientCIDR)
	}
	return fmt.Sprintf(`apiVersion: cbctl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: %s
  description: cbctl nightly integration suite (CAP-032)
clusters:
  - name: %s
    description: cbctl nightly, shared by the per-feature tests
    configurationType: multiNode
    cloudProvider: { type: aws, region: us-east-1, cidr: %s }
    availability: { type: multi }
    support: { plan: developer pro, timezone: PT }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 3
        services: [data, index, query]
%s    buckets:
      - name: %s
        memoryAllocationInMb: 256
`, project, cluster, vpcCIDR, cidrs, bucket)
}
