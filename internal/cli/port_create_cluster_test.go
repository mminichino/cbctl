package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/cli"
	"github.com/mminichino/cbctl/internal/clitest"
)

// minimalDoc is capellactl's MINIMAL capella.yaml (with the fixture's server version).
const minimalDoc = `apiVersion: cbctl/v1
project:
  name: nova-dev
  description: Nova development environment
clusters:
  - name: nova-dev-01
    description: Nova development cluster
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.10.0.0/24 }
    couchbaseServer: { version: "8.0" }
    availability: { type: multi }
    support: { plan: developer pro, timezone: PT }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 3
        services: [data, index, query]
`

// childrenDoc adds an allow-list entry to minimalDoc's cluster.
const childrenDoc = minimalDoc + `    allowedCidrs:
      - cidr: 203.0.113.10/32
        comment: Office VPN
`

// createBodyFixture is capellactl's tests/scenario/fixtures/cluster_create_body.json.
const createBodyFixture = `{
  "name": "nova-dev-01",
  "description": "Nova development cluster",
  "configurationType": "multiNode",
  "cloudProvider": {"type": "aws", "region": "us-east-1", "cidr": "10.10.0.0/24"},
  "couchbaseServer": {"version": "8.0"},
  "serviceGroups": [
    {"node": {"compute": {"cpu": 4, "ram": 16}, "disk": {"type": "gp3", "storage": 50, "iops": 3000}},
     "numOfNodes": 3, "services": ["data", "index", "query"]}
  ],
  "availability": {"type": "multi"},
  "support": {"plan": "developer pro", "timezone": "PT"}
}`

const clustersPath = "/v4/organizations/*/projects/*/clusters"

// novaDev seeds project nova-dev as capellactl's fixtures do (with its description, so the
// project itself has no change) and returns its id.
func novaDev(s *clitest.Scenario) string {
	return s.Fake.SeedProject("nova-dev", capellafake.WithField("description", "Nova development environment"))
}

func TestCluster_CAP011_AC1_CreateBodyMatchesFixture(t *testing.T) {
	s := newScenario(t)
	novaDev(s)
	writeCapellaYAML(t, s, minimalDoc)
	mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
	posts := clusterPosts(s)
	if len(posts) != 1 {
		t.Fatalf("cluster POSTs = %d, want 1", len(posts))
	}
	var want any
	if err := json.Unmarshal([]byte(createBodyFixture), &want); err != nil {
		t.Fatal(err)
	}
	wantJSON(t, "POST body", posts[0], want)
}

func TestCluster_CAP011_AC1_PlanLineSummary(t *testing.T) {
	s := newScenario(t)
	novaDev(s)
	writeCapellaYAML(t, s, minimalDoc)
	r := mustScenario(t, s.Run("capella", "plan"), "plan")
	wantContains(t, r.Stdout, "+ cluster nova-dev-01  aws us-east-1 · multi-AZ · 3 × 4 vCPU/16 GB · data,index,query · developer pro")
}

func TestCluster_CAP011_AC2_ChildrenAfterHealthy(t *testing.T) {
	s := newScenario(t)
	novaDev(s)
	writeCapellaYAML(t, s, childrenDoc)
	r := mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
	log := s.Fake.RequestLog()
	post, child, polls := -1, -1, 0
	for i, q := range log {
		switch {
		case q.Method == "POST" && strings.HasSuffix(q.Path, "/clusters"):
			post = i
		case q.Method == "POST" && strings.HasSuffix(q.Path, "/allowedcidrs") && child < 0:
			child = i
		case q.Method == "GET" && post >= 0 && child < 0 && strings.Contains(q.Path, "/clusters/") &&
			strings.Count(q.Path[strings.Index(q.Path, "/clusters/")+1:], "/") == 1:
			polls++
		}
	}
	if post < 0 || child < post || polls == 0 {
		t.Fatalf("cluster POST at %d, first child POST at %d, cluster polls between = %d", post, child, polls)
	}
	out := r.Stdout + r.Stderr
	if h, c := strings.Index(out, "cluster nova-dev-01: healthy"), strings.Index(out, "creating allowed-cidr"); h < 0 || c < h {
		t.Errorf("'healthy' must be printed before 'creating allowed-cidr':\n%s", out)
	}
}

func TestCluster_CAP011_AC2_SummaryConnectionString(t *testing.T) {
	s := newScenario(t)
	novaDev(s)
	writeCapellaYAML(t, s, minimalDoc)
	r := mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
	wantContains(t, r.Stdout, "connectionString (nova-dev-01): cb.")
}

func TestCluster_CAP011_AC3_DeployingWaitsNoPost(t *testing.T) {
	s := newScenario(t)
	cid := s.Fake.SeedCluster(novaDev(s), "nova-dev-01", "healthy",
		capellafake.WithField("description", "Nova development cluster"),
		capellafake.WithField("cloudProvider", map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.10.0.0/24"}),
		capellafake.WithField("couchbaseServer", map[string]any{"version": "8.0"}),
		capellafake.WithField("support", map[string]any{"plan": "developer pro", "timezone": "PT"}))
	s.Fake.SetTransition(cid, []string{"deploying", "healthy"}, 0)
	writeCapellaYAML(t, s, childrenDoc)
	r := mustScenario(t, s.Run("capella", "apply", "-y"), "apply")
	if n := len(clusterPosts(s)); n != 0 {
		t.Errorf("cluster POSTs = %d, want 0 (the deploying cluster is adopted)", n)
	}
	wantContains(t, r.Stdout+r.Stderr, "cluster nova-dev-01: deploying")
}

func TestCluster_CAP011_AC4_DeploymentFailedBlocked(t *testing.T) {
	s := newScenario(t)
	s.Fake.SeedCluster(novaDev(s), "nova-dev-01", "deploymentFailed",
		capellafake.WithField("description", "Nova development cluster"))
	writeCapellaYAML(t, s, minimalDoc)
	r := s.Run("capella", "apply", "-y")
	wantExit(t, r, cli.ExitBlocked)
	wantContains(t, r.Stdout, "! cluster nova-dev-01  in deploymentFailed; delete it (cbctl capella cluster delete nova-dev-01) and re-run apply")
	clitest.AssertNoWrites(t, s.Fake)
}

func TestCluster_CAP011_AC6_422MessageAndHint(t *testing.T) {
	s := newScenario(t)
	novaDev(s)
	writeCapellaYAML(t, s, minimalDoc)
	s.Fake.FailNextOpts("POST", clustersPath, capellafake.FaultOptions{Status: 422, Code: 4005, Message: "injected 422."})
	r := s.Run("capella", "apply", "-y")
	wantExit(t, r, cli.ExitError)
	wantContains(t, r.Stderr, "Failed: cluster nova-dev-01", "injected 422.")
	if n := len(s.Fake.Writes()); n != 1 {
		t.Errorf("writes = %d, want 1 (the refused cluster POST)", n)
	}
}

func TestCluster_CAP011_AC5_ValidateRules(t *testing.T) {
	for _, tc := range []struct{ old, new, msg string }{
		{"services: [data, index, query]", "services: [index]", "at least one service group must run the data service"},
		{"    cloudProvider:", "    configurationType: singleNode\n    cloudProvider:", "singleNode clusters must use availability: single"},
		{"    cloudProvider:", "    configurationType: singleNode\n    cloudProvider:", "singleNode clusters have exactly 1 node"},
		{"type: gp3", "type: pd-ssd", "disk type pd-ssd is not valid for aws"},
		{"storage: 50", "storage: 20", "must be >= 50"},
		{"    availability: { type: multi }", "    availability: { type: multi }\n    zones: [a, b]", "zones"},
		{"    availability: { type: multi }", "    availability: { type: multi }\n    zones: [use1-az1]", "zones can only be set for availability: single"},
	} {
		t.Run(tc.msg, func(t *testing.T) {
			s := newScenario(t)
			if !strings.Contains(minimalDoc, tc.old) {
				t.Fatalf("%q not in the document", tc.old)
			}
			writeCapellaYAML(t, s, strings.Replace(minimalDoc, tc.old, tc.new, 1))
			r := s.Run("capella", "validate")
			wantExit(t, r, cli.ExitConfigInvalid)
			wantContains(t, r.Stdout+r.Stderr, tc.msg)
		})
	}
}
