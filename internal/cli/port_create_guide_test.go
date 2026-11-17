package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/clitest"
)

// customClustersGuide is docs/capella/custom-clusters.md, relative to this package.
var customClustersGuide = filepath.Join("..", "..", "docs", "capella", "custom-clusters.md")

var (
	bashBlock    = regexp.MustCompile("(?s)```bash\n(.*?)```")
	shellComment = regexp.MustCompile(`\s+#.*$`)
)

// createGuideCommands returns the cbctl commands of the guide's bash blocks, in order, without the
// leading "cbctl".
func createGuideCommands(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(customClustersGuide)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range bashBlock.FindAllStringSubmatch(string(b), -1) {
		for _, line := range strings.Split(strings.ReplaceAll(m[1], "\\\n", " "), "\n") {
			line = shellComment.ReplaceAllString(strings.TrimSpace(line), "")
			if rest, ok := strings.CutPrefix(line, "cbctl "); ok {
				out = append(out, rest)
			}
		}
	}
	return out
}

// splitShell splits a command line on spaces, keeping double-quoted words together.
func splitShell(line string) []string {
	var out []string
	var cur strings.Builder
	quoted, any := false, false
	for _, r := range line {
		switch {
		case r == '"':
			quoted, any = !quoted, true
		case r == ' ' && !quoted:
			if any {
				out = append(out, cur.String())
				cur.Reset()
				any = false
			}
		default:
			cur.WriteRune(r)
			any = true
		}
	}
	if any {
		out = append(out, cur.String())
	}
	return out
}

// guideExitCodes are the non-zero exits the guide shows on purpose.
var guideExitCodes = map[string][]int{
	"capella cluster group remove prod-01 search": {4},
	"capella sync --dry-run":                      {0, 2},
}

// runGuide runs every command of the guide, in order, against the fake (project sandbox) and
// returns the create POST body of each cluster, by name.
func runGuide(t *testing.T) (*clitest.Scenario, map[string]map[string]any) {
	t.Helper()
	s := newScenario(t)
	s.Fake.SeedProject("sandbox")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	for _, cmd := range createGuideCommands(t) {
		if strings.Contains(cmd, "--skip-project") {
			continue // the guide's project non-prod does not exist in the fake organization
		}
		r := s.Run(append(splitShell(cmd), "-y")...)
		allowed := []int{0}
		for k, codes := range guideExitCodes {
			if strings.HasPrefix(cmd, k) {
				allowed = codes
			}
		}
		ok := false
		for _, c := range allowed {
			ok = ok || r.Code == c
		}
		if !ok {
			t.Fatalf("cbctl %s: exit %d\nstdout: %s\nstderr: %s", cmd, r.Code, r.Stdout, r.Stderr)
		}
	}
	bodies := map[string]map[string]any{}
	for _, b := range clusterPosts(s) {
		bodies[b["name"].(string)] = b
	}
	return s, bodies
}

// liveGroups returns the current disks of cluster name's service groups (in project sandbox),
// by services key, as the fake Capella holds them.
func liveGroups(t *testing.T, s *clitest.Scenario, name string) map[string]any {
	t.Helper()
	ctx := context.Background()
	c := s.Fake.Client()
	projects, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, p := range projects {
		clusters, err := c.ListClusters(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, cl := range clusters {
			if cl.Name != name {
				continue
			}
			for _, g := range cl.ServiceGroups {
				var disk any
				if g.Node != nil {
					disk = g.Node.Disk
				}
				out[strings.Join(g.Services, ",")] = disk
			}
		}
	}
	return out
}

func TestClusterCreate_CAP051_AC8_GuideSteps(t *testing.T) {
	cmds := createGuideCommands(t)
	hasGroup, hasAdd := false, false
	for _, c := range cmds {
		hasGroup = hasGroup || strings.Contains(c, "--group data:3x8/32")
		hasAdd = hasAdd || strings.HasPrefix(c, "capella cluster group add")
	}
	if !hasGroup || !hasAdd {
		t.Fatalf("the guide lost its --group create or group add step: %q", cmds)
	}
	s, bodies := runGuide(t)
	for _, n := range []string{"prod-01", "prod-02", "prod-03", "prod-01-weu", "prod-01-copy", "dev-03", "prod-04", "prod-06"} {
		if bodies[n] == nil {
			t.Errorf("the guide did not create %s", n)
		}
	}
	got := liveGroups(t, s, "prod-01")
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	wantJSON(t, "prod-01 groups", len(keys), 4)
	for _, k := range []string{"data", "index,query", "search", "eventing"} {
		if _, ok := got[k]; !ok {
			t.Errorf("prod-01 has no group %s (groups %v)", k, keys)
		}
	}
}

func TestClusterCreate_CAP060_AC3_GuideValueScope(t *testing.T) {
	_, bodies := runGuide(t)
	disks := func(name string) map[string]any {
		out := map[string]any{}
		for _, g := range groupsOf(bodies[name]) {
			var keys []string
			for _, sv := range g["services"].([]any) {
				keys = append(keys, sv.(string))
			}
			out[strings.Join(keys, ",")] = g["node"].(map[string]any)["disk"]
		}
		return out
	}
	wantJSON(t, "prod-02 disks", disks("prod-02"), map[string]any{
		"data,index,query": map[string]any{"type": "gp3", "storage": 100, "iops": 4370},
		"analytics":        map[string]any{"type": "gp3", "storage": 200, "iops": 5000},
	})
	d6 := disks("prod-06")
	wantJSON(t, "prod-06 data disk type", d6["data,index,query"].(map[string]any)["type"], "P10")
	wantJSON(t, "prod-06 search disk type", d6["search"].(map[string]any)["type"], "P20")
	wantJSON(t, "prod-06 analytics disk", d6["analytics"], map[string]any{"type": "Ultra", "storage": 256, "iops": 20000})
}

func TestClusterCreate_CAP061_AC2_DiskGuideStepsRun(t *testing.T) {
	hasDiskTypes := false
	for _, c := range createGuideCommands(t) {
		hasDiskTypes = hasDiskTypes || strings.HasPrefix(c, "capella cluster disk-types")
	}
	if !hasDiskTypes {
		t.Fatal("the guide lost its 'cluster disk-types' step")
	}
	s, bodies := runGuide(t)
	wantJSON(t, "prod-05 created with", groupsOf(bodies["prod-05"])[0]["node"].(map[string]any)["disk"],
		map[string]any{"type": "gp3", "storage": 200, "iops": 5740})
	wantJSON(t, "prod-05 disks", liveGroups(t, s, "prod-05"), map[string]any{
		"data,index,query": map[string]any{"type": "io2", "storage": 200, "iops": 20000},
		"search":           map[string]any{"type": "io2", "storage": 200, "iops": 5740},
	})
}
