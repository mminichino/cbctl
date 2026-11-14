package port

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRulesVersion(t *testing.T) {
	if RulesVersion != 1 {
		t.Fatalf("RulesVersion = %d, want 1", RulesVersion)
	}
}

func TestRewrite_R1_CommandInFenceAndInline(t *testing.T) {
	in := "Run:\n```\ncapellactl bucket flush X Y\n```\nAlso `capellactl plan` and bare `capellactl`.\n"
	out, flags := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "deadbee", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "cbctl capella bucket flush X Y") {
		t.Fatalf("R1 fence rewrite missing:\n%s", out)
	}
	if !strings.Contains(out, "`cbctl capella plan`") {
		t.Fatalf("R1 inline rewrite missing:\n%s", out)
	}
	if !strings.Contains(out, "`cbctl`") {
		t.Fatalf("R1 exact inline missing:\n%s", out)
	}
	if strings.Contains(out, "`capellactl`") || strings.Contains(out, "\ncapellactl ") {
		t.Fatalf("capellactl still present:\n%s", out)
	}
	if len(flags) != 0 {
		t.Fatalf("unexpected flags: %v", flags)
	}
	if !strings.Contains(out, "<!-- ported-from: capellactl@deadbee docs/release-1.5.0/x.md · rules v1 -->") {
		t.Fatalf("R13 footer missing:\n%s", out)
	}
}

func TestRewrite_R1_Negative_PreservesURL(t *testing.T) {
	in := "See https://github.com/couchbaselabs/capellactl for upstream.\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "https://github.com/couchbaselabs/capellactl") {
		t.Fatalf("URL rewritten incorrectly:\n%s", out)
	}
}

func TestRewrite_R1_GlobalFlagsPreserved(t *testing.T) {
	in := "```\ncapellactl -o json plan\ncapellactl --auto-approve apply\n```\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "cbctl capella -o json plan") {
		t.Fatalf("flags not preserved:\n%s", out)
	}
	if !strings.Contains(out, "cbctl capella --auto-approve apply") {
		t.Fatalf("auto-approve not preserved:\n%s", out)
	}
}

func TestRewrite_R3_HelpToGuide(t *testing.T) {
	in := "Use capellactl help and help topics; see `help` topics and help config.\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "cbctl capella guide") {
		t.Fatalf("R3 capellactl help missing:\n%s", out)
	}
	if strings.Contains(out, "help topics") || strings.Contains(out, "`help` topics") {
		t.Fatalf("R3 help topics not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "guide topics") || !strings.Contains(out, "`guide` topics") {
		t.Fatalf("R3 guide topics missing:\n%s", out)
	}
	if !strings.Contains(out, "guide config") {
		t.Fatalf("R3 guide config missing:\n%s", out)
	}
}

func TestRewrite_R3_Negative_UnrelatedHelp(t *testing.T) {
	in := "This is a helpful tip about Capella.\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "helpful tip") {
		t.Fatalf("unrelated text damaged:\n%s", out)
	}
}

func TestRewrite_R5_EnvVars(t *testing.T) {
	in := "Set CAPELLACTL_IT_TARGET and CAPELLACTL_PROFILE; leave CAPELLA_API_KEY alone.\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "CBCTL_IT_TARGET") || !strings.Contains(out, "CBCTL_PROFILE") {
		t.Fatalf("R5 rewrite missing:\n%s", out)
	}
	if !strings.Contains(out, "CAPELLA_API_KEY") {
		t.Fatalf("CAPELLA_API_KEY should be unchanged:\n%s", out)
	}
	if strings.Contains(out, "CAPELLACTL_") {
		t.Fatalf("CAPELLACTL_ still present:\n%s", out)
	}
}

func TestRewrite_R5_Negative_PartialName(t *testing.T) {
	in := "MY_CAPELLACTL_IT_TARGET_EXTRA is not ours.\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	// word-boundary: leading underscore still matches CAPELLACTL_IT_TARGET inside? \b before C after _
	// In Go \b is between word and non-word; _ is a word char, so MY_CAPELLACTL_IT_TARGET should NOT match \bCAPELLACTL_
	if strings.Contains(out, "CBCTL_IT_TARGET") {
		t.Fatalf("R5 over-matched:\n%s", out)
	}
}

func TestRewrite_R6_PathsAndAPIVersion(t *testing.T) {
	in := "Prefix capellactl-ci-tmp; config in .capellactl/ and ~/.config/capellactl; apiVersion: capellactl/v1\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "cbctl-ci-tmp") {
		t.Fatalf("R6 ci prefix missing:\n%s", out)
	}
	if !strings.Contains(out, ".cbctl/") || !strings.Contains(out, "~/.config/cbctl") {
		t.Fatalf("R6 path rewrite missing:\n%s", out)
	}
	if !strings.Contains(out, "apiVersion: cbctl/v1") {
		t.Fatalf("R6 apiVersion missing:\n%s", out)
	}
}

func TestRewrite_R6_Negative_URLAndCapellactlCiBoundary(t *testing.T) {
	in := "URL https://example.com/capellactl/v1 stays; word capellactl alone changes.\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "https://example.com/capellactl/v1") {
		t.Fatalf("URL path rewritten:\n%s", out)
	}
	if !strings.Contains(out, "word cbctl alone") {
		t.Fatalf("bare word not rewritten:\n%s", out)
	}
}

func TestRewrite_R13_IdempotentFooter(t *testing.T) {
	in := "# Title\n\nbody\n"
	opts := RewriteOpts{Profile: "story", SHA7: "6389d5a", SrcPath: "docs/release-1.5.0/CAP-066-bucket-flush.md"}
	out1, _ := Rewrite(in, opts)
	out2, _ := Rewrite(out1, opts)
	// Second rewrite of already-footed content should still produce one footer
	if strings.Count(out2, "<!-- ported-from:") != 1 {
		t.Fatalf("expected one footer, got:\n%s", out2)
	}
}

func TestRewrite_CAP066_Golden(t *testing.T) {
	inPath := filepath.Join("testdata", "CAP-066.in.md")
	wantPath := filepath.Join("testdata", "CAP-066.out.md")
	in, err := os.ReadFile(inPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	got, flags := Rewrite(string(in), RewriteOpts{
		Profile:  "story",
		SHA7:     "6389d5a",
		SrcPath:  "docs/release-1.5.0/CAP-066-bucket-flush.md",
		Release:  "1.5.0",
		FileName: "CAP-066-bucket-flush.md",
	})
	if len(flags) != 0 {
		t.Fatalf("unexpected flags: %v", flags)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRewrite_DollarPrompt(t *testing.T) {
	in := "```\n$ capellactl sync\n```\n"
	out, _ := Rewrite(in, RewriteOpts{Profile: "story", SHA7: "abc1234", SrcPath: "docs/release-1.5.0/x.md"})
	if !strings.Contains(out, "$ cbctl capella sync") {
		t.Fatalf("$ prompt rewrite failed:\n%s", out)
	}
}
