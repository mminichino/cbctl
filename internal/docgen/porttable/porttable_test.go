package porttable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const srcList = `# generated
tests/scenario/test_sync.py::test_a	CAP038_AC9
tests/scenario/test_sync.py::test_b	CAP038_AC13
tests/scenario/test_sync.py::test_c
tests/unit/test_packaging.py::test_wheel	CAP043_AC1
tests/unit/test_packaging.py::test_sdist	CAP043_AC2
`

func goTests(names ...string) GoTests {
	g := GoTests{Names: map[string]bool{}, Tags: map[string][]string{}}
	for _, n := range names {
		g.add(n)
	}
	return g
}

func mustSrc(t *testing.T) []SourceTest {
	t.Helper()
	src, err := ParseSourceList(strings.NewReader(srcList))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func mustMap(t *testing.T, text string) []TestRow {
	t.Helper()
	rows, err := ParseMap(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestCheck(t *testing.T) {
	g := goTests("TestSync_CAP038_AC9_Refresh", "TestOther", "TestEdge_CAP038_AC13_AC14_Gone")
	cases := []struct {
		name     string
		rows     string
		min      float64
		wantOK   bool
		problems []string
		counts   Counts
	}{
		{
			name: "tags, wildcard n/a and explicit covered",
			rows: "tests/unit/test_packaging.py::*\tn/a\t\tpackaging: GoReleaser\n" +
				"tests/scenario/test_sync.py::test_c\tcovered\tTestOther\t\n",
			min: 100, wantOK: true,
			counts: Counts{Covered: 3, ByTag: 2, NA: 2},
		},
		{
			name:     "untagged test with no row is missing and fails the floor",
			rows:     "tests/unit/test_packaging.py::*\tn/a\t\tpackaging\n",
			min:      100,
			counts:   Counts{Covered: 2, ByTag: 2, Missing: 1, NA: 2},
			problems: []string{"80.0% of capellactl tests are covered or n/a (4 of 5); the floor is 100.0%"},
		},
		{
			name: "a lower floor passes with gaps",
			rows: "tests/unit/test_packaging.py::*\tn/a\t\tpackaging\n" +
				"tests/scenario/test_sync.py::test_c\tpartial\t\tCLI level only\n",
			min: 50, wantOK: true,
			counts: Counts{Covered: 2, ByTag: 2, Partial: 1, NA: 2},
		},
		{
			name:     "cited Go test must exist",
			rows:     "tests/scenario/test_sync.py::test_c\tcovered\tTestGone/sub\t\n",
			problems: []string{"Go test TestGone does not exist"},
		},
		{
			name:     "n/a needs a reason",
			rows:     "tests/scenario/test_sync.py::test_c\tn/a\t\t\n",
			problems: []string{"n/a needs a reason"},
		},
		{
			name:     "covered needs a Go test",
			rows:     "tests/scenario/test_sync.py::test_c\tcovered\t\t\n",
			problems: []string{"covered but names no Go test"},
		},
		{
			name:     "stale row",
			rows:     "tests/scenario/test_sync.py::test_zzz\tn/a\t\tgone\n",
			problems: []string{"matches no capellactl test"},
		},
		{
			name: "exact row beats wildcard, shadowed wildcard reported",
			rows: "tests/scenario/test_sync.py::test_c*\tn/a\t\tx\n" +
				"tests/scenario/test_sync.py::test_c\tcovered\tTestOther\t\n",
			problems: []string{"shadowed by a more specific row"},
		},
		{
			name:   "explicit row overrides a tag",
			rows:   "tests/scenario/test_sync.py::test_a\tpartial\t\tonly unit\n",
			counts: Counts{Covered: 1, ByTag: 1, Partial: 1, Missing: 3},
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := Check(mustSrc(t), mustMap(t, tc.rows), g, tc.min)
			if tc.wantOK != rep.OK() && len(tc.problems) == 0 {
				t.Fatalf("OK = %v, problems: %v", rep.OK(), rep.Problems)
			}
			for _, p := range tc.problems {
				if !strings.Contains(strings.Join(rep.Problems, "\n"), p) {
					t.Errorf("problems %v lack %q", rep.Problems, p)
				}
			}
			if tc.counts != (Counts{}) && rep.All != tc.counts {
				t.Errorf("counts = %+v, want %+v", rep.All, tc.counts)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := ParseMap(strings.NewReader("a::b\tdone\t\t\n")); err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Errorf("err = %v", err)
	}
	if _, err := ParseMap(strings.NewReader("\tcovered\tT\t\n")); err == nil {
		t.Error("empty id accepted")
	}
	if _, err := ParseSourceList(strings.NewReader("a::b\na::b\n")); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("err = %v", err)
	}
	if _, err := ParseSourceList(strings.NewReader("nocolons\n")); err == nil {
		t.Error("bad id accepted")
	}
	rows := mustMap(t, "a::b\tN/A\t\twhy\tmore\n")
	if rows[0].Status != NA || rows[0].Reason != "why\tmore" {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestCollectGoTests(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/x_test.go", "package a\nfunc TestSync_CAP038_AC13_AC14_Gone(t *testing.T) {}\nfunc TestPlain(t *testing.T) {}\nfunc helper() {}\n")
	write("a/x.go", "package a\nfunc TestNotATest(t *testing.T) {}\n")
	write(".git/y_test.go", "func TestHidden(t *testing.T) {}\n")
	g, err := CollectGoTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Names["TestPlain"] || g.Names["TestNotATest"] || g.Names["TestHidden"] {
		t.Errorf("names = %v", g.Names)
	}
	for _, tag := range []string{"CAP038", "CAP038_AC13", "CAP038_AC14"} {
		if len(g.Tags[tag]) != 1 {
			t.Errorf("tag %s = %v", tag, g.Tags[tag])
		}
	}
	if _, err := CollectGoTests(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing root accepted")
	}
}

func TestFooter(t *testing.T) {
	rep := Check(mustSrc(t), nil, goTests("TestX_CAP038_AC9_Y"), 0)
	sum := Summary(rep)
	if !strings.Contains(sum, "| scenario | 3 | 1 (1) | 0 | 2 | 0 | 33% |") || !strings.Contains(sum, "| **all** | 5 |") {
		t.Errorf("summary:\n%s", sum)
	}
	doc := ReplaceFooter("# Title\n", sum)
	if !strings.Contains(doc, FooterBegin) || !strings.HasSuffix(doc, FooterEnd+"\n") {
		t.Fatalf("doc:\n%s", doc)
	}
	again := ReplaceFooter(doc+"tail\n", "new\n")
	if strings.Contains(again, "| scenario |") || !strings.Contains(again, "new\n") || !strings.HasSuffix(again, "tail\n") {
		t.Errorf("replace:\n%s", again)
	}
	if len(Gaps(rep)) != 4 {
		t.Errorf("gaps = %v", Gaps(rep))
	}
	if (SourceTest{ID: "x.py::t"}).Tier() != "other" {
		t.Error("tier of a bare id")
	}
	if (Counts{}).Percent() != 100 {
		t.Error("empty percent")
	}
}
