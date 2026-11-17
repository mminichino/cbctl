package links

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIssueString(t *testing.T) {
	if got := (Issue{File: "a.md", Line: 3, Msg: "boom"}).String(); got != "a.md:3: boom" {
		t.Fatalf("got %q", got)
	}
}

func TestCheckFileCases(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string // issue messages, in order
	}{
		{"same-file anchor ok", "# Intro\n\n[x](#intro)\n", nil},
		{"same-file anchor missing", "# Intro\n\n[x](#nope)\n", []string{`missing anchor "#nope"`}},
		{"empty target", "[x]( )\n", nil},
		{"external skipped", "[a](https://e.com) [b](http://e.com) [c](mailto:a@b)\n", nil},
		{"title stripped", "[b](b.md \"Title\")\n", nil},
		{"empty path with fragment via slash", "# Intro\n[x](#a/b)\n", []string{`missing anchor #a/b`}},
		{"other file anchor missing", "[b](b.md#nope)\n", []string{`missing anchor "nope" in b.md`}},
		{"other file anchor via html id", "[b](b.md#custom)\n", nil},
		{"duplicate heading suffix", "[b](b.md#target-1)\n", nil},
		{"directory link tolerated", "[d](sub)\n", nil},
		{"non-md fragment ignored", "[t](t.txt#x)\n", nil},
		{"fenced links ignored", "```\n[x](missing.md)\n```\n~~~~\n[y](gone.md)\n~~~~\n", nil},
		{"fence inside fence", "````\n```\n[x](missing.md)\n````\n[z](z.md)\n", []string{`broken link "z.md"`}},
		{"html id anchor", "<a id=\"here\"></a>\n[x](#here) [y](#)\n", nil},
		{"heading in fence is not anchor", "```\n# Hidden\n```\n[x](#hidden)\n", []string{`missing anchor "#hidden"`}},
		{"hashes without space", "#NoSpace\n####### seven\n[x](#nospace)\n", []string{`missing anchor "#nospace"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "b.md"), "# Target\n\n# Target\n\n<span id=\"custom\"></span>\n")
			write(t, filepath.Join(dir, "t.txt"), "plain")
			if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			a := filepath.Join(dir, "a.md")
			write(t, a, tc.body)
			issues := CheckFile(a)
			if len(issues) != len(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, issues)
			}
			for i, w := range tc.want {
				if issues[i].Msg != w || issues[i].File != a {
					t.Errorf("issue %d = %+v, want msg %q", i, issues[i], w)
				}
			}
		})
	}
}

func TestCheckFileUnreadable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.md")
	issues := CheckFile(p)
	if len(issues) != 1 || issues[0].Line != 1 || !strings.Contains(issues[0].Msg, "no such file") {
		t.Fatalf("got %v", issues)
	}
}

func TestCheckWalksRoots(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ok.md"), "# Ok\n[x](#ok)\n")
	write(t, filepath.Join(dir, "nested", "bad.md"), "[x](nope.md)\n")
	write(t, filepath.Join(dir, "nested", "skip.txt"), "[x](nope.md)\n")

	issues := Check(dir, filepath.Join(dir, "does-not-exist"))
	if len(issues) != 1 || !strings.HasSuffix(issues[0].File, filepath.Join("nested", "bad.md")) || issues[0].Msg != `broken link "nope.md"` {
		t.Fatalf("got %v", issues)
	}
}

func TestCheckDotRootSkipsNonDocsTrees(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "top.md"), "[x](missing-top.md)\n")
	write(t, filepath.Join(dir, "docs", "d.md"), "[x](missing-doc.md)\n")
	write(t, filepath.Join(dir, "other", "o.md"), "[x](missing-other.md)\n")
	t.Chdir(dir)

	var msgs []string
	for _, is := range Check() { // defaults: "docs", "."
		msgs = append(msgs, is.File+" "+is.Msg)
	}
	got := strings.Join(msgs, "\n")
	for _, want := range []string{"top.md", filepath.Join("docs", "d.md")} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "missing-other.md") {
		t.Errorf("non-docs subtree should be skipped:\n%s", got)
	}
}
