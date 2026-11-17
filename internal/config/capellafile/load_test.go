package capellafile

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mminichino/cbctl/internal/redact"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/invalid")

const goodPassword = "Abcdef1!xyz"

// testEnv resolves the variables the fixtures use. Anything else is unset.
func testEnv(extra map[string]string) func(string) string {
	vars := map[string]string{
		"CAPELLA_ORG_ID":      "org-1",
		"ORDERS_APP_PASSWORD": goodPassword,
		"APP_PASSWORD":        goodPassword,
		"APP_PW":              goodPassword,
		"WEAK_PW":             "abc",
	}
	for k, v := range extra {
		vars[k] = v
	}
	return func(k string) string { return vars[k] }
}

func loadFixture(t *testing.T, rel string) (*Config, error) {
	t.Helper()
	return Load(filepath.Join("testdata", rel), testEnv(nil))
}

func mustLoadText(t *testing.T, text string) *Config {
	t.Helper()
	cfg, err := LoadText(text, "capella.yaml", t.TempDir(), testEnv(nil))
	if err != nil {
		t.Fatalf("LoadText: %v", err)
	}
	return cfg
}

func loadErr(t *testing.T, text string) *ConfigError {
	t.Helper()
	_, err := LoadText(text, "capella.yaml", t.TempDir(), testEnv(nil))
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ConfigError, got %v", err)
	}
	return ce
}

const minimalYAML = `apiVersion: cbctl/v1
organizationId: ${CAPELLA_ORG_ID}
project:
  name: nova-dev
  description: Nova development environment
clusters:
  - name: nova-dev-01
    description: Nova development cluster
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.10.0.0/24 }
    availability: { type: multi }
    support: { plan: developer pro, timezone: PT }
    serviceGroups:
      - node:
          compute: { cpu: 4, ram: 16 }
          disk: { type: gp3, storage: 50, iops: 3000 }
        numOfNodes: 3
        services: [data, index, query]
`

func TestLoadValidFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/valid/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no valid fixtures: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			cfg, err := Load(f, testEnv(nil))
			if err != nil {
				t.Fatalf("Load(%s): %v", f, err)
			}
			if len(cfg.Projects()) == 0 {
				t.Fatalf("no projects loaded")
			}
			if !strings.HasPrefix(cfg.Summary(), filepath.Base(f)+" is valid: ") {
				t.Errorf("Summary() = %q", cfg.Summary())
			}
		})
	}
}

func TestLoadInvalidGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/invalid/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no invalid fixtures: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".yaml")
		t.Run(name, func(t *testing.T) {
			_, err := Load(f, testEnv(nil))
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("Load(%s) error = %v, want *ConfigError", f, err)
			}
			abs, err := filepath.Abs(filepath.Dir(f))
			if err != nil {
				t.Fatal(err)
			}
			got := strings.ReplaceAll(ce.Error(), abs, "<testdata>") + "\n"
			// The product prints native paths; goldens use '/' so they hold on every OS.
			got = strings.ReplaceAll(got, `<testdata>\`, "<testdata>/")
			got = strings.ReplaceAll(got, `secrets\`, "secrets/")
			golden := strings.TrimSuffix(f, ".yaml") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden %s (run with -update): %v", golden, err)
			}
			if got != string(want) {
				t.Errorf("error text mismatch for %s\n--- got ---\n%s--- want ---\n%s", f, got, want)
			}
			for _, issue := range ce.Issues {
				if issue.Message == "" || issue.Where == "" || issue.Path == "" {
					t.Errorf("incomplete issue: %+v", issue)
				}
			}
		})
	}
}

func TestInvalidFixturesCoverEveryRuleFamily(t *testing.T) {
	// Each substring must appear in at least one golden file, so a rule that stops firing is
	// noticed even when its fixture still fails for another reason.
	wants := []string{
		"invalid YAML", "is empty", "must be a mapping", "already defined", "missing required field 'apiVersion'",
		"unsupported apiVersion", "environment variable MISSING_VAR is not set", "not found or unreadable", "is empty (resolved to",
		"not a literal", "does not meet Capella rules", "must be between 3 and 27", "must be one of:", "unknown field 'descripton' (did you mean 'description'?)",
		"must be an integer", "use either projects:", "looks like a gcp region", "not available for free-tier clusters",
		"free-tier clusters on aws are available in", "only one free-tier cluster", "not available for free-tier buckets",
		"at least one service group must run the data service", "each group needs a different set of services",
		"additional service groups allow 2–24 nodes", "at most 27 nodes", "singleNode clusters have exactly 1 node",
		"singleNode clusters run only", "singleNode clusters must use availability: single", "exactly one service group",
		"zones can only be set for availability: single", "disk type P20 is not valid for aws", "storage is required for gcp",
		"iops is required for aws", "autoExpansion is for azure disks only", "disks take 3000–16000 IOPS", "disks are at most 16000 GB",
		"duplicate allowed CIDR name", "is not a valid CIDR", "is not a network address (did you mean 203.0.113.0/24?)",
		"is not an RFC 3339 time", "overlaps the network of cluster", "duplicate bucket name", "not allowed for ephemeral buckets",
		"is only valid for ephemeral buckets", "only allowed with storageBackend: magma", "replicas: 0 is only for single-node clusters",
		"single-node clusters only support replicas: 0", "is not valid: use 1–251 characters", "duplicate scope name",
		"duplicate collection name", "minute must be 00 or 30", "hour must be 00–23", "must start before it ends",
		"timezone is required", "cannot be off every day", "duplicate credential name", "which capella.yaml does not list",
		"is not defined in bucket", "is not defined in orders.inventory", "basic credentials support read/write only",
		"duplicate cluster name", "duplicate project name", "missing required field 'name'",
	}
	var all strings.Builder
	files, _ := filepath.Glob("testdata/invalid/*.golden")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	if all.Len() == 0 {
		t.Skip("goldens not generated yet")
	}
	for _, w := range wants {
		if !strings.Contains(all.String(), w) {
			t.Errorf("no golden file contains %q", w)
		}
	}
}

func TestLoadSetsPathSourceAndText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, testEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != path || cfg.Source != "my.yaml" || cfg.Text != minimalYAML {
		t.Errorf("Path=%q Source=%q", cfg.Path, cfg.Source)
	}
	if cfg.OrgID != "org-1" {
		t.Errorf("OrgID = %q", cfg.OrgID)
	}
	if got := cfg.CredentialsOutput(); got != filepath.Join(dir, ".cbctl", "credentials") {
		t.Errorf("CredentialsOutput() = %q", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "capella.yaml"), nil)
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ce.Error(), "not found") || !strings.Contains(ce.Error(), "cbctl capella sync") {
		t.Errorf("message: %s", ce.Error())
	}
}

func TestCredentialsOutput(t *testing.T) {
	// An absolute path needs a drive letter on Windows; "/var/secrets" is rooted but relative there.
	abs, absWant := "/var/secrets", "/var/secrets"
	if runtime.GOOS == "windows" {
		abs, absWant = `C:\secrets`, "C:/secrets"
	}
	tests := []struct {
		name, setting, want string
	}{
		{"default", "", "/base/.cbctl/credentials"},
		{"relative explicit", "credentialsOutput: ./out\n", "/base/out"},
		{"legacy namespace honoured", "credentialsOutput: ./.capellactl/credentials\n", "/base/.capellactl/credentials"},
		{"absolute", "credentialsOutput: '" + abs + "'\n", absWant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadText(tt.setting+minimalYAML, "capella.yaml", "/base", testEnv(nil))
			if err != nil {
				t.Fatal(err)
			}
			if got := filepath.ToSlash(cfg.CredentialsOutput()); got != tt.want {
				t.Errorf("CredentialsOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIVersion(t *testing.T) {
	tests := []struct {
		name, version string
		wantErr       string
	}{
		{"cbctl", "cbctl/v1", ""},
		{"capellactl accepted unchanged", "capellactl/v1", ""},
		{"future version", "cbctl/v2", "unsupported apiVersion cbctl/v2; supported values: cbctl/v1, capellactl/v1"},
		{"other tool", "terraform/v1", "unsupported apiVersion terraform/v1"},
		{"empty", `""`, "unsupported apiVersion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := strings.Replace(minimalYAML, "apiVersion: cbctl/v1", "apiVersion: "+tt.version, 1)
			_, err := LoadText(text, "capella.yaml", t.TempDir(), testEnv(nil))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ce *ConfigError
			if !errors.As(err, &ce) || !strings.Contains(ce.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
			if ce.Issues[0].Fix != "the file must start with apiVersion cbctl/v1 ('cbctl capella init' writes a new file)" || ce.Issues[0].Line != 1 {
				t.Errorf("issue = %+v", ce.Issues[0])
			}
		})
	}
}

func TestAPIVersionMissingHasFix(t *testing.T) {
	ce := loadErr(t, strings.Replace(minimalYAML, "apiVersion: cbctl/v1\n", "", 1))
	if len(ce.Issues) != 1 || ce.Issues[0].Message != "missing required field 'apiVersion'" || ce.Issues[0].Fix != "the file must start with apiVersion cbctl/v1 ('cbctl capella init' writes a new file)" {
		t.Errorf("issues = %+v", ce.Issues)
	}
}

func TestSecretReferences(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pw.txt", "Fileword1!abc\n")
	write("crlf.txt", "Crlfword1!abc\r\n")
	write("empty.txt", "\n")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("sub", "nested.txt"), "Nested1!abcd")

	cred := func(pw string) string {
		return minimalYAML + "    databaseCredentials:\n      - name: app\n        password: " + pw + "\n        access:\n          - privileges: [read]\n"
	}
	password := func(cfg *Config) string {
		return asString(objList(cfg.Projects()[0].Clusters[0]["databaseCredentials"])[0]["password"])
	}

	t.Run("env reference resolved and registered for redaction", func(t *testing.T) {
		redact.Reset()
		t.Cleanup(redact.Reset)
		cfg, err := LoadText(cred("${APP_PASSWORD}"), "capella.yaml", dir, testEnv(map[string]string{"APP_PASSWORD": "Uniq1!envpw"}))
		if err != nil {
			t.Fatal(err)
		}
		if password(cfg) != "Uniq1!envpw" {
			t.Errorf("password = %q", password(cfg))
		}
		if got := redact.String("the password is Uniq1!envpw ok"); strings.Contains(got, "Uniq1!envpw") {
			t.Errorf("resolved password was not registered with redact: %q", got)
		}
	})
	t.Run("file reference relative to baseDir, one trailing newline trimmed", func(t *testing.T) {
		cfg, err := LoadText(cred("${file:./pw.txt}"), "capella.yaml", dir, testEnv(nil))
		if err != nil {
			t.Fatal(err)
		}
		if password(cfg) != "Fileword1!abc" {
			t.Errorf("password = %q", password(cfg))
		}
	})
	t.Run("file reference with CRLF", func(t *testing.T) {
		cfg, err := LoadText(cred("${file:crlf.txt}"), "capella.yaml", dir, testEnv(nil))
		if err != nil {
			t.Fatal(err)
		}
		if password(cfg) != "Crlfword1!abc" {
			t.Errorf("password = %q", password(cfg))
		}
	})
	t.Run("file path is relative to the config file, not the working directory", func(t *testing.T) {
		cfg, err := LoadText(cred("${file:sub/nested.txt}"), "capella.yaml", dir, testEnv(nil))
		if err != nil {
			t.Fatal(err)
		}
		if password(cfg) != "Nested1!abcd" {
			t.Errorf("password = %q", password(cfg))
		}
	})
	t.Run("absolute file path", func(t *testing.T) {
		cfg, err := LoadText(cred("${file:"+filepath.Join(dir, "pw.txt")+"}"), "capella.yaml", t.TempDir(), testEnv(nil))
		if err != nil {
			t.Fatal(err)
		}
		if password(cfg) != "Fileword1!abc" {
			t.Errorf("password = %q", password(cfg))
		}
	})
	t.Run("same variable in two credentials", func(t *testing.T) {
		text := cred("${APP_PASSWORD}") + "      - name: second\n        password: ${APP_PASSWORD}\n        access:\n          - privileges: [read]\n"
		if _, err := LoadText(text, "capella.yaml", dir, testEnv(nil)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("password containing a literal ${ after resolution is not expanded again", func(t *testing.T) {
		cfg, err := LoadText(cred("${APP_PASSWORD}"), "capella.yaml", dir, testEnv(map[string]string{"APP_PASSWORD": "Ab1!${NOT_A_REF}x"}))
		if err != nil {
			t.Fatal(err)
		}
		if password(cfg) != "Ab1!${NOT_A_REF}x" {
			t.Errorf("password = %q", password(cfg))
		}
	})
	t.Run("embedded references resolve in non-password strings", func(t *testing.T) {
		text := strings.Replace(minimalYAML, "description: Nova development environment", "description: org ${CAPELLA_ORG_ID} env", 1)
		cfg := mustLoadText(t, text)
		if got := asString(cfg.Projects()[0].Data["description"]); got != "org org-1 env" {
			t.Errorf("description = %q", got)
		}
	})

	failures := []struct {
		name, pw, want string
	}{
		{"unset variable", "${MISSING}", "environment variable MISSING is not set"},
		{"empty variable", "${EMPTYVAR}", "environment variable EMPTYVAR is not set"},
		{"missing file", "${file:nope.txt}", "file nope.txt not found or unreadable"},
		{"empty file", "${file:empty.txt}", "file empty.txt is empty"},
		{"literal", "Abcdef1!xyz", "password must be a ${VAR} or ${file:PATH} reference, not a literal"},
		{"embedded in a literal", "x${APP_PASSWORD}", "not a literal"},
		{"empty string", `""`, "password must not be empty"},
		{"too weak", "${WEAK_PW}", "does not meet Capella rules"},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadText(cred(tt.pw), "capella.yaml", dir, testEnv(map[string]string{"EMPTYVAR": ""}))
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v", err)
			}
			text := ce.Error()
			if !strings.Contains(text, tt.want) {
				t.Errorf("error does not contain %q:\n%s", tt.want, text)
			}
			if !strings.Contains(text, "databaseCredentials[0].password") && !strings.Contains(text, "credential app") {
				t.Errorf("error does not locate the password:\n%s", text)
			}
			if !strings.Contains(text, "(line ") {
				t.Errorf("error has no line number:\n%s", text)
			}
		})
	}

	t.Run("errors never contain the secret value", func(t *testing.T) {
		_, err := LoadText(cred("${WEAK_PW}"), "capella.yaml", dir, testEnv(map[string]string{"WEAK_PW": "SecretValue-xyz"}))
		if err == nil || strings.Contains(err.Error(), "SecretValue-xyz") {
			t.Errorf("err = %v", err)
		}
		_, err = LoadText(cred("LiteralSecret1!"), "capella.yaml", dir, nil)
		if err == nil || strings.Contains(err.Error(), "LiteralSecret1!") {
			t.Errorf("literal password leaked: %v", err)
		}
	})
}

func TestOrganizationIDResolution(t *testing.T) {
	t.Run("resolved", func(t *testing.T) {
		if cfg := mustLoadText(t, minimalYAML); cfg.OrgID != "org-1" {
			t.Errorf("OrgID = %q", cfg.OrgID)
		}
	})
	t.Run("unset variable leaves the organization to the profile", func(t *testing.T) {
		cfg, err := Load("testdata/valid/org-unresolved.yaml", testEnv(nil))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OrgID != "" {
			t.Errorf("OrgID = %q, want empty", cfg.OrgID)
		}
		if _, present := cfg.Raw["organizationId"]; present {
			t.Error("unresolved organizationId should be dropped from Raw")
		}
	})
	t.Run("absent", func(t *testing.T) {
		cfg, err := Load("testdata/valid/free-tier.yaml", nil)
		if err != nil || cfg.OrgID != "" {
			t.Errorf("OrgID = %q err = %v", cfg.OrgID, err)
		}
	})
}

func TestDeclaredOrgID(t *testing.T) {
	dir := t.TempDir()
	write := func(text string) string {
		p := filepath.Join(dir, "c.yaml")
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name, text string
		env        map[string]string
		want       string
		wantErr    bool
	}{
		{"literal", "organizationId: abc\n", nil, "abc", false},
		{"expanded", "organizationId: ${ORG}\n", map[string]string{"ORG": "from-env"}, "from-env", false},
		{"embedded", "organizationId: pre-${ORG}\n", map[string]string{"ORG": "x"}, "pre-x", false},
		{"unset variable", "organizationId: ${ORG}\n", nil, "", false},
		{"file reference is unresolved", "organizationId: ${file:org.txt}\n", nil, "", false},
		{"absent", "apiVersion: cbctl/v1\n", nil, "", false},
		{"blank", "organizationId: \"  \"\n", nil, "", false},
		{"not validated: other garbage is fine", "organizationId: o1\nbogus: [1, 2\n  - x", nil, "", true},
		{"unknown keys are fine", "organizationId: o1\nbogus: 1\n", nil, "o1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeclaredOrgID(write(tt.text), testEnv(tt.env))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("DeclaredOrgID() = %q, %v; want %q (err %v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
	if _, err := DeclaredOrgID(filepath.Join(dir, "missing.yaml"), nil); err == nil {
		t.Error("missing file should be an error")
	}
}

func TestDefaults(t *testing.T) {
	text := minimalYAML + `    buckets:
      - name: orders
        memoryAllocationInMb: 100
      - name: explicit
        replicas: 2
        flushEnabled: true
`
	cfg := mustLoadText(t, text)
	cl := cfg.Projects()[0].Clusters[0]
	if cl["configurationType"] != "multiNode" || cl["tier"] != "provisioned" {
		t.Errorf("cluster defaults: configurationType=%v tier=%v", cl["configurationType"], cl["tier"])
	}
	b := objList(cl["buckets"])[0]
	want := Obj{"type": "couchbase", "replicas": int64(1), "durabilityLevel": "none", "timeToLiveInSeconds": int64(0),
		"flushEnabled": false, "bucketConflictResolution": "seqno"}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("bucket default %s = %#v, want %#v", k, b[k], v)
		}
	}
	if _, ok := b["priority"]; ok {
		t.Error("priority must not be defaulted")
	}
	explicit := objList(cl["buckets"])[1]
	if explicit["replicas"] != int64(2) || explicit["flushEnabled"] != true {
		t.Errorf("explicit values were overwritten: %v", explicit)
	}
	if strings.Contains(cfg.Text, "multiNode") {
		t.Error("defaults must not touch the original text")
	}
}

func TestDefaultsSingleNodeReplicasAndFreeTier(t *testing.T) {
	cfg, err := loadFixture(t, "valid/single-node.yaml")
	if err != nil {
		t.Fatal(err)
	}
	b := objList(cfg.Projects()[0].Clusters[0]["buckets"])[0]
	if b["replicas"] != int64(0) {
		t.Errorf("single-node bucket replicas default = %#v, want 0", b["replicas"])
	}
	free, err := loadFixture(t, "valid/free-tier.yaml")
	if err != nil {
		t.Fatal(err)
	}
	fc := free.Projects()[0].Clusters[0]
	if _, ok := fc["configurationType"]; ok {
		t.Error("free-tier cluster must not get configurationType")
	}
	if _, ok := objList(fc["buckets"])[0]["replicas"]; ok {
		t.Error("free-tier bucket must not get replicas")
	}
}

func TestHibernationBooleanShorthand(t *testing.T) {
	cfg, err := loadFixture(t, "valid/single-node.yaml")
	if err != nil {
		t.Fatal(err)
	}
	hib := asObj(cfg.Projects()[0].Clusters[0]["hibernation"])
	if hib["weekdays"] != "on" || hib["weekend"] != "off" {
		t.Errorf("hibernation = %v", hib)
	}
	if len(cfg.Hints) != 1 || cfg.Hints[0] != HibernationBooleanHint {
		t.Errorf("Hints = %v", cfg.Hints)
	}
}

func TestNumbersAreNormalized(t *testing.T) {
	cfg := mustLoadText(t, minimalYAML)
	g := objList(cfg.Projects()[0].Clusters[0]["serviceGroups"])[0]
	if v, ok := g["numOfNodes"].(int64); !ok || v != 3 {
		t.Errorf("numOfNodes = %#v", g["numOfNodes"])
	}
	if v, ok := asObj(asObj(g["node"])["compute"])["cpu"].(int64); !ok || v != 4 {
		t.Errorf("cpu = %#v", asObj(asObj(g["node"])["compute"])["cpu"])
	}
	// "3" as a string is not silently converted.
	ce := loadErr(t, strings.Replace(minimalYAML, "numOfNodes: 3", `numOfNodes: "3"`, 1))
	if !strings.Contains(ce.Error(), "must be an integer") {
		t.Errorf("error = %v", ce)
	}
}

func TestLoadEdgeCases(t *testing.T) {
	t.Run("BOM", func(t *testing.T) {
		mustLoadText(t, "\ufeff"+minimalYAML)
	})
	t.Run("CRLF line endings", func(t *testing.T) {
		cfg := mustLoadText(t, strings.ReplaceAll(minimalYAML, "\n", "\r\n"))
		if cfg.ProjectNames()[0] != "nova-dev" {
			t.Errorf("names = %v", cfg.ProjectNames())
		}
	})
	t.Run("projects: [] is valid and empty", func(t *testing.T) {
		cfg := mustLoadText(t, "apiVersion: cbctl/v1\nprojects: []\n")
		if len(cfg.Projects()) != 0 {
			t.Errorf("projects = %v", cfg.ProjectNames())
		}
		if !strings.Contains(cfg.Summary(), "0 projects") {
			t.Errorf("Summary = %q", cfg.Summary())
		}
	})
	t.Run("unquoted timestamp stays a string", func(t *testing.T) {
		text := minimalYAML + "    allowedCidrs:\n      - cidr: 203.0.113.10/32\n        expiresAt: 2099-12-31T23:59:59Z\n"
		cfg := mustLoadText(t, text)
		got := objList(cfg.Projects()[0].Clusters[0]["allowedCidrs"])[0]["expiresAt"]
		if got != "2099-12-31T23:59:59Z" {
			t.Errorf("expiresAt = %#v", got)
		}
	})
	t.Run("anchors and merge keys load", func(t *testing.T) {
		text := strings.Replace(minimalYAML, "compute: { cpu: 4, ram: 16 }", "compute: &c { cpu: 4, ram: 16 }", 1)
		text += "      - node:\n          compute: *c\n          disk: { type: gp3, storage: 50, iops: 3000 }\n        numOfNodes: 2\n        services: [search]\n"
		cfg := mustLoadText(t, text)
		groups := objList(cfg.Projects()[0].Clusters[0]["serviceGroups"])
		if asObj(asObj(groups[1]["node"])["compute"])["cpu"] != int64(4) {
			t.Errorf("alias not expanded: %v", groups[1])
		}
	})
	t.Run("10k lines load quickly", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(minimalYAML)
		sb.WriteString("    buckets:\n")
		for i := 0; i < 1500; i++ {
			fmt.Fprintf(&sb, "      - name: b%d\n        memoryAllocationInMb: 100\n        scopes:\n          - name: s\n            collections:\n              - name: c\n", i)
		}
		start := time.Now()
		cfg := mustLoadText(t, sb.String())
		if d := time.Since(start); d > time.Second {
			t.Errorf("loading %d lines took %v", strings.Count(sb.String(), "\n"), d)
		}
		if cfg.Counts().Buckets != 1500 {
			t.Errorf("buckets = %d", cfg.Counts().Buckets)
		}
	})
}

func TestIssuesAreSortedAndHaveLines(t *testing.T) {
	text := strings.Replace(minimalYAML, "plan: developer pro", "plan: gold", 1)
	text = strings.Replace(text, "type: aws", "type: ibm", 1)
	ce := loadErr(t, text)
	if len(ce.Issues) != 2 {
		t.Fatalf("issues = %+v", ce.Issues)
	}
	if ce.Issues[0].Line == 0 || ce.Issues[0].Line > ce.Issues[1].Line {
		t.Errorf("not sorted by line: %+v", ce.Issues)
	}
	if !strings.HasPrefix(ce.Error(), "capella.yaml has 2 problems:\n") {
		t.Errorf("heading: %q", ce.Error())
	}
}

func TestConfigAccessors(t *testing.T) {
	cfg, err := loadFixture(t, "valid/multi-project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	entries := cfg.Projects()
	if len(entries) != 2 || entries[0].Name() != "alpha" || entries[1].Name() != "beta" {
		t.Fatalf("entries = %v", cfg.ProjectNames())
	}
	if FormatPath(entries[1].Base) != "projects[1]" || FormatPath(entries[1].ClustersBase) != "projects[1].clusters" {
		t.Errorf("bases = %v %v", entries[1].Base, entries[1].ClustersBase)
	}
	c := cfg.Counts()
	want := Counts{Projects: 2, Clusters: 2, Buckets: 1, Scopes: 1, Collections: 1, Credentials: 1}
	if c != want {
		t.Errorf("Counts = %+v, want %+v", c, want)
	}
	if got := cfg.Summary(); got != "multi-project.yaml is valid: 2 projects, 2 clusters, 1 bucket, 1 scope, 1 collection, 1 credential, 0 allowed CIDRs" {
		t.Errorf("Summary = %q", got)
	}
	if line := cfg.LineOf(Path{"projects", 1, "clusters", 0, "name"}); line != 46 {
		t.Errorf("LineOf cluster name = %d", line)
	}
	if line := cfg.LineOf(Path{"projects", 1, "clusters", 0, "nope", "deeper"}); line == 0 {
		t.Error("LineOf should fall back to an ancestor")
	}
	if got := cfg.CredentialsOutput(); got != filepath.Join(filepath.Dir(cfg.Path), "secrets-out") {
		t.Errorf("CredentialsOutput = %q", got)
	}
	single, err := loadFixture(t, "valid/single-node.yaml")
	if err != nil {
		t.Fatal(err)
	}
	se := single.Projects()
	if len(se) != 1 || FormatPath(se[0].Base) != "project" || FormatPath(se[0].ClustersBase) != "clusters" || len(se[0].Clusters) != 1 {
		t.Errorf("single-project form: %+v", se)
	}
}

func TestSchemaEmbeddedAndNodeLookup(t *testing.T) {
	doc, err := Schema()
	if err != nil || doc["title"] == nil {
		t.Fatalf("Schema() = %v, %v", doc, err)
	}
	node, ok := SchemaNode(Path{"clusters", 0, "cloudProvider", "region"})
	if !ok || node["type"] != "string" {
		t.Errorf("SchemaNode(region) = %v %v", node, ok)
	}
	if _, ok := SchemaNode(Path{"projects", 0, "clusters", 0, "serviceGroups", 0, "numOfNodes"}); !ok {
		t.Error("SchemaNode through projects[].clusters[] failed")
	}
	if _, ok := SchemaNode(Path{"clusters", 0, "nope"}); ok {
		t.Error("unknown field should not resolve")
	}
	cred, ok := SchemaNode(Path{"credentialsOutput"})
	if !ok || cred["default"] != "./.cbctl/credentials" {
		t.Errorf("credentialsOutput default = %v", cred["default"])
	}
}
