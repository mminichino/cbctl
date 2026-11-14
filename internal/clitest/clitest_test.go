package clitest_test

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/clitest"
)

func TestScriptedPrompterHappyPath(t *testing.T) {
	p := clitest.NewScriptedPrompter(t)
	p.Push("y", "typed-name", "answer", "2", "s3cret")
	ok, err := p.Confirm("go?", false)
	if err != nil || !ok {
		t.Fatalf("Confirm = %v %v", ok, err)
	}
	match, err := p.Typed("type name", "typed-name")
	if err != nil || !match {
		t.Fatalf("Typed = %v %v", match, err)
	}
	got, err := p.Ask("value", "def")
	if err != nil || got != "answer" {
		t.Fatalf("Ask = %q %v", got, err)
	}
	idx, err := p.Select("pick", []string{"a", "b", "c"})
	if err != nil || idx != 1 {
		t.Fatalf("Select = %d %v", idx, err)
	}
	sec, err := p.Secret("password")
	if err != nil || sec != "s3cret" {
		t.Fatalf("Secret = %q %v", sec, err)
	}
}

func TestAssertNoSecretClean(t *testing.T) {
	clitest.AssertNoSecret(t, clitest.Result{Stdout: "ok", Stderr: "fine"}, "super-secret-value-xyz")
}

func TestGoldenTextRoundTrip(t *testing.T) {
	const name = "unit-roundtrip"
	want := "line one\nline two\n"
	clitest.GoldenText(t, name, want)
	clitest.GoldenText(t, name, want)
}

func TestScenarioListNoWrites(t *testing.T) {
	s := clitest.NewScenario(t)
	_ = s.Fake.SeedProject("p1")
	s.Fake.ResetLog()
	res := s.Run("capella", "project", "list")
	if res.Code != 0 {
		t.Fatalf("project list: code=%d\n%s\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "p1") {
		t.Fatalf("list missing p1:\n%s", res.Stdout)
	}
	clitest.AssertNoWrites(t, s.Fake)
}
