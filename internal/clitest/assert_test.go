package clitest

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
)

func TestAssertNoSecret(t *testing.T) {
	secret := `p@ss/word "x"`
	t.Run("clean", func(t *testing.T) {
		AssertNoSecret(t, Result{Stdout: "all good\n", Stderr: "ok"}, secret)
	})
	t.Run("raw leak", func(t *testing.T) {
		rt := &countingTB{TB: t}
		AssertNoSecret(rt, Result{Stdout: "got " + secret}, secret)
		if rt.errs == 0 {
			t.Fatal("expected error for raw secret leak")
		}
	})
	t.Run("json-escaped leak", func(t *testing.T) {
		escaped := strings.ReplaceAll(secret, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		rt := &countingTB{TB: t}
		AssertNoSecret(rt, Result{Stderr: "body " + escaped}, secret)
		if rt.errs == 0 {
			t.Fatal("expected error for JSON-escaped secret leak")
		}
	})
	t.Run("url-encoded leak", func(t *testing.T) {
		rt := &countingTB{TB: t}
		AssertNoSecret(rt, Result{Stdout: url.QueryEscape(secret)}, secret)
		if rt.errs == 0 {
			t.Fatal("expected error for URL-encoded secret leak")
		}
	})
}

func TestGoldenText(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	name := "sample"
	content := "hello golden\nline 2\n"
	prev := *updateGoldens
	*updateGoldens = true
	t.Cleanup(func() { *updateGoldens = prev })
	GoldenText(t, name, content)
	*updateGoldens = false

	path := filepath.Join("testdata", name+".golden")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("golden not written: %v", err)
	}
	GoldenText(t, name, content)

	rt := &countingTB{TB: t, fatalf: func(format string, args ...any) {
		panic(fmt.Sprintf(format, args...))
	}}
	func() {
		defer func() {
			rec := recover()
			if rec == nil {
				t.Fatal("expected fatal on mismatch")
			}
			msg, _ := rec.(string)
			if !strings.Contains(msg, "golden mismatch") {
				t.Fatalf("recover = %v, want golden mismatch", rec)
			}
		}()
		GoldenText(rt, name, "different\n")
	}()
}

func TestAssertWritesAndNoWrites(t *testing.T) {
	f := capellafake.New(t)
	AssertNoWrites(t, f)

	pid := f.SeedProject("p1")
	f.ResetLog()

	body := strings.NewReader(`{"name":"p1","description":"d"}`)
	req, err := http.NewRequest(http.MethodPut, f.URL+"/v4/organizations/"+f.OrgID+"/projects/"+pid, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", "Version: 1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	labels := writeLabels(f)
	if len(labels) == 0 {
		t.Fatalf("expected a PUT in the fake log after status %d", res.StatusCode)
	}
	AssertWrites(t, f, labels[0])
	AssertWrites(t, f, "PUT /v4/organizations/*/projects/*")

	rt := &countingTB{TB: t, fatalf: func(format string, args ...any) {
		panic(fmt.Sprintf(format, args...))
	}}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected fatal for AssertNoWrites with writes present")
			}
		}()
		AssertNoWrites(rt, f)
	}()
}

// countingTB records Errorf calls and optionally intercepts Fatalf.
type countingTB struct {
	testing.TB
	errs   int
	fatalf func(format string, args ...any)
}

func (c *countingTB) Helper() {}

func (c *countingTB) Errorf(format string, args ...any) {
	c.errs++
}

func (c *countingTB) Fatalf(format string, args ...any) {
	if c.fatalf != nil {
		c.fatalf(format, args...)
		return
	}
	c.TB.Fatalf(format, args...)
}
