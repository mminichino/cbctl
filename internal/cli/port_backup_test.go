package cli_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/clitest"
	"gopkg.in/yaml.v3"
)

// clusterPath is the fake's cluster URL as a Calls glob.
const clusterPath = "/v4/organizations/*/projects/*/clusters/*"

// backupEnv is a synced nova-dev project with the healthy cluster nova-dev-01 (capellactl's
// seed_healthy + import fixture).
type backupEnv struct {
	s   *clitest.Scenario
	pid string
	cid string
}

func newBackupEnv(t *testing.T) backupEnv {
	t.Helper()
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	pid := s.Fake.SeedProject("nova-dev")
	cid := s.Fake.SeedCluster(pid, "nova-dev-01", "healthy")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.ResetCalls()
	return backupEnv{s: s, pid: pid, cid: cid}
}

// threeSnapshots seeds backups of 26, 28 and 27 October kept 30 days (capellactl's `three`).
func (e backupEnv) threeSnapshots() []string {
	var ids []string
	for _, d := range []string{"26", "28", "27"} {
		ids = append(ids, e.s.Fake.SeedSnapshot(e.cid, "2026-10-"+d+"T02:00:00Z", 720, ""))
	}
	return ids
}

func (e backupEnv) run(args ...string) clitest.Result {
	return e.s.Run(append([]string{"capella"}, args...)...)
}

func (e backupEnv) must(t *testing.T, args ...string) clitest.Result {
	t.Helper()
	return mustScenario(t, e.run(args...), args...)
}

func wantCode(t *testing.T, r clitest.Result, code int, parts ...string) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.Code, code, r.Stdout, r.Stderr)
	}
	wantContains(t, r.Stdout+r.Stderr, parts...)
}

func fields(line string) []string { return strings.Fields(line) }

func wantFields(t *testing.T, line string, want ...string) {
	t.Helper()
	if got := fields(line); !reflect.DeepEqual(got, want) {
		t.Errorf("line %q fields = %v, want %v", line, got, want)
	}
}

func lastField(line string) string {
	f := fields(line)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

func wantBody(t *testing.T, c []any, want string) {
	t.Helper()
	if len(c) != 1 {
		t.Fatalf("calls = %d, want 1", len(c))
	}
	got, err := json.Marshal(c[0])
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func bodies(s *clitest.Scenario, method, glob string) []any {
	var out []any
	for _, c := range s.Fake.Calls(method, glob) {
		out = append(out, c.Body)
	}
	return out
}

// TestClusterBackup_CAP057_AC1_List: the list is newest first with the ID last, -o json carries
// every id, and an empty list says so without writes.
func TestClusterBackup_CAP057_AC1_List(t *testing.T) {
	e := newBackupEnv(t)
	ids := e.threeSnapshots()
	r := e.must(t, "cluster", "backup", "list", "nova-dev-01")
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	wantFields(t, lines[0], "CREATED", "TYPE", "STATUS", "SIZE", "EXPIRES", "COPIES", "ID")
	var got []string
	for _, l := range lines[1:] {
		got = append(got, lastField(l))
	}
	if want := []string{ids[1], ids[2], ids[0]}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want newest first %v", got, want)
	}
	if f := fields(lines[1]); len(f) < 2 || f[0] != "2026-10-28" || f[1] != "02:00" {
		t.Errorf("first row = %q", lines[1])
	}

	var data []map[string]any
	if err := json.Unmarshal([]byte(e.must(t, "-o", "json", "cluster", "backup", "list", "nova-dev-01").Stdout), &data); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range data {
		seen[d["id"].(string)] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("json misses %s", id)
		}
	}
	for _, id := range ids {
		e.must(t, "--yes", "cluster", "backup", "delete", "nova-dev-01", id)
	}
	e.s.Fake.ResetCalls()
	wantContains(t, e.must(t, "cluster", "backup", "list", "nova-dev-01").Stdout, "no backups")
	clitest.AssertNoWrites(t, e.s.Fake)
}

// TestClusterBackup_CAP057_AC1_ListProject: without NAME the project-level summary is read once
// and shows each cluster's newest and oldest backup.
func TestClusterBackup_CAP057_AC1_ListProject(t *testing.T) {
	e := newBackupEnv(t)
	e.threeSnapshots()
	r := e.must(t, "cluster", "backup", "list")
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	wantFields(t, lines[0], "CLUSTER", "REGION", "NEWEST", "OLDEST", "CLUSTER", "ID")
	if f := fields(lines[1]); len(f) < 5 || !reflect.DeepEqual(f[:5], []string{"nova-dev-01", "us-east-1", "2026-10-28", "02:00", "2026-10-26"}) {
		t.Errorf("row = %q", lines[1])
	}
	if len(e.s.Fake.Calls("GET", "/v4/organizations/*/projects/*/cloudsnapshotbackups")) == 0 {
		t.Error("project-level cloudsnapshotbackups not read")
	}
	clitest.AssertNoWrites(t, e.s.Fake)
}

// TestClusterBackup_CAP057_AC1_ListEveryProject: a capella.yaml with several projects lists every
// project's clusters with a PROJECT column; --project narrows it and drops the column.
func TestClusterBackup_CAP057_AC1_ListEveryProject(t *testing.T) {
	e := newBackupEnv(t)
	e.threeSnapshots()
	pid := e.s.Fake.SeedProject("non-prod")
	other := e.s.Fake.SeedCluster(pid, "web-01", "healthy")
	e.s.Fake.SeedSnapshot(other, "2026-10-29T02:00:00Z", 0, "")
	e.must(t, "sync")

	r := e.must(t, "cluster", "backup", "list")
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	wantFields(t, lines[0], "CLUSTER", "PROJECT", "REGION", "NEWEST", "OLDEST", "CLUSTER", "ID")
	var got [][]string
	for _, l := range lines[1:] {
		got = append(got, fields(l)[:2])
	}
	if want := [][]string{{"nova-dev-01", "nova-dev"}, {"web-01", "non-prod"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}

	r = e.must(t, "cluster", "backup", "list", "--project", "non-prod")
	if strings.Contains(r.Stdout, "PROJECT") || strings.Contains(r.Stdout, "nova-dev-01") || !strings.Contains(r.Stdout, "web-01") {
		t.Errorf("--project non-prod:\n%s", r.Stdout)
	}

	var data []map[string]any
	if err := json.Unmarshal([]byte(e.must(t, "-o", "json", "cluster", "backup", "list").Stdout), &data); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range data {
		names[d["projectName"].(string)] = true
	}
	if !names["nova-dev"] || !names["non-prod"] || len(names) != 2 {
		t.Errorf("projectName values = %v", names)
	}
}

// TestClusterBackup_CAP057_AC2_CreateWaits: create sends retention hours and copy regions, shows
// progress until complete; --no-wait returns once started.
func TestClusterBackup_CAP057_AC2_CreateWaits(t *testing.T) {
	e := newBackupEnv(t)
	e.s.Fake.SettlePolls(1)
	r := e.must(t, "--yes", "cluster", "backup", "create", "nova-dev-01", "--retain", "7d", "--copy-to", "us-west-2")
	wantBody(t, bodies(e.s, "POST", clusterPath+"/cloudsnapshotbackups"), `{"retention":168,"regionsToCopy":["us-west-2"]}`)
	var list []map[string]any
	if err := json.Unmarshal([]byte(e.must(t, "-o", "json", "cluster", "backup", "list", "nova-dev-01").Stdout), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	id := list[0]["id"].(string)
	wantContains(t, r.Stderr, "of nova-dev-01: pending", "of nova-dev-01: complete")
	wantContains(t, r.Stdout, "backup "+id+" of nova-dev-01 is complete")

	r = e.must(t, "--yes", "--no-wait", "cluster", "backup", "create", "nova-dev-01")
	wantContains(t, r.Stdout, "started (--no-wait)")
	if strings.Contains(r.Stdout, "is complete") {
		t.Errorf("--no-wait waited:\n%s", r.Stdout)
	}
}

// TestClusterBackup_CAP057_AC3_Retain: retain sends the new hours and reports the old ones; an
// unknown backup is refused (cbctl: exit 3, not found).
func TestClusterBackup_CAP057_AC3_Retain(t *testing.T) {
	e := newBackupEnv(t)
	ids := e.threeSnapshots()
	r := e.must(t, "--yes", "cluster", "backup", "retain", "nova-dev-01", ids[0], "--retain", "90d")
	wantBody(t, bodies(e.s, "PUT", clusterPath+"/cloudsnapshotbackups/*"), `{"retention":2160}`)
	wantContains(t, r.Stdout, "is now kept 90 days (was 30 days)")
	wantCode(t, e.run("cluster", "backup", "retain", "nova-dev-01", "nope", "--retain", "90d"), 3,
		"backup nope of nova-dev-01 not found")
}

// TestClusterBackup_CAP057_AC4_Delete: at a terminal the cluster name must be typed; a wrong name
// cancels with no writes, the right one deletes the backup.
func TestClusterBackup_CAP057_AC4_Delete(t *testing.T) {
	e := newBackupEnv(t)
	ids := e.threeSnapshots()
	e.s.TTY(true)
	e.s.Answers("nova")
	r := e.must(t, "cluster", "backup", "delete", "nova-dev-01", ids[0])
	wantContains(t, r.Stdout+r.Stderr, "cancelled; nothing was deleted")
	clitest.AssertNoWrites(t, e.s.Fake)

	e.s.Answers("nova-dev-01")
	e.must(t, "cluster", "backup", "delete", "nova-dev-01", ids[0])
	if n := len(e.s.Fake.Calls("DELETE", clusterPath+"/cloudsnapshotbackups/*")); n != 1 {
		t.Errorf("DELETE calls = %d, want 1", n)
	}
	if strings.Contains(e.must(t, "cluster", "backup", "list", "nova-dev-01").Stdout, ids[0]) {
		t.Errorf("backup %s still listed", ids[0])
	}
}

// TestClusterBackup_CAP057_AC5_RestoreConfirm: restore warns that all data is replaced and needs
// the cluster name typed; without a terminal and --yes it is refused (exit 4).
func TestClusterBackup_CAP057_AC5_RestoreConfirm(t *testing.T) {
	e := newBackupEnv(t)
	ids := e.threeSnapshots()
	e.s.TTY(true)
	e.s.Answers("other")
	r := e.must(t, "cluster", "backup", "restore", "nova-dev-01", ids[1])
	wantContains(t, r.Stdout+r.Stderr, "all data is replaced with the backup of 2026-10-28 02:00 UTC",
		"cancelled; nothing was restored")
	clitest.AssertNoWrites(t, e.s.Fake)

	e.s.TTY(false)
	wantCode(t, e.run("cluster", "backup", "restore", "nova-dev-01", ids[1]), 4)
	clitest.AssertNoWrites(t, e.s.Fake)
}

// TestClusterBackup_CAP057_AC5_RestoreWaits: --from-region is sent as the restore preference and
// the command waits for the restore and the cluster to be healthy.
func TestClusterBackup_CAP057_AC5_RestoreWaits(t *testing.T) {
	e := newBackupEnv(t)
	ids := e.threeSnapshots()
	r := e.must(t, "--yes", "cluster", "backup", "restore", "nova-dev-01", ids[1], "--from-region", "us-west-2")
	wantBody(t, bodies(e.s, "POST", clusterPath+"/cloudsnapshotbackups/*/restore"), `{"crossRegionRestorePreference":["us-west-2"]}`)
	out := r.Stdout + r.Stderr
	wantContains(t, out, "restore of nova-dev-01: complete", "cluster nova-dev-01: healthy",
		"restored cluster nova-dev-01 from the backup of 2026-10-28 02:00 UTC")
}

// TestClusterBackup_CAP057_AC6_RestoresAndRegions: restores starts empty, then lists the restore
// with its snapshot time; regions lists the copy regions.
func TestClusterBackup_CAP057_AC6_RestoresAndRegions(t *testing.T) {
	e := newBackupEnv(t)
	ids := e.threeSnapshots()
	wantContains(t, e.must(t, "cluster", "backup", "restores", "nova-dev-01").Stdout, "no restores")
	e.must(t, "--yes", "cluster", "backup", "restore", "nova-dev-01", ids[0])
	lines := strings.Split(e.must(t, "cluster", "backup", "restores", "nova-dev-01").Stdout, "\n")
	wantFields(t, lines[0], "STARTED", "FINISHED", "SNAPSHOT", "STATUS", "ID")
	wantContains(t, lines[1], "complete", "2026-10-26 02:00")
	r := e.must(t, "cluster", "backup", "regions", "nova-dev-01")
	wantFields(t, strings.Split(r.Stdout, "\n")[0], "REGION")
	wantContains(t, r.Stdout, "us-west-2")
}

// TestClusterBackup_CAP057_AC7_Help: the group lists every verb and each verb's help has examples.
func TestClusterBackup_CAP057_AC7_Help(t *testing.T) {
	s := newScenario(t)
	r := mustScenario(t, s.Run("capella", "cluster", "backup", "--help"), "cluster backup --help")
	for _, verb := range []string{"list", "create", "retain", "delete", "restore", "restores", "regions", "schedule"} {
		wantContains(t, r.Stdout, verb)
		h := mustScenario(t, s.Run("capella", "cluster", "backup", verb, "--help"), verb, "--help")
		if !strings.Contains(h.Stdout, "Examples:") {
			t.Errorf("cluster backup %s --help has no Examples:\n%s", verb, h.Stdout)
		}
	}
}

// seedSnapshotSchedule puts a schedule straight into Capella, as the Capella UI would.
func (e backupEnv) seedSnapshotSchedule(t *testing.T) {
	t.Helper()
	err := e.s.Fake.Client().UpsertSnapshotSchedule(context.Background(), e.pid, e.cid, capellaapi.Object{
		"interval": 24, "retention": 720, "startTime": "2026-10-30T02:00:00Z", "copyToRegions": []any{"us-west-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.s.Fake.ResetCalls()
}

// planNoChanges is the plan summary when Capella matches the file.
const planNoChanges = "Plan: 0 to add, 0 to change, 0 to delete, 0 blocked."

// clusterSchedule returns the first cluster's backupSchedule in capella.yaml (nil when absent).
func clusterSchedule(t *testing.T, s *clitest.Scenario) map[string]any {
	t.Helper()
	var doc struct {
		Projects []struct {
			Clusters []map[string]any `yaml:"clusters"`
		} `yaml:"projects"`
	}
	if err := yaml.Unmarshal([]byte(s.ReadFile("capella.yaml")), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Projects) == 0 || len(doc.Projects[0].Clusters) == 0 {
		t.Fatal("capella.yaml has no cluster")
	}
	m, _ := doc.Projects[0].Clusters[0]["backupSchedule"].(map[string]any)
	return m
}

func wantYAML(t *testing.T, got map[string]any, want string) {
	t.Helper()
	var w map[string]any
	if err := yaml.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("backupSchedule = %v, want %v", got, w)
	}
}

// TestClusterBackupSchedule_CAP056_AC1_SetBody: with a capella.yaml, set plans the new schedule,
// reads the permitted options first, PUTs it and records it in the file.
func TestClusterBackupSchedule_CAP056_AC1_SetBody(t *testing.T) {
	e := newBackupEnv(t)
	r := e.must(t, "--yes", "cluster", "backup", "schedule", "set", "nova-dev-01", "--every", "24", "--retain", "30d")
	wantContains(t, r.Stdout, "+ backup-schedule nova-dev-01  every 24 h - kept 30 days")
	if len(e.s.Fake.Calls("GET", clusterPath+"/cloudsnapshotbackupschedule/options")) == 0 {
		t.Error("schedule options not read before the write")
	}
	wantBody(t, bodies(e.s, "PUT", clusterPath+"/cloudsnapshotbackupschedule"), `{"interval":24,"retention":720,"copyToRegions":[]}`)
	wantYAML(t, clusterSchedule(t, e.s), "{interval: 24, retention: 720}")
	got, err := e.s.Fake.Client().GetSnapshotSchedule(context.Background(), e.pid, e.cid)
	if err != nil || intOf(got["retention"]) != 720 {
		t.Errorf("Capella schedule = %v, %v", got, err)
	}
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// TestClusterBackupSchedule_CAP056_AC1_NoDrift: start time and copy region reach Capella and the
// file, and plan and drift are clean right afterwards.
func TestClusterBackupSchedule_CAP056_AC1_NoDrift(t *testing.T) {
	e := newBackupEnv(t)
	e.must(t, "--yes", "cluster", "backup", "schedule", "set", "nova-dev-01", "--every", "6", "--retain", "168h",
		"--start", "02:00", "--copy-to", "us-west-2")
	puts := bodies(e.s, "PUT", clusterPath+"/cloudsnapshotbackupschedule")
	if len(puts) != 1 {
		t.Fatalf("PUTs = %v", puts)
	}
	body := puts[0].(map[string]any)
	if st, _ := body["startTime"].(string); !strings.HasSuffix(st, "T02:00:00Z") {
		t.Errorf("startTime = %v", body["startTime"])
	}
	if !reflect.DeepEqual(body["copyToRegions"], []any{"us-west-2"}) {
		t.Errorf("copyToRegions = %v", body["copyToRegions"])
	}
	wantYAML(t, clusterSchedule(t, e.s), `{interval: 6, retention: 168, startTime: "02:00", copyToRegions: [us-west-2]}`)
	wantContains(t, e.must(t, "plan").Stdout, planNoChanges)
	e.must(t, "drift", "--detailed-exitcode")
}

// TestClusterBackupSchedule_CAP056_AC3_Refused: invalid schedules change neither Capella nor the
// file. Values the cluster's options do not permit block the plan (cbctl exit 4); malformed flags
// and file-invalid values are usage errors (exit 3). The fake permits up to 365 days, so 400d
// stands in for capellactl's 120d on a 90-day plan.
func TestClusterBackupSchedule_CAP056_AC3_Refused(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
		code  int
		msg   string
	}{
		{"interval", []string{"--every", "3", "--retain", "30d"}, 4, "interval 3 is not permitted: use one of 1, 2, 4, 6, 8, 12, 24 hours"},
		{"short retention", []string{"--every", "24", "--retain", "12"}, 4, "retention 12 h is not permitted: keep backups 1 day to 365 days"},
		{"long retention", []string{"--every", "24", "--retain", "400d"}, 4, "retention 400 days is not permitted"},
		{"three regions", []string{"--every", "24", "--retain", "30d", "--copy-to", "us-east-2", "--copy-to", "us-west-2", "--copy-to", "eu-west-1"}, 3, "copyToRegions"},
		{"unknown region", []string{"--every", "24", "--retain", "30d", "--copy-to", "mars-1"}, 4, "copies cannot go to mars-1"},
		{"weeks", []string{"--every", "24", "--retain", "1w"}, 3, "--retain takes hours or days"},
		{"retain alone", []string{"--retain", "30d"}, 3, "a new backup schedule needs --every HOURS and --retain DURATION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newBackupEnv(t)
			before := e.s.ReadFile("capella.yaml")
			r := e.run(append([]string{"--yes", "cluster", "backup", "schedule", "set", "nova-dev-01"}, tc.extra...)...)
			wantCode(t, r, tc.code, tc.msg)
			clitest.AssertNoWrites(t, e.s.Fake)
			if after := e.s.ReadFile("capella.yaml"); after != before {
				t.Errorf("capella.yaml changed:\n%s", after)
			}
		})
	}
}

// TestClusterBackupSchedule_CAP056_AC3_FreeTier: free-tier clusters have no backups (exit 4).
func TestClusterBackupSchedule_CAP056_AC3_FreeTier(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	s.Fake.SeedFreeCluster(pid, "try-01", "healthy")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.ResetCalls()
	before := s.ReadFile("capella.yaml")
	wantCode(t, s.Run("capella", "--yes", "cluster", "backup", "schedule", "set", "try-01", "--every", "24", "--retain", "30d"), 4,
		"free-tier clusters have no backups")
	wantCode(t, s.Run("capella", "cluster", "backup", "list", "try-01"), 4, "free-tier clusters have no backups")
	clitest.AssertNoWrites(t, s.Fake)
	if s.ReadFile("capella.yaml") != before {
		t.Error("capella.yaml changed")
	}
}

// TestClusterBackupSchedule_CAP056_AC4_Show: show says when there is none, then prints the summary
// line, the next backup, and JSON with nextBackup.
func TestClusterBackupSchedule_CAP056_AC4_Show(t *testing.T) {
	e := newBackupEnv(t)
	wantContains(t, e.must(t, "cluster", "backup", "schedule", "show", "nova-dev-01").Stdout, "has no backup schedule")
	e.seedSnapshotSchedule(t)
	r := e.must(t, "cluster", "backup", "schedule", "show", "nova-dev-01")
	wantContains(t, r.Stdout, "every 24 h - kept 30 days - first at 02:00 UTC - copies: us-west-2", "next backup: ")
	var data map[string]any
	if err := json.Unmarshal([]byte(e.must(t, "-o", "json", "cluster", "backup", "schedule", "show", "nova-dev-01").Stdout), &data); err != nil {
		t.Fatal(err)
	}
	if intOf(data["interval"]) != 24 || !reflect.DeepEqual(data["copyToRegions"], []any{"us-west-2"}) || data["nextBackup"] == nil {
		t.Errorf("json = %v", data)
	}
	clitest.AssertNoWrites(t, e.s.Fake)
}

// TestClusterBackupSchedule_CAP056_AC4_Options: options shows the permitted intervals, the keep
// range and the copy regions, and the same as JSON.
func TestClusterBackupSchedule_CAP056_AC4_Options(t *testing.T) {
	e := newBackupEnv(t)
	r := e.must(t, "cluster", "backup", "schedule", "options", "nova-dev-01")
	wantContains(t, r.Stdout, "every (hours)   1, 2, 4, 6, 8, 12, 24", "keep            1 day to 365 days",
		"copy regions    us-east-2, us-west-2, eu-west-1 (up to two)")
	var data map[string]any
	if err := json.Unmarshal([]byte(e.must(t, "-o", "json", "cluster", "backup", "schedule", "options", "nova-dev-01").Stdout), &data); err != nil {
		t.Fatal(err)
	}
	regions, _ := data["regions"].([]any)
	if intOf(data["maximumRetention"]) != 8760 || len(regions) == 0 || regions[0] != "us-east-2" {
		t.Errorf("json = %v", data)
	}
	clitest.AssertNoWrites(t, e.s.Fake)
}

// TestClusterBackupSchedule_CAP056_AC6_SyncRoundTrip: sync writes a schedule made in Capella into
// the file and the next apply has nothing to do.
func TestClusterBackupSchedule_CAP056_AC6_SyncRoundTrip(t *testing.T) {
	e := newBackupEnv(t)
	e.seedSnapshotSchedule(t)
	e.must(t, "sync")
	wantYAML(t, clusterSchedule(t, e.s), `{interval: 24, retention: 720, startTime: "02:00", copyToRegions: [us-west-2]}`)
	e.s.Fake.ResetCalls()
	wantContains(t, e.must(t, "--yes", "apply").Stdout, "No changes")
	clitest.AssertNoWrites(t, e.s.Fake)
}

// TestClusterBackupSchedule_CAP056_AC6_SyncKeepsUnreadable: a key that may not read backup
// schedules (403) keeps the file's schedule and warns about the roles.
func TestClusterBackupSchedule_CAP056_AC6_SyncKeepsUnreadable(t *testing.T) {
	e := newBackupEnv(t)
	e.must(t, "--yes", "cluster", "backup", "schedule", "set", "nova-dev-01", "--every", "24", "--retain", "30d")
	e.s.Fake.FailNext("GET", ".*/cloudsnapshotbackupschedule$", 403, 5)
	r := e.must(t, "sync")
	wantContains(t, r.Stderr, "backup schedules not read: the API key does not have the Organization Owner or Project Owner role")
	wantYAML(t, clusterSchedule(t, e.s), "{interval: 24, retention: 720}")
}

// TestClusterBackupSchedule_CAP056_AC6_DriftAndPrune: a schedule the file does not declare is left
// alone by plan, reported by drift, and deleted by apply --prune.
func TestClusterBackupSchedule_CAP056_AC6_DriftAndPrune(t *testing.T) {
	e := newBackupEnv(t)
	e.seedSnapshotSchedule(t)
	r := e.must(t, "plan")
	// cbctl lists it as unmanaged (informational) and plans no change for it.
	wantContains(t, r.Stdout, planNoChanges, "Unmanaged (not in capella.yaml; use --prune to delete):")
	if strings.Contains(r.Stdout, "- backup-schedule") || strings.Contains(r.Stdout, "~ backup-schedule") {
		t.Errorf("plan changes the undeclared schedule:\n%s", r.Stdout)
	}
	clitest.AssertNoWrites(t, e.s.Fake)
	wantContains(t, e.must(t, "drift").Stdout, "backup-schedule nova-dev-01  every 24 h - kept 30 days")
	r = e.must(t, "--yes", "apply", "--prune")
	wantContains(t, r.Stdout, "- backup-schedule nova-dev-01")
	if n := len(e.s.Fake.Calls("DELETE", clusterPath+"/cloudsnapshotbackupschedule")); n != 1 {
		t.Errorf("DELETE calls = %d, want 1", n)
	}
	wantContains(t, e.must(t, "cluster", "backup", "schedule", "show", "nova-dev-01").Stdout, "has no backup schedule")
}

// TestClusterBackupSchedule_CAP056_AC7_Help: each schedule verb has examples, and the guide covers
// both kinds of backup and the backupSchedule field.
func TestClusterBackupSchedule_CAP056_AC7_Help(t *testing.T) {
	s := newScenario(t)
	for _, verb := range []string{"show", "options", "set", "delete"} {
		r := mustScenario(t, s.Run("capella", "cluster", "backup", "schedule", verb, "--help"), verb)
		wantContains(t, r.Stdout, "Examples:", "cbctl capella cluster backup schedule")
	}
	r := mustScenario(t, s.Run("capella", "guide", "backup"), "guide backup")
	wantContains(t, r.Stdout, "Cluster backup", "Bucket backup", "backupSchedule")
	r = mustScenario(t, s.Run("capella", "guide", "config", "clusters.backupSchedule"), "guide config")
	wantContains(t, r.Stdout, "Cloud snapshot backups")
}

// bucketWorld is capellactl's `world` fixture: nova-dev-01 with buckets orders and users, a
// second cluster nova-dr-01, a full and an incremental backup of orders in cycle cy-1 and a manual
// backup of users.
type bucketWorld struct {
	backupEnv
	dr, full, inc, users string
}

func newBucketWorld(t *testing.T) bucketWorld {
	t.Helper()
	e := newBackupEnv(t)
	f := e.s.Fake
	dr := f.SeedCluster(e.pid, "nova-dr-01", "healthy")
	f.SeedBucket(e.cid, "orders")
	f.SeedBucket(e.cid, "users")
	w := bucketWorld{backupEnv: e, dr: dr}
	w.full = f.SeedBucketBackup(e.cid, "orders", "full", "scheduled",
		capellafake.WithField("cycleID", "cy-1"), capellafake.WithField("date", "2026-10-25T02:00:00Z"))
	w.inc = f.SeedBucketBackup(e.cid, "orders", "incremental", "scheduled",
		capellafake.WithField("cycleID", "cy-1"), capellafake.WithField("date", "2026-10-26T02:00:00Z"))
	w.users = f.SeedBucketBackup(e.cid, "users", "full", "manual",
		capellafake.WithField("cycleID", "cy-u"), capellafake.WithField("date", "2026-10-24T02:00:00Z"))
	e.must(t, "sync")
	f.ResetCalls()
	return w
}

func dataLines(out string) []string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	return lines[1:]
}

// TestBucketBackup_CAP059_AC1_List: the latest backup of each bucket, newest first with the ID
// last; with BUCKET every backup of it; JSON carries bucketName; an empty list says so.
func TestBucketBackup_CAP059_AC1_List(t *testing.T) {
	w := newBucketWorld(t)
	r := w.must(t, "bucket", "backup", "list", "nova-dev-01")
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	wantFields(t, lines[0], "BUCKET", "DATE", "METHOD", "SOURCE", "STATUS", "SIZE", "ITEMS", "EXPIRES", "ID")
	var buckets []string
	for _, l := range lines[1:] {
		buckets = append(buckets, fields(l)[0])
	}
	if !reflect.DeepEqual(buckets, []string{"orders", "users"}) {
		t.Errorf("buckets = %v", buckets)
	}
	if lastField(lines[1]) != w.inc {
		t.Errorf("latest orders backup = %q, want %s", lines[1], w.inc)
	}
	var ids []string
	for _, l := range dataLines(w.must(t, "bucket", "backup", "list", "nova-dev-01", "orders").Stdout) {
		ids = append(ids, lastField(l))
	}
	if !reflect.DeepEqual(ids, []string{w.inc, w.full}) {
		t.Errorf("orders backups = %v", ids)
	}
	var data []map[string]any
	if err := json.Unmarshal([]byte(w.must(t, "-o", "json", "bucket", "backup", "list", "nova-dev-01").Stdout), &data); err != nil {
		t.Fatal(err)
	}
	names := map[any]bool{}
	for _, d := range data {
		names[d["bucketName"]] = true
	}
	if !names["orders"] || !names["users"] || len(names) != 2 {
		t.Errorf("bucketName = %v", names)
	}
	w.must(t, "--yes", "bucket", "backup", "delete", "nova-dev-01", w.full)
	w.must(t, "--yes", "bucket", "backup", "delete", "nova-dev-01", w.users)
	w.s.Fake.ResetCalls()
	wantContains(t, w.must(t, "bucket", "backup", "list", "nova-dev-01").Stdout, "no backups")
	clitest.AssertNoWrites(t, w.s.Fake)
}

// TestBucketBackup_CAP059_AC2_Create: create POSTs to the bucket's backups and waits until ready;
// --no-wait returns once started.
func TestBucketBackup_CAP059_AC2_Create(t *testing.T) {
	w := newBucketWorld(t)
	w.s.Fake.SettlePolls(1)
	r := w.must(t, "--yes", "bucket", "backup", "create", "nova-dev-01", "orders")
	posts := w.s.Fake.Calls("POST", clusterPath+"/buckets/*/backups")
	if len(posts) != 1 || !strings.HasSuffix(posts[0].Path, "/buckets/b3JkZXJz/backups") {
		t.Fatalf("POSTs = %v", posts)
	}
	wantContains(t, r.Stderr, "backup of orders: ready")
	wantContains(t, r.Stdout, "of orders is ready")
	r = w.must(t, "--yes", "--no-wait", "bucket", "backup", "create", "nova-dev-01", "users")
	wantContains(t, r.Stdout, "started (--no-wait)")
}

// TestBucketBackup_CAP059_AC2_CreateFailedAndRefused: a failed backup exits 1 and says so; an
// unknown bucket is refused before any write (cbctl: exit 3 for not found).
func TestBucketBackup_CAP059_AC2_CreateFailedAndRefused(t *testing.T) {
	w := newBucketWorld(t)
	w.s.Fake.FailNextBucketBackup()
	wantCode(t, w.run("--yes", "bucket", "backup", "create", "nova-dev-01", "orders"), 1, "backup of orders ended failed")
	w.s.Fake.ResetCalls()
	wantCode(t, w.run("--yes", "bucket", "backup", "create", "nova-dev-01", "nope"), 3, "bucket nope not found")
	clitest.AssertNoWrites(t, w.s.Fake)
}

// TestBucketBackup_CAP059_AC3_DeleteCycle: delete says it removes the whole cycle; the bucket name
// must be typed; the right name deletes both backups of the cycle with one DELETE.
func TestBucketBackup_CAP059_AC3_DeleteCycle(t *testing.T) {
	w := newBucketWorld(t)
	w.s.TTY(true)
	w.s.Answers("users")
	r := w.must(t, "bucket", "backup", "delete", "nova-dev-01", w.inc)
	wantContains(t, r.Stdout+r.Stderr, "deletes cycle 2026-10-25 02:00 with 2 backups of orders", "cancelled")
	clitest.AssertNoWrites(t, w.s.Fake)
	w.s.Answers("orders")
	w.must(t, "bucket", "backup", "delete", "nova-dev-01", w.inc)
	if n := len(w.s.Fake.Calls("DELETE", clusterPath+"/backups/*")); n != 1 {
		t.Errorf("DELETE calls = %d, want 1", n)
	}
	w.s.TTY(false)
	out := w.must(t, "bucket", "backup", "list", "nova-dev-01").Stdout
	if strings.Contains(out, w.full) || strings.Contains(out, w.inc) || !strings.Contains(out, w.users) {
		t.Errorf("after delete:\n%s", out)
	}
}

// TestBucketBackup_CAP059_AC4_RestoreConfirm: a wrong typed name cancels the restore.
func TestBucketBackup_CAP059_AC4_RestoreConfirm(t *testing.T) {
	w := newBucketWorld(t)
	w.s.TTY(true)
	w.s.Answers("orders")
	r := w.must(t, "bucket", "backup", "restore", "nova-dev-01", w.full)
	wantContains(t, r.Stdout+r.Stderr, "cancelled; nothing was restored")
	clitest.AssertNoWrites(t, w.s.Fake)
}

// TestBucketBackup_CAP059_AC5_RestoreOptionsBody: every restore flag reaches the POST body.
func TestBucketBackup_CAP059_AC5_RestoreOptionsBody(t *testing.T) {
	w := newBucketWorld(t)
	r := w.must(t, "--yes", "bucket", "backup", "restore", "nova-dev-01", w.full, "--to-cluster", "nova-dr-01",
		"--include-data", "orders.sales", "--map-data", "orders=orders_copy", "--replace-ttl", "all",
		"--replace-ttl-with", "2027-01-01T00:00:00Z", "--services", "query,data", "--force-updates",
		"--auto-remove-collections", "--filter-keys", "^order::")
	wantContains(t, r.Stdout, "into nova-dr-01", "overwrites newer documents (--force-updates)")
	want, err := json.Marshal(map[string]any{
		"targetClusterID": w.dr, "sourceClusterID": w.cid, "backupID": w.full,
		"services": []string{"data", "query"}, "includeData": "orders.sales", "mapData": "orders=orders_copy",
		"filterKeys": "^order::", "replaceTTL": "all", "replaceTTLWith": "2027-01-01T00:00:00Z", "forceUpdates": true,
		"autoRemoveCollections": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantBody(t, bodies(w.s, "POST", clusterPath+"/backups/*/restore"), string(want))
}

// TestBucketBackup_CAP059_AC5_RestoreRefused: bad option combinations and an unknown target are
// refused with exit 3 before any write.
func TestBucketBackup_CAP059_AC5_RestoreRefused(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
		msg   string
	}{
		{"ttl-with alone", []string{"--replace-ttl-with", "2027-01-01T00:00:00Z"}, "--replace-ttl-with was given without --replace-ttl"},
		{"ttl none", []string{"--replace-ttl", "none", "--replace-ttl-with", "2027-01-01T00:00:00Z"}, "--replace-ttl-with was given without --replace-ttl"},
		{"index service", []string{"--services", "data,index"}, "--services"},
		{"unknown target", []string{"--to-cluster", "nope-01"}, "cluster nope-01 not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newBucketWorld(t)
			wantCode(t, w.run(append([]string{"--yes", "bucket", "backup", "restore", "nova-dev-01", w.full}, tc.extra...)...), 3, tc.msg)
			clitest.AssertNoWrites(t, w.s.Fake)
		})
	}
}

// TestBucketBackup_CAP059_AC5_RestoreTargetTurnedOff: a turned-off target cluster is refused (4).
func TestBucketBackup_CAP059_AC5_RestoreTargetTurnedOff(t *testing.T) {
	w := newBucketWorld(t)
	w.s.Fake.SetClusterState(w.dr, "turnedOff")
	wantCode(t, w.run("--yes", "bucket", "backup", "restore", "nova-dev-01", w.full, "--to-cluster", "nova-dr-01"), 4,
		"nova-dr-01 is turned off")
	clitest.AssertNoWrites(t, w.s.Fake)
}

// TestBucketBackup_CAP059_AC6_ExportExistingAndRefused: the download link is printed once and never
// logged; a second export of the same cycle says one already exists; a backup that is not ready
// cannot be exported (exit 4).
func TestBucketBackup_CAP059_AC6_ExportExistingAndRefused(t *testing.T) {
	w := newBucketWorld(t)
	r := w.must(t, "--log-level", "debug", "bucket", "backup", "export", "nova-dev-01", w.full)
	out := r.Stdout + r.Stderr
	if !strings.Contains(r.Stderr, "/exports/") {
		t.Errorf("no debug request lines:\n%s", r.Stderr)
	}
	if n := strings.Count(out, "https://capella-fake.test/exports/"); n != 1 {
		t.Errorf("download link appears %d times, want 1:\n%s", n, out)
	}
	w.s.Fake.ResetCalls()
	r = w.must(t, "bucket", "backup", "export", "nova-dev-01", w.inc)
	wantContains(t, r.Stdout, "already has an export (Capella allows one per cycle)")
	if n := len(w.s.Fake.Writes()); n != 1 {
		t.Errorf("writes = %d, want the refused POST only", n)
	}
	pending := w.s.Fake.SeedBucketBackup(w.cid, "users", "full", "manual",
		capellafake.WithField("cycleID", "cy-p"), capellafake.WithField("status", "pending"))
	wantCode(t, w.run("bucket", "backup", "export", "nova-dev-01", pending), 4, "Capella cannot export this backup", "ready")
}

// TestBucketBackup_CAP059_AC7_Help: the group lists every verb and each verb's help has examples.
func TestBucketBackup_CAP059_AC7_Help(t *testing.T) {
	s := newScenario(t)
	r := mustScenario(t, s.Run("capella", "bucket", "backup", "--help"), "bucket backup --help")
	for _, verb := range []string{"list", "get", "create", "delete", "restore", "export", "schedule", "cycle"} {
		wantContains(t, r.Stdout, verb)
		h := mustScenario(t, s.Run("capella", "bucket", "backup", verb, "--help"), verb, "--help")
		if !strings.Contains(h.Stdout, "Examples:") {
			t.Errorf("bucket backup %s --help has no Examples:\n%s", verb, h.Stdout)
		}
	}
}

// bucketScheduleDefaults is what a bare `bucket backup schedule set` records and sends.
const bucketScheduleDefaults = `{dayOfWeek: sunday, startAt: 0, incrementalEvery: 24, retentionTime: 30days, costOptimizedRetention: false}`

// newOrdersEnv is capellactl's bucket-schedule `imported` fixture: nova-dev-01 with bucket orders.
func newOrdersEnv(t *testing.T) backupEnv {
	t.Helper()
	e := newBackupEnv(t)
	e.s.Fake.SeedBucket(e.cid, "orders")
	e.must(t, "sync")
	e.s.Fake.ResetCalls()
	return e
}

// ordersEntry returns the orders bucket entry of capella.yaml as an ordered mapping node.
func ordersEntry(t *testing.T, s *clitest.Scenario) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(s.ReadFile("capella.yaml")), &doc); err != nil {
		t.Fatal(err)
	}
	var find func(n *yaml.Node) *yaml.Node
	find = func(n *yaml.Node) *yaml.Node {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == "name" && n.Content[i+1].Value == "orders" {
					return n
				}
			}
		}
		for _, c := range n.Content {
			if m := find(c); m != nil {
				return m
			}
		}
		return nil
	}
	m := find(&doc)
	if m == nil {
		t.Fatal("capella.yaml has no bucket orders")
	}
	return m
}

func ordersSchedule(t *testing.T, s *clitest.Scenario) map[string]any {
	t.Helper()
	var m map[string]any
	if err := ordersEntry(t, s).Decode(&m); err != nil {
		t.Fatal(err)
	}
	sch, _ := m["backupSchedule"].(map[string]any)
	return sch
}

func setOrders(e backupEnv, extra ...string) clitest.Result {
	return e.run(append([]string{"--yes", "bucket", "backup", "schedule", "set", "nova-dev-01", "orders"}, extra...)...)
}

const ordersBucketID = "b3JkZXJz" // the fake's bucket ID is the base64 of the name

func (e backupEnv) ordersSchedule(t *testing.T) capellaapi.Object {
	t.Helper()
	got, err := e.s.Fake.Client().GetBucketBackupSchedule(context.Background(), e.pid, e.cid, ordersBucketID)
	if err != nil {
		return nil
	}
	return got
}

// TestBucketBackupSchedule_CAP058_AC1_SetCreate: a bare set plans the defaults, POSTs them, records
// them as the bucket's last key, and the next plan is clean.
func TestBucketBackupSchedule_CAP058_AC1_SetCreate(t *testing.T) {
	e := newOrdersEnv(t)
	r := mustScenario(t, setOrders(e), "set")
	wantContains(t, r.Stdout, "+ bucket-backup-schedule nova-dev-01/orders  weekly full on sunday at 00:00 UTC - "+
		"incremental every 24 h - kept 30 days")
	wantBody(t, bodies(e.s, "POST", clusterPath+"/buckets/*/backup/schedules"),
		`{"type":"weekly","weeklySchedule":{"dayOfWeek":"sunday","startAt":0,"incrementalEvery":24,"retentionTime":"30days","costOptimizedRetention":false}}`)
	wantYAML(t, ordersSchedule(t, e.s), bucketScheduleDefaults)
	entry := ordersEntry(t, e.s)
	if last := entry.Content[len(entry.Content)-2].Value; last != "backupSchedule" {
		t.Errorf("last key of the bucket = %q, want backupSchedule", last)
	}
	wantContains(t, e.must(t, "plan").Stdout, planNoChanges)
}

// TestBucketBackupSchedule_CAP058_AC1_SetUpdate: changing one field shows its diff, PUTs once
// (no POST) and updates the file.
func TestBucketBackupSchedule_CAP058_AC1_SetUpdate(t *testing.T) {
	e := newOrdersEnv(t)
	mustScenario(t, setOrders(e), "set")
	e.s.Fake.ResetCalls()
	r := mustScenario(t, setOrders(e, "--incremental-every", "12"), "set --incremental-every 12")
	wantContains(t, r.Stdout, "incrementalEvery:  24 -> 12")
	puts := bodies(e.s, "PUT", clusterPath+"/buckets/*/backup/schedules")
	if len(puts) != 1 {
		t.Fatalf("PUTs = %v", puts)
	}
	ws := puts[0].(map[string]any)["weeklySchedule"].(map[string]any)
	if intOf(ws["incrementalEvery"]) != 12 || ws["dayOfWeek"] != "sunday" || ws["retentionTime"] != "30days" {
		t.Errorf("weeklySchedule = %v", ws)
	}
	if n := len(e.s.Fake.Calls("POST", clusterPath+"/buckets/*/backup/schedules")); n != 0 {
		t.Errorf("POSTs = %d, want 0", n)
	}
	wantYAML(t, ordersSchedule(t, e.s), `{dayOfWeek: sunday, startAt: 0, incrementalEvery: 12, retentionTime: 30days, costOptimizedRetention: false}`)
}

// TestBucketBackupSchedule_CAP058_AC1_SetInteractive: at a terminal without flags, set asks for
// each field and creates what was answered.
func TestBucketBackupSchedule_CAP058_AC1_SetInteractive(t *testing.T) {
	e := newOrdersEnv(t)
	e.s.TTY(true)
	e.s.Answers("y") // apply the plan
	r := e.s.RunWith("monday\n3\n12\n90days\nn\n", "capella", "bucket", "backup", "schedule", "set", "nova-dev-01", "orders")
	mustScenario(t, r, "set (interactive)")
	wantContains(t, r.Stderr, "Day of the weekly full backup", "Hours between incremental backups")
	wantBody(t, bodies(e.s, "POST", clusterPath+"/buckets/*/backup/schedules"),
		`{"type":"weekly","weeklySchedule":{"dayOfWeek":"monday","startAt":3,"incrementalEvery":12,"retentionTime":"90days","costOptimizedRetention":false}}`)
}

// TestBucketBackupSchedule_CAP058_AC3_FreeTier: free-tier clusters have no backups (exit 4).
func TestBucketBackupSchedule_CAP058_AC3_FreeTier(t *testing.T) {
	s := newScenario(t)
	pid := s.Fake.SeedProject("nova-dev")
	fid := s.Fake.SeedFreeCluster(pid, "try-01", "healthy")
	s.Fake.SeedBucket(fid, "free-b")
	mustScenario(t, s.Run("capella", "sync"), "sync")
	s.Fake.ResetCalls()
	before := s.ReadFile("capella.yaml")
	wantCode(t, s.Run("capella", "--yes", "bucket", "backup", "schedule", "set", "try-01", "free-b"), 4,
		"free-tier clusters have no backups")
	clitest.AssertNoWrites(t, s.Fake)
	if s.ReadFile("capella.yaml") != before {
		t.Error("capella.yaml changed")
	}
}

// TestBucketBackupSchedule_CAP058_AC4_Show: show says when there is none, then prints the summary
// line of a schedule made in Capella, and the JSON form.
func TestBucketBackupSchedule_CAP058_AC4_Show(t *testing.T) {
	e := newOrdersEnv(t)
	wantContains(t, e.must(t, "bucket", "backup", "schedule", "show", "nova-dev-01", "orders").Stdout, "has no backup schedule")
	err := e.s.Fake.Client().CreateBucketBackupSchedule(context.Background(), e.pid, e.cid, ordersBucketID, capellaapi.Object{
		"type": "weekly", "weeklySchedule": map[string]any{"dayOfWeek": "sunday", "startAt": 2, "incrementalEvery": 24,
			"retentionTime": "1year", "costOptimizedRetention": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantContains(t, e.must(t, "bucket", "backup", "schedule", "show", "nova-dev-01", "orders").Stdout,
		"weekly full on sunday at 02:00 UTC - incremental every 24 h - kept 1 year")
	var data map[string]any
	if err := json.Unmarshal([]byte(e.must(t, "-o", "json", "bucket", "backup", "schedule", "show", "nova-dev-01", "orders").Stdout), &data); err != nil {
		t.Fatal(err)
	}
	ws, _ := data["weeklySchedule"].(map[string]any)
	if data["type"] != "weekly" || intOf(ws["startAt"]) != 2 {
		t.Errorf("json = %v", data)
	}
}

// TestBucketBackupSchedule_CAP058_AC4_DeleteMissing: deleting a missing schedule in managed mode
// is a no-op with capellactl's wording.
func TestBucketBackupSchedule_CAP058_AC4_DeleteMissing(t *testing.T) {
	e := newOrdersEnv(t)
	r := e.must(t, "--yes", "bucket", "backup", "schedule", "delete", "nova-dev-01", "orders")
	wantContains(t, r.Stdout, "has no backup schedule; nothing to do")
	clitest.AssertNoWrites(t, e.s.Fake)
}

// TestBucketBackupSchedule_CAP058_AC5_Cycles: cycle list is newest first with its count and
// latest status; cycle get lists the backups of a cycle; an unknown cycle is refused (exit 3).
func TestBucketBackupSchedule_CAP058_AC5_Cycles(t *testing.T) {
	e := newOrdersEnv(t)
	f := e.s.Fake
	f.SeedBucketBackup(e.cid, "orders", "full", "scheduled", capellafake.WithField("cycleID", "cy-1"), capellafake.WithField("date", "2026-10-18T00:00:00Z"))
	f.SeedBucketBackup(e.cid, "orders", "incremental", "scheduled", capellafake.WithField("cycleID", "cy-1"), capellafake.WithField("date", "2026-10-19T00:00:00Z"))
	f.SeedBucketBackup(e.cid, "orders", "full", "scheduled", capellafake.WithField("cycleID", "cy-2"), capellafake.WithField("date", "2026-10-25T00:00:00Z"))
	f.SeedBucketBackup(e.cid, "orders", "incremental", "manual", capellafake.WithField("cycleID", "cy-2"),
		capellafake.WithField("date", "2026-10-26T00:00:00Z"), capellafake.WithField("status", "failed"))

	lines := strings.Split(strings.TrimRight(e.must(t, "bucket", "backup", "cycle", "list", "nova-dev-01", "orders").Stdout, "\n"), "\n")
	wantFields(t, lines[0], "STARTED", "BACKUPS", "LATEST", "STATUS", "ID")
	wantFields(t, lines[1], "2026-10-25", "00:00", "2", "failed", "cy-2")
	wantFields(t, lines[2], "2026-10-18", "00:00", "2", "ready", "cy-1")

	lines = strings.Split(strings.TrimRight(e.must(t, "bucket", "backup", "cycle", "get", "nova-dev-01", "orders", "cy-2").Stdout, "\n"), "\n")
	wantFields(t, lines[0], "DATE", "METHOD", "SOURCE", "STATUS", "SIZE", "ITEMS", "EXPIRES", "ID")
	if got := fields(lines[1])[2:5]; !reflect.DeepEqual(got, []string{"incremental", "manual", "failed"}) {
		t.Errorf("row 1 = %v", got)
	}
	if got := fields(lines[2])[2:5]; !reflect.DeepEqual(got, []string{"full", "scheduled", "ready"}) {
		t.Errorf("row 2 = %v", got)
	}
	wantCode(t, e.run("bucket", "backup", "cycle", "get", "nova-dev-01", "orders", "nope"), 3, "cycle nope of orders not found")
}

// TestBucketBackupSchedule_CAP058_AC6_ApplySyncDrift: an undeclared schedule is left alone by
// plan and shown by drift; sync records it; a change in the Capella UI shows in drift and apply
// restores the file's value; a deleted schedule is recreated by apply.
func TestBucketBackupSchedule_CAP058_AC6_ApplySyncDrift(t *testing.T) {
	e := newOrdersEnv(t)
	ctx := context.Background()
	c := e.s.Fake.Client()
	ws := map[string]any{"dayOfWeek": "monday", "startAt": 0, "incrementalEvery": 24, "retentionTime": "30days", "costOptimizedRetention": false}
	if err := c.CreateBucketBackupSchedule(ctx, e.pid, e.cid, ordersBucketID, capellaapi.Object{"type": "weekly", "weeklySchedule": ws}); err != nil {
		t.Fatal(err)
	}
	r := e.must(t, "plan")
	wantContains(t, r.Stdout, planNoChanges)
	if strings.Contains(r.Stdout, "+ bucket-backup-schedule") || strings.Contains(r.Stdout, "~ bucket-backup-schedule") ||
		strings.Contains(r.Stdout, "- bucket-backup-schedule") {
		t.Errorf("plan changes the undeclared schedule:\n%s", r.Stdout)
	}
	wantContains(t, e.must(t, "drift").Stdout, "bucket-backup-schedule nova-dev-01/orders  weekly full on monday")
	e.must(t, "sync")
	wantYAML(t, ordersSchedule(t, e.s), `{dayOfWeek: monday, startAt: 0, incrementalEvery: 24, retentionTime: 30days, costOptimizedRetention: false}`)
	e.s.Fake.ResetCalls()
	wantContains(t, e.must(t, "--yes", "apply").Stdout, "No changes")
	clitest.AssertNoWrites(t, e.s.Fake)

	ws["startAt"] = 5 // changed in the Capella UI
	if err := c.UpdateBucketBackupSchedule(ctx, e.pid, e.cid, ordersBucketID, capellaapi.Object{"type": "weekly", "weeklySchedule": ws}); err != nil {
		t.Fatal(err)
	}
	wantContains(t, e.must(t, "drift").Stdout, "startAt")
	e.must(t, "--yes", "apply")
	if got := e.ordersSchedule(t); got == nil || intOf(got["weeklySchedule"].(map[string]any)["startAt"]) != 0 {
		t.Errorf("after apply = %v, want startAt 0", got)
	}

	if err := c.DeleteBucketBackupSchedule(ctx, e.pid, e.cid, ordersBucketID); err != nil {
		t.Fatal(err)
	}
	e.must(t, "--yes", "apply")
	if e.ordersSchedule(t) == nil {
		t.Error("apply did not recreate the deleted schedule")
	}
}

// TestBucketBackupSchedule_CAP058_AC7_Help: schedule and cycle verbs have examples, and the guide
// covers bucket backups.
func TestBucketBackupSchedule_CAP058_AC7_Help(t *testing.T) {
	s := newScenario(t)
	for _, args := range [][]string{{"schedule", "show"}, {"schedule", "set"}, {"schedule", "delete"}, {"cycle", "list"}, {"cycle", "get"}} {
		r := mustScenario(t, s.Run(append(append([]string{"capella", "bucket", "backup"}, args...), "--help")...), args...)
		wantContains(t, r.Stdout, "Examples:", "cbctl capella bucket backup")
	}
	wantContains(t, mustScenario(t, s.Run("capella", "guide", "backup"), "guide backup").Stdout, "Bucket backup")
}
