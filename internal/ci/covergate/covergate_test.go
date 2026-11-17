package covergate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const root = "example.com/m/internal"

func TestDefaultConfig(t *testing.T) {
	c, err := DefaultConfig()
	if err != nil {
		t.Fatalf("embedded tiers.json: %v", err)
	}
	if c.Root != "github.com/mminichino/cbctl/internal" {
		t.Fatalf("root = %q", c.Root)
	}
	want := map[string]float64{"core": 90, "security": 90, "cli": 85, "importer": 80, "testinfra": 85, "tooling": 70, "server-legacy": 60}
	if len(c.Tiers) != len(want) {
		t.Fatalf("tiers = %d, want %d", len(c.Tiers), len(want))
	}
	for _, tier := range c.Tiers {
		if want[tier.Name] != tier.Min {
			t.Errorf("tier %s min = %.0f, want %.0f", tier.Name, tier.Min, want[tier.Name])
		}
	}
}

func TestParseConfig_Invalid(t *testing.T) {
	cases := map[string]string{
		"not json":       `{`,
		"no root":        `{"tiers":[{"name":"a","min":1,"packages":["x"]}]}`,
		"no tiers":       `{"root":"r","tiers":[]}`,
		"no name":        `{"root":"r","tiers":[{"min":1,"packages":["x"]}]}`,
		"duplicate tier": `{"root":"r","tiers":[{"name":"a","min":1,"packages":["x"]},{"name":"a","min":1,"packages":["y"]}]}`,
		"zero min":       `{"root":"r","tiers":[{"name":"a","min":0,"packages":["x"]}]}`,
		"min over 100":   `{"root":"r","tiers":[{"name":"a","min":101,"packages":["x"]}]}`,
		"no packages":    `{"root":"r","tiers":[{"name":"a","min":1,"packages":[]}]}`,
		"empty package":  `{"root":"r","tiers":[{"name":"a","min":1,"packages":[""]}]}`,
		"package twice":  `{"root":"r","tiers":[{"name":"a","min":1,"packages":["x"]},{"name":"b","min":1,"packages":["x"]}]}`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(text)); err == nil {
				t.Fatalf("ParseConfig(%s) succeeded, want error", text)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing file should fail")
	}
	path := filepath.Join(t.TempDir(), "tiers.json")
	if err := os.WriteFile(path, []byte(`{"root":"r","tiers":[{"name":"a","min":50,"packages":["x"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil || len(c.Tiers) != 1 {
		t.Fatalf("LoadConfig = %+v, %v", c, err)
	}
}

// Two profiles covering different blocks of the same package merge to the union, and a block
// listed twice in one profile (as -coverpkg writes it per test binary) counts once.
func TestProfile_MergeAndTotals(t *testing.T) {
	a := "mode: set\n" +
		root + "/plan/a.go:1.1,2.1 3 1\n" +
		root + "/plan/a.go:3.1,4.1 2 0\n" +
		root + "/plan/a.go:1.1,2.1 3 0\n" +
		root + "/ci/x/b.go:1.1,2.1 4 0\n"
	b := "mode: atomic\n" +
		root + "/plan/a.go:3.1,4.1 2 7\n" +
		root + "/ci/y/c.go:1.1,2.1 6 1\n"
	p := Profile{}
	if err := p.ParseProfile(strings.NewReader(a)); err != nil {
		t.Fatal(err)
	}
	if err := p.ParseProfile(strings.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	tot := p.Totals()
	if got := tot[root+"/plan"]; got != (Counts{Statements: 5, Covered: 5}) {
		t.Fatalf("plan = %+v, want 5/5", got)
	}
	if got := tot[root+"/ci/x"]; got != (Counts{Statements: 4, Covered: 0}) {
		t.Fatalf("ci/x = %+v", got)
	}
}

func TestProfile_ParseErrors(t *testing.T) {
	cases := map[string]string{
		"empty":      "",
		"no header":  root + "/plan/a.go:1.1,2.1 3 1\n",
		"fields":     "mode: set\n" + root + "/plan/a.go:1.1,2.1 3\n",
		"no colon":   "mode: set\nfile 3 1\n",
		"no package": "mode: set\na.go:1.1,2.1 3 1\n",
		"bad stmts":  "mode: set\n" + root + "/plan/a.go:1.1,2.1 x 1\n",
		"bad count":  "mode: set\n" + root + "/plan/a.go:1.1,2.1 3 y\n",
		"stale":      "mode: set\n" + root + "/plan/a.go:1.1,2.1 3 1\n" + root + "/plan/a.go:1.1,2.1 4 1\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if err := (Profile{}).ParseProfile(strings.NewReader(text)); err == nil {
				t.Fatalf("ParseProfile succeeded, want error")
			}
		})
	}
}

func TestLoadProfiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.out")
	if err := os.WriteFile(good, []byte("mode: set\n"+root+"/plan/a.go:1.1,2.1 3 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.out")
	if err := os.WriteFile(bad, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfiles([]string{good})
	if err != nil || p.Totals()[root+"/plan"].Covered != 3 {
		t.Fatalf("LoadProfiles = %v, %v", p, err)
	}
	if _, err := LoadProfiles([]string{good, bad}); err == nil || !strings.Contains(err.Error(), "bad.out") {
		t.Fatalf("bad profile err = %v, want path in message", err)
	}
	if _, err := LoadProfiles([]string{filepath.Join(dir, "missing.out")}); err == nil {
		t.Fatal("missing profile should fail")
	}
}

func TestEvaluate(t *testing.T) {
	cfg := Config{Root: root, Tiers: []Tier{
		{Name: "core", Min: 90, Pkgs: []string{"plan", "gone"}},
		{Name: "tooling", Min: 70, Pkgs: []string{"ci/..."}},
		{Name: "low", Min: 50, Pkgs: []string{"cli"}},
	}}
	totals := map[string]Counts{
		root + "/plan":     {Statements: 100, Covered: 95},
		root + "/ci":       {Statements: 10, Covered: 10},
		root + "/ci/x":     {Statements: 40, Covered: 20},
		root + "/ci/y":     {Statements: 50, Covered: 40},
		root + "/cli":      {Statements: 10, Covered: 4},
		root + "/cliextra": {Statements: 10, Covered: 10}, // "cli" must not match by prefix
		root + "/models":   {Statements: 0},               // no statements: not listed as untiered
	}
	rep := Evaluate(cfg, totals)
	got := map[string]Row{}
	for _, r := range rep.Rows {
		got[r.Package] = r
	}
	if r := got["plan"]; r.Status != StatusOK || r.Percent() != 95 {
		t.Errorf("plan = %+v", r)
	}
	if r := got["gone"]; r.Status != StatusMissing {
		t.Errorf("gone = %+v, want MISSING", r)
	}
	if r := got["ci/..."]; r.Status != StatusOK || r.Statements != 100 || r.Covered != 70 {
		t.Errorf("ci/... = %+v, want 70/100 OK", r)
	}
	if r := got["cli"]; r.Status != StatusBelow || r.Statements != 10 {
		t.Errorf("cli = %+v, want BELOW over 10 statements", r)
	}
	if !rep.Failed() {
		t.Error("report with BELOW and MISSING rows should fail")
	}
	if len(rep.Untiered) != 1 || rep.Untiered[0] != "cliextra" {
		t.Errorf("untiered = %q, want [cliextra]", rep.Untiered)
	}

	var buf bytes.Buffer
	if err := Write(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"TIER", "core", "95.0%", "MISSING", "BELOW", "n/a", "untiered (not gated): cliextra"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	ok := Evaluate(Config{Root: root, Tiers: []Tier{{Name: "core", Min: 90, Pkgs: []string{"plan"}}}}, totals)
	if ok.Failed() {
		t.Errorf("passing report failed: %+v", ok.Rows)
	}
}

func TestCountsPercent_NoStatements(t *testing.T) {
	if got := (Counts{}).Percent(); got != 100 {
		t.Fatalf("Percent = %v, want 100", got)
	}
}
