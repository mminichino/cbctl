package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
)

var bashFence = regexp.MustCompile("(?s)```(\\w*)[^\\n]*\\n(.*?)```")

func readRepoDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(findRepoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// docSection returns the text from heading to the next "## " heading.
func docSection(t *testing.T, text, heading string) string {
	t.Helper()
	i := strings.Index(text, heading)
	if i < 0 {
		t.Fatalf("heading %q not found", heading)
	}
	rest := text[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return text[i : i+len(heading)+j]
	}
	return text[i:]
}

// guideCommands returns the cbctl invocations of the ```bash blocks in text, one per line.
func guideCommands(text string) []string {
	var out []string
	for _, m := range bashFence.FindAllStringSubmatch(text, -1) {
		if m[1] != "bash" {
			continue
		}
		for _, line := range strings.Split(strings.ReplaceAll(m[2], "\\\n", " "), "\n") {
			line = strings.TrimSpace(regexp.MustCompile(`\s+#.*$`).ReplaceAllString(line, ""))
			if strings.HasPrefix(line, "cbctl ") {
				out = append(out, line)
			}
		}
	}
	return out
}

// TestBackupGuide_CAP061_AC1_StepsRun: every command of docs/capella/backup-restore.md runs, in
// order, against the fake, and leaves the schedules, backups, restores and export it describes.
func TestBackupGuide_CAP061_AC1_StepsRun(t *testing.T) {
	commands := guideCommands(readRepoDoc(t, "docs/capella/backup-restore.md"))
	if len(commands) < 15 {
		t.Fatalf("only %d commands in the guide", len(commands))
	}
	s := newScenario(t)
	s.Fake.SettlePolls(0)
	f := s.Fake
	pid := f.SeedProject("sandbox")
	cid := f.SeedCluster(pid, "prod-01", "healthy")
	copyID := f.SeedCluster(pid, "prod-01-copy", "healthy", capellafake.WithField("cloudProvider",
		map[string]any{"type": "aws", "region": "us-east-1", "cidr": "10.1.0.0/20"}))
	f.SeedBucket(cid, "orders")
	f.SeedBucket(copyID, "orders")
	f.SeedSnapshot(cid, "2026-10-28T02:00:00Z", 0, "")
	mustScenario(t, s.Run("capella", "sync"), "sync")

	newest := func(args ...string) string {
		var rows []map[string]any
		r := mustScenario(t, s.Run(append([]string{"capella", "-o", "json"}, args...)...), args...)
		if err := json.Unmarshal([]byte(r.Stdout), &rows); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		key := "createdAt"
		if args[0] == "bucket" {
			key = "date"
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i][key].(string) > rows[j][key].(string) })
		if len(rows) == 0 {
			return "none"
		}
		return rows[0]["id"].(string)
	}
	for _, line := range commands {
		// The reader copies IDs from the lists; use the newest of each kind.
		line = strings.ReplaceAll(line, "CLUSTER_BACKUP_ID", newest("cluster", "backup", "list", "prod-01"))
		line = strings.ReplaceAll(line, "BUCKET_BACKUP_ID", newest("bucket", "backup", "list", "prod-01", "orders"))
		words := strings.Fields(strings.TrimPrefix(line, "cbctl ")) // capella VERB ...
		r := s.Run(append([]string{words[0], "--yes"}, words[1:]...)...)
		if r.Code != 0 {
			t.Fatalf("%s: exit %d\nstdout: %s\nstderr: %s", line, r.Code, r.Stdout, r.Stderr)
		}
	}

	wantContains(t, mustScenario(t, s.Run("capella", "cluster", "backup", "schedule", "show", "prod-01"), "show").Stdout, "every 24 h")
	wantContains(t, mustScenario(t, s.Run("capella", "bucket", "backup", "schedule", "show", "prod-01", "orders"), "show").Stdout,
		"incremental every 12 h")
	if len(f.Calls("POST", clusterPath+"/cloudsnapshotbackups")) == 0 {
		t.Error("no on-demand cluster backup")
	}
	if len(f.Calls("POST", clusterPath+"/buckets/*/backups")) == 0 {
		t.Error("no on-demand bucket backup")
	}
	var targets []string
	for _, c := range f.Calls("POST", clusterPath+"/backups/*/restore") {
		targets = append(targets, c.Body.(map[string]any)["targetClusterID"].(string))
	}
	if strings.Join(targets, ",") != cid+","+copyID {
		t.Errorf("bucket restore targets = %v, want [%s %s]", targets, cid, copyID)
	}
	if n := len(f.Calls("POST", clusterPath+"/cloudsnapshotbackups/*/restore")); n != 1 {
		t.Errorf("cluster restores = %d, want 1", n)
	}
	if len(f.Calls("POST", clusterPath+"/buckets/*/backups/*/export")) == 0 {
		t.Error("no export")
	}
}

var plannedNote = regexp.MustCompile(`(?i)planned for (release )?1\.0\.0|disk type \(planned\)|CAP-061\)? turns them`)

// TestBackupGuide_CAP061_AC3_NoPlannedNotes: no guide still calls backups or disk types planned,
// and every command block of the backup guide and the disk section is a ```bash block.
func TestBackupGuide_CAP061_AC3_NoPlannedNotes(t *testing.T) {
	root := findRepoRoot(t)
	docs, err := filepath.Glob(filepath.Join(root, "docs", "capella", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	topics, err := filepath.Glob(filepath.Join(root, "internal", "guide", "topics", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(append(docs, topics...), filepath.Join(root, "README.md")) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if m := plannedNote.FindString(string(b)); m != "" {
			t.Errorf("%s still says %q", filepath.Base(p), m)
		}
	}
	backup := readRepoDoc(t, "docs/capella/backup-restore.md")
	disk := docSection(t, readRepoDoc(t, "docs/capella/custom-clusters.md"), "## 4. Choose each group's disk")
	for name, text := range map[string]string{"backup-restore.md": backup, "disk section": disk} {
		langs := map[string]bool{}
		for _, m := range bashFence.FindAllStringSubmatch(text, -1) {
			if strings.HasPrefix(strings.TrimSpace(m[2]), "cbctl ") {
				langs[m[1]] = true
			}
		}
		if len(langs) != 1 || !langs["bash"] {
			t.Errorf("%s: command blocks use %v, want only bash", name, langs)
		}
	}
}

// TestBackupGuide_CAP061_AC4_Linked: the backup guide and the disk section are linked from the
// README, getting started, the backup and scaling topics and the runbook, which has every backup
// heading.
func TestBackupGuide_CAP061_AC4_Linked(t *testing.T) {
	wantContains(t, readRepoDoc(t, "README.md"), "[Backup and restore](docs/capella/backup-restore.md)")
	gs := readRepoDoc(t, "docs/capella/getting-started.md")
	wantContains(t, docSection(t, gs, "### Manage a provisioned cluster"), "[Backup and restore](backup-restore.md)")
	wantContains(t, gs, "custom-clusters.md#4-choose-each-groups-disk")
	wantContains(t, readRepoDoc(t, "internal/guide/topics/backup.md"), "docs/capella/backup-restore.md")
	wantContains(t, readRepoDoc(t, "internal/guide/topics/scaling.md"), "cbctl capella cluster disk-types")
	runbook := readRepoDoc(t, "docs/capella/runbook.md")
	for _, h := range []string{"## Change a disk type", "## Set a cluster backup schedule", "## Set a bucket backup schedule",
		"## Take a cluster backup before a change", "## Restore a cluster from a backup",
		"## Restore a bucket from a backup", "## Download a bucket backup", "## Backups"} {
		wantContains(t, runbook, h)
	}
	wantContains(t, runbook, "[backup-restore.md](backup-restore.md)")
}
