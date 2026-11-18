package capellafile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "capella.yaml")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func openText(t *testing.T, text string) *Editor {
	t.Helper()
	ed, err := OpenEditor(writeTemp(t, text))
	if err != nil {
		t.Fatalf("OpenEditor: %v", err)
	}
	return ed
}

func editedText(t *testing.T, ed *Editor) string {
	t.Helper()
	text, err := ed.Text()
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestEditorRoundTripOfCbctlWrittenFilesIsByteIdentical(t *testing.T) {
	files, _ := filepath.Glob("testdata/valid/*.yaml")
	for _, f := range files {
		rel := filepath.Join("valid", filepath.Base(f))
		t.Run(filepath.Base(f), func(t *testing.T) {
			written, err := Dump(fixtureObj(t, rel))
			if err != nil {
				t.Fatal(err)
			}
			ed := openText(t, written)
			if got := editedText(t, ed); got != written {
				t.Errorf("re-saved text differs\n--- written ---\n%s--- re-saved ---\n%s", written, got)
			}
			// And once more, to be sure it is a fixed point.
			again := openText(t, editedText(t, ed))
			if got := editedText(t, again); got != written {
				t.Errorf("second round differs")
			}
		})
	}
}

func TestEditorKeepsCommentsAndKeyOrderOfHandEditedFiles(t *testing.T) {
	src, err := os.ReadFile("testdata/valid/capellactl-example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ed := openText(t, string(src))
	_, cluster, err := ed.FindCluster("nova-dev-01", "")
	if err != nil || cluster == nil {
		t.Fatalf("FindCluster = %v, %v", cluster, err)
	}
	group, err := ed.Group(cluster, "")
	if err != nil {
		t.Fatal(err)
	}
	ed.Set(group, []string{"numOfNodes"}, 5)
	text := editedText(t, ed)

	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		idx := strings.Index(trimmed, "#")
		if idx < 0 {
			continue
		}
		comment := strings.TrimSpace(trimmed[idx:])
		// Spacing before a trailing comment is normalized; the comment text must survive.
		if !strings.Contains(text, comment) {
			t.Errorf("comment lost: %q", comment)
		}
	}
	keys := []string{"description:", "deletionProtection:", "configurationType:", "cloudProvider:", "availability:", "support:", "serviceGroups:", "hibernation:", "allowedCidrs:", "buckets:", "databaseCredentials:"}
	last := strings.Index(text, "- name: nova-dev-01")
	for _, k := range keys {
		at := strings.Index(text, "\n    "+k)
		if at < 0 || at < last {
			t.Errorf("key %s moved or vanished (at %d, previous key at %d)", k, at, last)
		}
		last = at
	}
	if !strings.Contains(text, "numOfNodes: 5") || strings.Contains(text, "numOfNodes: 3") {
		t.Errorf("edit not applied:\n%s", text)
	}
	if _, err := LoadText(text, "capella.yaml", filepath.Join("testdata", "valid"), testEnv(nil)); err != nil {
		t.Errorf("edited file no longer loads: %v", err)
	}
}

func TestEditorEnsureProjectsConvertsSingleProjectForm(t *testing.T) {
	src := `# header comment
apiVersion: cbctl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: nova-dev   # the project
clusters:
  # first cluster
  - name: nova-dev-01
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.10.0.0/24 }
`
	ed := openText(t, src)
	if got := ed.ProjectNames(); len(got) != 1 || got[0] != "nova-dev" {
		t.Fatalf("ProjectNames = %v", got)
	}
	ed.EnsureProjects()
	ed.EnsureProjects() // idempotent
	text := editedText(t, ed)
	for _, want := range []string{"# header comment", "# the project", "# first cluster", "projects:", "  - name: nova-dev", "    clusters:"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "\nproject:") || strings.Contains(text, "\nclusters:") {
		t.Errorf("single-project keys remain:\n%s", text)
	}
	var doc Obj
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	projects := doc["projects"].([]any)
	cl := projects[0].(map[string]any)["clusters"].([]any)
	if len(cl) != 1 || cl[0].(map[string]any)["name"] != "nova-dev-01" {
		t.Errorf("clusters not moved under the project: %v", doc)
	}
}

func TestEditorEnsureProjectsOnAnEmptyDocument(t *testing.T) {
	ed := openText(t, "apiVersion: cbctl/v1\n")
	ed.AddProject(Obj{"name": "new"})
	text := editedText(t, ed)
	if text != "apiVersion: cbctl/v1\nprojects:\n  - name: new\n" {
		t.Errorf("text = %q", text)
	}
	if got := ed.ProjectNames(); len(got) != 1 || got[0] != "new" {
		t.Errorf("ProjectNames = %v", got)
	}
	empty := openText(t, "")
	empty.AddProject(Obj{"name": "x"})
	if !strings.HasPrefix(editedText(t, empty), "projects:\n  - name: x") {
		t.Errorf("empty file: %q", editedText(t, empty))
	}
}

func TestEditorProjectAndClusterLookup(t *testing.T) {
	src, _ := os.ReadFile("testdata/valid/multi-project.yaml")
	ed := openText(t, string(src))
	if got := strings.Join(ed.ProjectNames(), ","); got != "alpha,beta" {
		t.Errorf("ProjectNames = %q", got)
	}
	if _, ok := ed.FindProject("beta"); !ok {
		t.Error("FindProject(beta)")
	}
	if _, ok := ed.FindProject("gamma"); ok {
		t.Error("FindProject(gamma) should not exist")
	}

	_, _, err := ed.FindCluster("shared-name", "")
	var amb *AmbiguousError
	if !errors.As(err, &amb) || amb.Name != "shared-name" || strings.Join(amb.Projects, ",") != "alpha,beta" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "pass --project NAME") {
		t.Errorf("message = %q", err.Error())
	}
	proj, cl, err := ed.FindCluster("shared-name", "beta")
	if err != nil || cl == nil {
		t.Fatalf("FindCluster(beta) = %v, %v", cl, err)
	}
	if v, _ := mapGet(proj, "name"); v.Value != "beta" {
		t.Errorf("project = %s", v.Value)
	}
	if p, c, err := ed.FindCluster("nope", ""); p != nil || c != nil || err != nil {
		t.Errorf("missing cluster = %v %v %v", p, c, err)
	}
	if p, c, err := ed.FindCluster("shared-name", "gamma"); p != nil || c != nil || err != nil {
		t.Errorf("unknown project = %v %v %v", p, c, err)
	}
}

func TestEditorAddAndRemoveCluster(t *testing.T) {
	for _, form := range []string{"single", "projects"} {
		t.Run(form, func(t *testing.T) {
			src := "apiVersion: cbctl/v1\nproject:\n  name: p\n"
			if form == "projects" {
				src = "apiVersion: cbctl/v1\nprojects:\n  - name: p\n"
			}
			ed := openText(t, src)
			proj, ok := ed.FindProject("p")
			if !ok {
				t.Fatal("project not found")
			}
			ed.AddCluster(proj, Obj{"name": "c1", "cloudProvider": Obj{"type": "aws", "region": "us-east-1"}})
			ed.AddCluster(proj, Obj{"name": "c2"})
			text := editedText(t, ed)
			if strings.Count(text, "- name: c") != 2 {
				t.Fatalf("clusters not added:\n%s", text)
			}
			if form == "single" && !strings.Contains(text, "\nclusters:\n") {
				t.Errorf("single-project form should keep top-level clusters:\n%s", text)
			}
			if _, c, err := ed.FindCluster("c2", ""); err != nil || c == nil {
				t.Errorf("FindCluster after add: %v %v", c, err)
			}
			ed.RemoveCluster(proj, "c1")
			ed.RemoveCluster(proj, "does-not-exist")
			text = editedText(t, ed)
			if strings.Contains(text, "c1") || !strings.Contains(text, "c2") {
				t.Errorf("RemoveCluster:\n%s", text)
			}
			ed.RemoveCluster(proj, "c2")
			if _, err := LoadText(editedText(t, ed), "capella.yaml", t.TempDir(), nil); err != nil {
				t.Errorf("file with no clusters should load: %v", err)
			}
		})
	}
}

func TestEditorGroup(t *testing.T) {
	src := `apiVersion: cbctl/v1
projects:
  - name: p
    clusters:
      - name: c
        serviceGroups:
          - services: [data, index]
            numOfNodes: 3
          - services: [query]
            numOfNodes: 2
      - name: two-data
        serviceGroups:
          - services: [data]
          - services: [data, search]
      - name: no-data
        serviceGroups:
          - services: [query]
`
	ed := openText(t, src)
	_, c, _ := ed.FindCluster("c", "")
	g, err := ed.Group(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mapGet(g, "numOfNodes"); v.Value != "3" {
		t.Errorf("data group = %v", v.Value)
	}
	if g, err := ed.Group(c, " query "); err != nil || scalarString(mustGet(g, "numOfNodes")) != "2" {
		t.Errorf("Group(query) = %v, %v", g, err)
	}
	if g, err := ed.Group(c, "index,data"); err != nil || scalarString(mustGet(g, "numOfNodes")) != "3" {
		t.Errorf("Group(index,data) = %v, %v", g, err)
	}
	if _, err := ed.Group(c, "search"); err == nil || !strings.Contains(err.Error(), "no service group 'search'") || !strings.Contains(err.Error(), "data,index, query") {
		t.Errorf("err = %v", err)
	}
	_, ambiguous, _ := ed.FindCluster("two-data", "")
	if _, err := ed.Group(ambiguous, ""); err == nil || !strings.Contains(err.Error(), "more than one group runs data") {
		t.Errorf("err = %v", err)
	}
	_, none, _ := ed.FindCluster("no-data", "")
	if _, err := ed.Group(none, ""); err == nil {
		t.Error("cluster without a data group should fail")
	}
}

func mustGet(n *yaml.Node, key string) *yaml.Node {
	v, _ := mapGet(n, key)
	return v
}

func TestEditorSetAndDelete(t *testing.T) {
	src := `apiVersion: cbctl/v1
project:
  name: p
clusters:
  - name: c
    # keep me
    description: old   # trailing
    cloudProvider: { type: aws, region: us-east-1 }
    buckets:
      - name: b
        scopes:
          - name: s
`
	ed := openText(t, src)
	_, c, _ := ed.FindCluster("c", "")
	ed.Set(c, []string{"description"}, "new")
	ed.Set(c, []string{"deletionProtection"}, true)
	ed.Set(c, []string{"support", "plan"}, "enterprise")
	ed.Set(c, []string{"hibernation", "timezone"}, "US/Pacific")
	ed.Set(c, []string{"id"}, "abc")
	text := editedText(t, ed)
	for _, want := range []string{"# keep me", "# trailing", "description: new", "deletionProtection: true", "plan: enterprise", "id: abc"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	pos := func(s string) int { return strings.Index(text, s) }
	if !(pos("id: abc") < pos("name: c") && pos("description: new") < pos("cloudProvider") && pos("cloudProvider") < pos("support:") &&
		pos("support:") < pos("deletionProtection") && pos("deletionProtection") < pos("hibernation:") && pos("hibernation:") < pos("buckets:")) {
		t.Errorf("new keys are not in canonical order:\n%s", text)
	}
	ed.Delete(c, "deletionProtection")
	ed.Delete(c, "no-such-key")
	if strings.Contains(editedText(t, ed), "deletionProtection") {
		t.Error("Delete did not remove the key")
	}
	// Bucket keys use the bucket order.
	_, bucketCluster, _ := ed.FindCluster("c", "")
	bucket := bucketCluster.Content[1+indexOfKey(bucketCluster, "buckets")].Content[0]
	ed.Set(bucket, []string{"replicas"}, 2)
	ed.Set(bucket, []string{"memoryAllocationInMb"}, 512)
	ed.Set(bucket, []string{"backupSchedule", "interval"}, 24)
	text = editedText(t, ed)
	pos = func(s string) int { return strings.Index(text, s) }
	if !(pos("memoryAllocationInMb") < pos("replicas") && pos("replicas") < pos("backupSchedule") && pos("backupSchedule") < pos("scopes")) {
		t.Errorf("bucket keys out of order:\n%s", text)
	}
}

func indexOfKey(m *yaml.Node, key string) int {
	_, i := mapGet(m, key)
	return i
}

func TestPutKeyOrdering(t *testing.T) {
	order := []string{"a", "b", "c", "d"}
	tests := []struct {
		name string
		have []string
		put  string
		want string
	}{
		{"before the first later key", []string{"a", "c", "d"}, "b", "a,b,c,d"},
		{"at the front", []string{"b", "c"}, "a", "a,b,c"},
		{"at the end when nothing is later", []string{"a", "b"}, "c", "a,b,c"},
		{"unlisted key goes last", []string{"a", "b"}, "zzz", "a,b,zzz"},
		{"before a later key even after unlisted ones", []string{"x", "c"}, "b", "x,b,c"},
		{"existing key keeps its place", []string{"b", "a"}, "b", "b,a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for _, k := range tt.have {
				n.Content = append(n.Content, scalarNode(k), scalarNode("v"))
			}
			PutKey(n, tt.put, "new", order)
			var got []string
			for i := 0; i < len(n.Content); i += 2 {
				got = append(got, n.Content[i].Value)
			}
			if strings.Join(got, ",") != tt.want {
				t.Errorf("keys = %v, want %s", got, tt.want)
			}
			if v, _ := mapGet(n, tt.put); v.Value != "new" {
				t.Errorf("value = %q", v.Value)
			}
		})
	}
}

func TestEditorSetIDAndClearID(t *testing.T) {
	src, _ := os.ReadFile("testdata/valid/multi-project.yaml")
	ed := openText(t, string(src))
	if !ed.SetID("project", "", "alpha", "pid-1") || !ed.SetID("cluster", "alpha", "shared-name", "cid-1") {
		t.Fatal("SetID did not find the resources")
	}
	if ed.SetID("cluster", "", "shared-name", "x") {
		t.Error("SetID on an ambiguous cluster must not guess")
	}
	if ed.SetID("cluster", "alpha", "nope", "x") || ed.SetID("project", "", "nope", "x") {
		t.Error("SetID on a missing resource must report false")
	}
	text := editedText(t, ed)
	if !strings.Contains(text, "  - id: pid-1\n    name: alpha") || !strings.Contains(text, "      - id: cid-1\n        name: shared-name") {
		t.Errorf("ids are not the first keys:\n%s", text)
	}
	ed.SetID("cluster", "alpha", "shared-name", "cid-2")
	if strings.Contains(editedText(t, ed), "cid-1") || !strings.Contains(editedText(t, ed), "cid-2") {
		t.Error("SetID must replace an existing id")
	}
	if !ed.ClearID("cluster", "alpha", "shared-name") || !ed.ClearID("project", "", "alpha") {
		t.Fatal("ClearID did not find the resources")
	}
	if strings.Contains(editedText(t, ed), "pid-1") || strings.Contains(editedText(t, ed), "cid-2") {
		t.Error("ClearID left the ids")
	}
}

func TestEditorSave(t *testing.T) {
	path := writeTemp(t, "apiVersion: cbctl/v1\nprojects: []\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	ed, err := OpenEditor(path)
	if err != nil {
		t.Fatal(err)
	}
	ed.AddProject(Obj{"name": "p"})
	text := editedText(t, ed)
	backup, err := ed.Save(text)
	if err != nil {
		t.Fatal(err)
	}
	if backup != path+".bak" {
		t.Errorf("backup = %q", backup)
	}
	if got, _ := os.ReadFile(backup); string(got) != "apiVersion: cbctl/v1\nprojects: []\n" {
		t.Errorf(".bak = %q", got)
	}
	if got, _ := os.ReadFile(path); string(got) != text {
		t.Errorf("file = %q", got)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(backup); info.Mode().Perm() != 0o600 {
			t.Errorf(".bak mode = %v", info.Mode().Perm())
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
			t.Errorf("file mode = %v, want the original 0640", info.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	// Saving unchanged text is a no-op and leaves the .bak alone.
	if err := os.WriteFile(backup, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := ed.Save(text); err != nil || b != "" {
		t.Errorf("no-op Save = %q, %v", b, err)
	}
	if got, _ := os.ReadFile(backup); string(got) != "sentinel" {
		t.Error("no-op Save rewrote the .bak")
	}

	// A second save keeps the text of the first as the new .bak (overwrites).
	ed.AddProject(Obj{"name": "q"})
	second := editedText(t, ed)
	if b, err := ed.Save(second); err != nil || b != backup {
		t.Fatalf("second Save = %q, %v", b, err)
	}
	if got, _ := os.ReadFile(backup); string(got) != text {
		t.Errorf("second .bak = %q", got)
	}
}

func TestEditorSaveRefusesWhenTheFileChangedOnDisk(t *testing.T) {
	path := writeTemp(t, "apiVersion: cbctl/v1\n")
	ed, err := OpenEditor(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("apiVersion: cbctl/v1\n# edited by someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ed.AddProject(Obj{"name": "p"})
	_, err = ed.Save(editedText(t, ed))
	var ce *ChangedError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "capella.yaml changed while the command was running; nothing was written") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "edited by someone else") {
		t.Error("file was overwritten")
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error(".bak must not be written when nothing was written")
	}
}

func TestEditorSaveRestoresPreviousText(t *testing.T) {
	original := "# mine\napiVersion: cbctl/v1\nprojects: []\n"
	path := writeTemp(t, original)
	ed, _ := OpenEditor(path)
	if _, err := ed.Save("apiVersion: cbctl/v1\nprojects:\n  - name: p\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := ed.Save(original); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("restored file = %q", got)
	}
}

func TestEditorSaveCreatesMissingFile(t *testing.T) {
	path := writeTemp(t, "x: 1\n")
	ed, _ := OpenEditor(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	backup, err := ed.Save("apiVersion: cbctl/v1\n")
	if err != nil || backup != "" {
		t.Fatalf("Save = %q, %v", backup, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "apiVersion: cbctl/v1\n" {
		t.Errorf("file = %q", got)
	}
}

func TestOpenEditorRefusesAnchorsAndAliases(t *testing.T) {
	src := strings.Replace(minimalYAML, "compute: { cpu: 4, ram: 16 }", "compute: &c { cpu: 4, ram: 16 }", 1)
	_, err := OpenEditor(writeTemp(t, src))
	var ae *AnchorError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v", err)
	}
	want := "capella.yaml uses YAML anchors/aliases, which cbctl cannot edit safely; edit the file by hand or remove them"
	if err.Error() != want {
		t.Errorf("message = %q", err.Error())
	}
}

func TestOpenEditorErrors(t *testing.T) {
	if _, err := OpenEditor(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("missing file should fail")
	}
	_, err := OpenEditor(writeTemp(t, "a: [1, 2\n"))
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Errorf("syntax error = %v", err)
	}
	_, err = OpenEditor(writeTemp(t, "- a\n- b\n"))
	if !errors.As(err, &ce) {
		t.Errorf("non-mapping = %v", err)
	}
}

func TestOpenEditorStripsTemplateComments(t *testing.T) {
	src := "apiVersion: cbctl/v1\nprojects:\n  - name: p\n" + NewClusterTemplate +
		"# This project has no clusters yet. To create one, run: capellactl cluster create NAME\n"
	ed := openText(t, src)
	if text := editedText(t, ed); strings.Contains(text, "no clusters yet") {
		t.Errorf("template not stripped:\n%s", text)
	}
	if ed.Original != src {
		t.Error("Original must stay the on-disk text")
	}
	empty := openText(t, "apiVersion: cbctl/v1\n"+NoProjectsTemplate)
	if text := editedText(t, empty); strings.Contains(text, "No projects left") || strings.Contains(text, "add an existing one") {
		t.Errorf("no-projects template not stripped:\n%s", text)
	}
}

func TestEditorOutputLoadsAfterTypicalCommandEdits(t *testing.T) {
	ed := openText(t, minimalYAML)
	proj, _ := ed.FindProject("nova-dev")
	ed.AddCluster(proj, Obj{
		"name":          "second",
		"cloudProvider": Obj{"type": "aws", "region": "us-east-1", "cidr": "10.11.0.0/24"},
		"availability":  Obj{"type": "multi"},
		"support":       Obj{"plan": "basic", "timezone": "PT"},
		"serviceGroups": []any{Obj{
			"node":       Obj{"compute": Obj{"cpu": 4, "ram": 16}, "disk": Obj{"type": "gp3", "storage": 50, "iops": 3000}},
			"numOfNodes": 3, "services": []string{"data"},
		}},
	})
	cfg, err := LoadText(editedText(t, ed), "capella.yaml", t.TempDir(), testEnv(nil))
	if err != nil {
		t.Fatalf("%v\n%s", err, editedText(t, ed))
	}
	if cfg.Counts().Clusters != 2 {
		t.Errorf("clusters = %d", cfg.Counts().Clusters)
	}
}

func TestEditorRemoveProject(t *testing.T) {
	ed := openText(t, "apiVersion: cbctl/v1\nproject:\n  name: a\nclusters: []\n")
	if ed.RemoveProject("missing") {
		t.Fatal("removed a project that is not there")
	}
	if !ed.RemoveProject("a") {
		t.Fatal("project a not removed")
	}
	text, err := ed.Text()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "name: a") || !strings.Contains(text, "projects: []") {
		t.Errorf("unexpected text:\n%s", text)
	}

	ed = openText(t, "apiVersion: cbctl/v1\nprojects:\n  - name: a\n  - name: b\n")
	ed.RemoveProject("a")
	if got := ed.ProjectNames(); len(got) != 1 || got[0] != "b" {
		t.Errorf("projects = %v", got)
	}
}
