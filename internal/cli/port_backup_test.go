package cli_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
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
	wantContains(t, r.Stdout, "+ backup-schedule nova-dev-01  every 24 h · kept 30 days")
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
	wantContains(t, r.Stdout, "every 24 h · kept 30 days · first at 02:00 UTC · copies: us-west-2", "next backup: ")
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
	wantContains(t, e.must(t, "drift").Stdout, "backup-schedule nova-dev-01  every 24 h · kept 30 days")
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
