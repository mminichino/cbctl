package capellaapply_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/manifest/capellaapply"
)

const smallBody = `  provider: aws
  region: us-east-1
  size: small
  buckets:
    - name: orders
      memoryMb: 256
      replicas: 1
      scopes:
        - name: billing
          collections:
            - name: invoices
  credentials:
    - name: app
      password: ${APP_PW}
      access:
        privileges: read+write
        bucket: orders
    - name: gen
      access:
        privileges: read
        allBuckets: true
`

func stepStatuses(rep *manifest.Report) map[string]string {
	out := map[string]string{}
	for _, s := range rep.Steps {
		out[s.Name+" "+s.Target] = s.Status
	}
	return out
}

func TestApplyCreatesFromScratchAndConverges(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"size small", smallBody},
		{"size dev", "  provider: aws\n  region: us-east-1\n  size: dev\n"},
		{"service groups", `  provider: aws
  region: us-east-1
  serviceGroups:
    - services: [data]
      nodes: 3
      compute: { cpu: 4, ramGb: 16 }
    - services: [index, query]
      nodes: 2
      compute: { cpu: 4, ramGb: 16 }
`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			y := manifestYAML("c1", tc.body)
			first := h.run(y)
			if first.err != nil {
				t.Fatalf("first apply: %v\n%s", first.err, first.out.text())
			}
			if h.f.Count("POST", `/clusters$`) != 1 {
				t.Fatalf("expected one cluster create: %v", h.f.Writes())
			}
			if first.rep.ConnectionString == "" {
				t.Fatalf("connection string missing: %+v", first.rep)
			}
			h.f.ResetLog()
			second := h.run(y)
			if second.err != nil {
				t.Fatalf("second apply: %v\n%s", second.err, second.out.text())
			}
			if n := h.writes(); n != 0 {
				t.Fatalf("re-apply made %d writes: %v", n, h.f.Writes())
			}
			for k, s := range stepStatuses(second.rep) {
				if s != manifest.StepUnchanged {
					t.Errorf("step %q is %s, want unchanged", k, s)
				}
			}
		})
	}
}

func TestApplyScaleOut(t *testing.T) {
	h := newHarness(t)
	groups := func(n string) string {
		return "  provider: aws\n  region: us-east-1\n  serviceGroups:\n    - services: [data, index, query]\n      nodes: " + n + "\n      compute: { cpu: 4, ramGb: 16 }\n"
	}
	if r := h.run(manifestYAML("c1", groups("3"))); r.err != nil {
		t.Fatalf("create: %v", r.err)
	}
	h.f.ResetLog()
	r := h.run(manifestYAML("c1", groups("4")))
	if r.err != nil {
		t.Fatalf("scale out: %v\n%s", r.err, r.out.text())
	}
	if h.f.Count("PUT", `/clusters/[^/]+$`) != 1 {
		t.Fatalf("expected one cluster update: %v", h.f.Writes())
	}
	cl := clusterNamed(t, h, "c1")
	if got := cl.ServiceGroups[0].NumOfNodes; got != 4 {
		t.Fatalf("nodes = %d, want 4", got)
	}
}

func TestApplyScaleInNeedsYesWithoutTerminal(t *testing.T) {
	h := newHarness(t)
	groups := func(n string) string {
		return "  provider: aws\n  region: us-east-1\n  serviceGroups:\n    - services: [data, index, query]\n      nodes: " + n + "\n      compute: { cpu: 4, ramGb: 16 }\n"
	}
	if r := h.run(manifestYAML("c1", groups("4"))); r.err != nil {
		t.Fatalf("create: %v", r.err)
	}
	h.f.ResetLog()
	r := h.run(manifestYAML("c1", groups("3")))
	var be *capellaapply.BlockedError
	if !errors.As(r.err, &be) {
		t.Fatalf("scale in without -y: err = %v, want BlockedError", r.err)
	}
	if h.writes() != 0 {
		t.Fatalf("blocked apply wrote: %v", h.f.Writes())
	}
	r = h.run(manifestYAML("c1", groups("3")), func(e *capellaapply.Env) { e.Yes = true })
	if r.err != nil {
		t.Fatalf("scale in with -y: %v", r.err)
	}
	if got := clusterNamed(t, h, "c1").ServiceGroups[0].NumOfNodes; got != 3 {
		t.Fatalf("nodes = %d, want 3", got)
	}
}

func TestApplyServiceGroupRemovalBlocked(t *testing.T) {
	h := newHarness(t)
	two := `  provider: aws
  region: us-east-1
  serviceGroups:
    - services: [data]
      nodes: 3
      compute: { cpu: 4, ramGb: 16 }
    - services: [index, query]
      nodes: 2
      compute: { cpu: 4, ramGb: 16 }
`
	one := `  provider: aws
  region: us-east-1
  serviceGroups:
    - services: [data]
      nodes: 3
      compute: { cpu: 4, ramGb: 16 }
`
	if r := h.run(manifestYAML("c1", two)); r.err != nil {
		t.Fatalf("create: %v", r.err)
	}
	h.f.ResetLog()
	r := h.run(manifestYAML("c1", one), func(e *capellaapply.Env) { e.Yes = true })
	var be *capellaapply.BlockedError
	if !errors.As(r.err, &be) {
		t.Fatalf("err = %v, want BlockedError", r.err)
	}
	if h.writes() != 0 {
		t.Fatalf("blocked apply wrote: %v", h.f.Writes())
	}
}

func TestApplyImmutableChangeIsInvalid(t *testing.T) {
	const created = "  provider: aws\n  region: us-east-1\n  size: small\n  cidr: 10.20.0.0/24\n  availability: multi\n  serverVersion: \"7.6\"\n"
	tests := []struct {
		name    string
		changed string
		want    string
	}{
		{"region", strings.Replace(created, "us-east-1", "us-west-2", 1), "/spec/region"},
		{"cidr", strings.Replace(created, "10.20.0.0/24", "10.99.0.0/24", 1), "/spec/cidr"},
		{"availability", strings.Replace(created, "availability: multi", "availability: single", 1), "/spec/availability"},
		{"server version release line", strings.Replace(created, `"7.6"`, `"7.2"`, 1), "/spec/serverVersion"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if r := h.run(manifestYAML("c1", created)); r.err != nil {
				t.Fatalf("create: %v\n%s", r.err, r.out.text())
			}
			h.f.ResetLog()
			r := h.run(manifestYAML("c1", tc.changed))
			var verr *manifest.ValidationError
			if !errors.As(r.err, &verr) {
				t.Fatalf("err = %v, want ValidationError", r.err)
			}
			if !strings.Contains(issuePointers(verr), tc.want) {
				t.Fatalf("issues do not point at %s: %s", tc.want, issuePointers(verr))
			}
			if h.writes() != 0 {
				t.Fatalf("invalid apply wrote: %v", h.f.Writes())
			}
		})
	}
}

func TestApplyProviderChangeIsInvalid(t *testing.T) {
	h := newHarness(t)
	if r := h.run(manifestYAML("c1", "  provider: aws\n  region: us-east-1\n  size: small\n")); r.err != nil {
		t.Fatalf("create: %v", r.err)
	}
	h.f.ResetLog()
	r := h.run(manifestYAML("c1", "  provider: gcp\n  region: us-east1\n  size: small\n"))
	var verr *manifest.ValidationError
	if !errors.As(r.err, &verr) || !strings.Contains(issuePointers(verr), "/spec/provider") {
		t.Fatalf("err = %v, want a /spec/provider issue", r.err)
	}
	if h.writes() != 0 {
		t.Fatalf("invalid apply wrote: %v", h.f.Writes())
	}
}

func issuePointers(v *manifest.ValidationError) string {
	var b strings.Builder
	for _, i := range v.Issues {
		b.WriteString(i.Pointer + "\n")
	}
	return b.String()
}

func TestApplyKeepsRemoteSettingsWhenAbsent(t *testing.T) {
	h := newHarness(t)
	if r := h.run(manifestYAML("c1", "  provider: aws\n  region: us-east-1\n  size: small\n  deletionProtection: true\n  description: from manifest\n")); r.err != nil {
		t.Fatalf("create: %v", r.err)
	}
	if !protected(clusterNamed(t, h, "c1")) {
		t.Skip("fake does not report deletion protection")
	}
	h.f.ResetLog()
	r := h.run(manifestYAML("c1", "  provider: aws\n  region: us-east-1\n  size: small\n"))
	if r.err != nil {
		t.Fatalf("re-apply: %v\n%s", r.err, r.out.text())
	}
	if n := h.writes(); n != 0 {
		t.Fatalf("absent optional fields changed Capella: %v", h.f.Writes())
	}
	cl := clusterNamed(t, h, "c1")
	if !protected(cl) {
		t.Fatal("deletion protection was turned off")
	}
	if cl.Description != "from manifest" {
		t.Fatalf("description = %q, want it kept", cl.Description)
	}
}

func TestApplyProtectionOffNeedsYes(t *testing.T) {
	h := newHarness(t)
	body := func(v string) string {
		return "  provider: aws\n  region: us-east-1\n  size: small\n  deletionProtection: " + v + "\n"
	}
	if r := h.run(manifestYAML("c1", body("true"))); r.err != nil {
		t.Fatalf("create: %v", r.err)
	}
	h.f.ResetLog()
	r := h.run(manifestYAML("c1", body("false")))
	var be *capellaapply.BlockedError
	if !errors.As(r.err, &be) {
		t.Fatalf("err = %v, want BlockedError", r.err)
	}
	if h.writes() != 0 {
		t.Fatalf("blocked apply wrote: %v", h.f.Writes())
	}
	if r := h.run(manifestYAML("c1", body("false")), func(e *capellaapply.Env) { e.Yes = true }); r.err != nil {
		t.Fatalf("with -y: %v", r.err)
	}
	if protected(clusterNamed(t, h, "c1")) {
		t.Fatal("deletion protection still on")
	}
}

func TestApplyGeneratedCredentialAndNoRotation(t *testing.T) {
	h := newHarness(t)
	first := h.run(manifestYAML("c1", smallBody))
	if first.err != nil {
		t.Fatalf("create: %v\n%s", first.err, first.out.text())
	}
	if len(first.rep.Passwords) != 1 || first.rep.Passwords[0].Credential != "gen" || first.rep.Passwords[0].Password == "" {
		t.Fatalf("generated passwords = %+v, want exactly the credential gen", first.rep.Passwords)
	}
	pw := first.rep.Passwords[0].Password
	assertNoSecret(t, first, "Passw0rd!Passw0rd!")
	assertSaved0600(t, h.dir, pw)

	h.env["APP_PW"] = "Different!Passw0rd!2"
	h.f.ResetLog()
	second := h.run(manifestYAML("c1", smallBody))
	if second.err != nil {
		t.Fatalf("re-apply: %v", second.err)
	}
	if len(second.rep.Passwords) != 0 {
		t.Fatalf("re-apply printed passwords: %+v", second.rep.Passwords)
	}
	if n := h.writes(); n != 0 {
		t.Fatalf("re-apply with a changed password wrote: %v", h.f.Writes())
	}
	if !strings.Contains(second.out.text(), "[info] credential app exists; password not changed") {
		t.Fatalf("missing D3 line:\n%s", second.out.text())
	}
	assertNoSecret(t, second, "Different!Passw0rd!2")
}

func assertNoSecret(t *testing.T, r result, secret string) {
	t.Helper()
	if strings.Contains(r.out.text(), secret) {
		t.Fatalf("output contains the password %q", secret)
	}
	if r.rep != nil {
		for _, s := range r.rep.Steps {
			if strings.Contains(s.Detail+s.Error+s.Target, secret) {
				t.Fatalf("step %+v contains the password", s)
			}
		}
	}
}

// assertSaved0600 finds the generated password in a file below dir and checks its mode.
func assertSaved0600(t *testing.T, dir, secret string) {
	t.Helper()
	found := false
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(b), secret) {
			found = true
			// Windows has no permission bits: Go reports 0666 for every writable file.
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("%s has mode %v, want 0600", p, info.Mode().Perm())
			}
		}
		return nil
	})
	if !found {
		t.Fatalf("generated password was not saved under %s", dir)
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	h := newHarness(t)
	r := h.run(manifestYAML("c1", smallBody), func(e *capellaapply.Env) { e.DryRun = true })
	if r.err != nil {
		t.Fatalf("dry run: %v", r.err)
	}
	if n := h.writes(); n != 0 {
		t.Fatalf("dry run wrote: %v", h.f.Writes())
	}
	planned := 0
	for _, s := range r.rep.Steps {
		if s.Status == manifest.StepPlanned {
			planned++
		}
		if s.Status == manifest.StepDone {
			t.Errorf("dry run has a done step: %+v", s)
		}
	}
	if planned == 0 {
		t.Fatalf("dry run planned nothing: %+v", r.rep.Steps)
	}
}

func TestApplyNeverTouchesCapellaYAML(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.dir, "capella.yaml")
	body := "apiVersion: cbctl/v1\nprojects:\n  - name: other\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if r := h.run(manifestYAML("c1", smallBody)); r.err != nil {
			t.Fatalf("apply %d: %v", i, r.err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != body {
		t.Fatalf("capella.yaml changed: %q, %v", got, err)
	}
	st, _ := os.Stat(path)
	if !st.ModTime().Equal(old) {
		t.Fatalf("capella.yaml mtime changed: %v", st.ModTime())
	}
	entries, _ := os.ReadDir(h.dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			t.Fatalf("unexpected backup file %s", e.Name())
		}
	}
}

func TestApplyImportWithCredential(t *testing.T) {
	sdk := &fakeSDK{empty: true}
	h := newHarness(t)
	h.connect = sdk.connector()
	body := `  provider: aws
  region: us-east-1
  size: small
  buckets:
    - name: orders
      scopes:
        - name: billing
          collections:
            - name: invoices
      import:
        - scope: billing
          collection: invoices
          credential: app
          keyField: id
          data:
            - { id: a, v: 1 }
            - { id: b, v: 2 }
  credentials:
    - name: app
      password: ${APP_PW}
      access:
        privileges: read+write
        bucket: orders
`
	r := h.run(manifestYAML("c1", body))
	if r.err != nil {
		t.Fatalf("apply: %v\n%s", r.err, r.out.text())
	}
	if sdk.docs["orders.billing.invoices#id"] != 2 {
		t.Fatalf("imported = %v", sdk.docs)
	}
	if len(sdk.users) == 0 || sdk.users[0] != "app" {
		t.Fatalf("import connected as %v, want app", sdk.users)
	}
	if st := stepStatuses(r.rep)["import orders.billing.invoices"]; st != manifest.StepDone {
		t.Fatalf("import step = %q: %+v", st, r.rep.Steps)
	}

	// A second run finds the collection non-empty and skips.
	sdk.empty = false
	r = h.run(manifestYAML("c1", body))
	if r.err != nil {
		t.Fatalf("re-apply: %v", r.err)
	}
	if sdk.docs["orders.billing.invoices#id"] != 2 {
		t.Fatalf("second run imported again: %v", sdk.docs)
	}
}

func TestApplyImportFailureAddsReachabilityHint(t *testing.T) {
	h := newHarness(t)
	h.connect = func(context.Context, string, string, string) (capellaapply.DataModel, func(), error) {
		return nil, nil, errors.New("timeout")
	}
	body := `  provider: aws
  region: us-east-1
  size: small
  buckets:
    - name: orders
      scopes:
        - name: s
          collections:
            - name: c
      import:
        - scope: s
          collection: c
          credential: app
          data: [{ a: 1 }]
  credentials:
    - name: app
      password: ${APP_PW}
      access:
        privileges: read+write
        bucket: orders
`
	r := h.run(manifestYAML("c1", body))
	if r.err == nil || !strings.Contains(r.err.Error(), "spec.allowedCidrs") {
		t.Fatalf("err = %v, want the allowedCidrs hint", r.err)
	}
	if r.rep.Status != manifest.ReportFailed {
		t.Fatalf("status = %q", r.rep.Status)
	}
}

func TestApplyInterruptedBeforeExecute(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := h.runCtx(ctx, manifestYAML("c1", smallBody))
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", r.err)
	}
	if h.writes() != 0 {
		t.Fatalf("cancelled apply wrote: %v", h.f.Writes())
	}
}

func TestApplyWaitDisabledReturnsWithoutHealthy(t *testing.T) {
	h := newHarness(t)
	body := "  provider: aws\n  region: us-east-1\n  size: small\n  wait:\n    enabled: false\n"
	r := h.run(manifestYAML("c1", body))
	if r.err != nil {
		t.Fatalf("apply: %v\n%s", r.err, r.out.text())
	}
	if h.f.Count("POST", `/clusters$`) != 1 {
		t.Fatalf("cluster not created: %v", h.f.Writes())
	}
}

func clusterNamed(t *testing.T, h *harness, name string) *capellaapi.Cluster {
	t.Helper()
	c := h.f.Client()
	projects, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		cls, err := c.ListClusters(context.Background(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := range cls {
			if cls[i].Name == name {
				full, err := c.GetCluster(context.Background(), p.ID, cls[i].ID)
				if err != nil {
					t.Fatal(err)
				}
				return full
			}
		}
	}
	t.Fatalf("cluster %q not found", name)
	return nil
}

func protected(c *capellaapi.Cluster) bool {
	return c.DeletionProtection != nil && *c.DeletionProtection
}

func TestApplyConfirmOnTerminal(t *testing.T) {
	tests := []struct {
		name       string
		answer     bool
		wantWrites bool
	}{
		{"yes", true, true},
		{"no", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			asked := 0
			r := h.run(manifestYAML("c1", smallBody), func(e *capellaapply.Env) {
				e.Confirm = func(string) (bool, error) { asked++; return tc.answer, nil }
			})
			if r.err != nil {
				t.Fatalf("apply: %v", r.err)
			}
			if asked != 1 {
				t.Fatalf("asked %d times, want 1", asked)
			}
			if got := h.writes() > 0; got != tc.wantWrites {
				t.Fatalf("writes = %v, want %v", got, tc.wantWrites)
			}
			if !tc.answer {
				for _, s := range r.rep.Steps {
					if s.Status != manifest.StepSkipped {
						t.Errorf("declined apply has step %+v", s)
					}
				}
			}
		})
	}
}

func TestApplyYesSkipsConfirm(t *testing.T) {
	h := newHarness(t)
	r := h.run(manifestYAML("c1", smallBody), func(e *capellaapply.Env) {
		e.Yes = true
		e.Confirm = func(string) (bool, error) { t.Error("asked despite -y"); return false, nil }
	})
	if r.err != nil {
		t.Fatalf("apply: %v", r.err)
	}
}
