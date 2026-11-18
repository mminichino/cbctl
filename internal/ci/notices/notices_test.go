package notices_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/ci/notices"
)

const mitText = `MIT License

Copyright (c) 2020 Someone

Permission is hereby granted, free of charge, to any person obtaining a copy of this software
and associated documentation files (the "Software"), to deal in the Software without restriction,
including without limitation the rights to use, copy, modify, merge, publish, distribute.

The above copyright notice and this permission notice shall be included in all copies or
substantial portions of the Software.`

const gplText = `GNU GENERAL PUBLIC LICENSE
Version 3, 29 June 2007
Everyone is permitted to copy and distribute verbatim copies of this license document.`

// repoRoot is the module root, two folders up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func policy(t *testing.T) notices.Policy {
	t.Helper()
	p, err := notices.LoadPolicy("")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// collected are the modules compiled into ./cmd/cbctl, gathered once per test binary.
var collected []notices.Dep

func installed(t *testing.T) []notices.Dep {
	t.Helper()
	if collected == nil {
		deps, err := notices.Collect(context.Background(), notices.GoList(repoRoot(t), "./cmd/cbctl"))
		if err != nil {
			t.Fatal(err)
		}
		collected = deps
	}
	return collected
}

func readNotices(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), notices.File))
	if err != nil {
		t.Fatalf("%v; run 'make notices'", err)
	}
	return string(b)
}

// TestLicence_CAP044_AC3_ThirdPartyNoticesCurrent: THIRD_PARTY_NOTICES.md lists every module
// compiled into the release binaries with its current version and licence, and each is allowed.
func TestLicence_CAP044_AC3_ThirdPartyNoticesCurrent(t *testing.T) {
	deps := installed(t)
	names := map[string]bool{}
	for _, d := range deps {
		names[d.Module] = true
	}
	for _, m := range []string{"github.com/spf13/cobra", "go.yaml.in/yaml/v3", "github.com/zalando/go-keyring",
		"github.com/santhosh-tekuri/jsonschema/v6", "github.com/couchbase/gocb/v2"} {
		if !names[m] {
			t.Errorf("%s is not among the collected modules", m)
		}
	}
	if problems := notices.Check(readNotices(t), deps, policy(t)); len(problems) != 0 {
		t.Fatalf("THIRD_PARTY_NOTICES.md is stale (run 'make notices'):\n%s", strings.Join(problems, "\n"))
	}
}

// TestLicence_CAP044_AC3_StaleFileDetected: a missing row, a changed licence and a row for a
// module that is gone are each reported.
func TestLicence_CAP044_AC3_StaleFileDetected(t *testing.T) {
	deps := installed(t)
	text := readNotices(t)
	p := policy(t)
	const cobra = "github.com/spf13/cobra"
	var without []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| "+cobra+" |") {
			without = append(without, line)
		}
	}
	if !hasProblem(notices.Check(strings.Join(without, "\n"), deps, p), cobra, "is not in THIRD_PARTY_NOTICES.md") {
		t.Error("a missing row was not reported")
	}
	relicensed := strings.Replace(text, "| "+cobra+" | v1.10.2 | Apache-2.0 |", "| "+cobra+" | v1.10.2 | GPL-3.0-only |", 1)
	if relicensed == text {
		t.Fatalf("no cobra row to change in:\n%s", text)
	}
	if !hasProblem(notices.Check(relicensed, deps, p), cobra+": THIRD_PARTY_NOTICES.md says GPL-3.0-only") {
		t.Error("a changed licence was not reported")
	}
	extra := text + "| example.com/gone | v1.0.0 | MIT |  | https://pkg.go.dev/example.com/gone |\n"
	if !hasProblem(notices.Check(extra, deps, p), "example.com/gone is in THIRD_PARTY_NOTICES.md but no longer compiled in") {
		t.Error("a row for a module no longer used was not reported")
	}
}

func hasProblem(problems []string, parts ...string) bool {
	for _, p := range problems {
		ok := true
		for _, part := range parts {
			ok = ok && strings.Contains(p, part)
		}
		if ok {
			return true
		}
	}
	return false
}

// TestLicence_CAP044_AC4_PolicyRejectsUnknownLicence: a copyleft licence and an undetectable one
// are refused by the policy, naming the policy file.
func TestLicence_CAP044_AC4_PolicyRejectsUnknownLicence(t *testing.T) {
	p := policy(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("All rights reserved. Ask us first."), 0o644); err != nil {
		t.Fatal(err)
	}
	unknown, err := notices.DetectDir(dir)
	if err != nil || unknown != notices.Unknown {
		t.Fatalf("DetectDir = %q, %v; want UNKNOWN", unknown, err)
	}
	if got := notices.Detect(gplText); got != "GPL-3.0-only" {
		t.Fatalf("Detect(GPL) = %q", got)
	}
	deps := []notices.Dep{
		{Module: "example.com/copyleft-lib", Version: "v1.0.0", Licence: notices.Detect(gplText)},
		{Module: "example.com/mystery-lib", Version: "v0.1.0", Licence: unknown},
	}
	problems := notices.Check(notices.Render(deps), deps, p)
	for _, want := range []string{
		"example.com/copyleft-lib v1.0.0 is licensed GPL-3.0-only, which is not allowed by " + notices.PolicyFile,
		"example.com/mystery-lib v0.1.0 is licensed UNKNOWN, which is not allowed by " + notices.PolicyFile,
	} {
		if !hasProblem(problems, want) {
			t.Errorf("missing problem %q in %q", want, problems)
		}
	}
}

// TestLicence_CAP044_AC4_SPDXExpressions: OR needs one allowed side, AND both, WITH counts as
// its licence.
func TestLicence_CAP044_AC4_SPDXExpressions(t *testing.T) {
	p := policy(t)
	for expr, ok := range map[string]bool{
		"MIT":                                  true,
		"Apache-2.0 OR BSD-3-Clause":           true,
		"GPL-2.0-only OR MIT":                  true,
		"MIT AND (Apache-2.0 OR BSD-2-Clause)": true,
		"MIT AND GPL-3.0-only":                 false,
		"LGPL-3.0-or-later":                    false,
		"Apache-2.0 WITH LLVM-exception":       true,
		"(MIT)":                                true,
		"UNKNOWN":                              false,
	} {
		if got := notices.Allowed(expr, p); got != ok {
			t.Errorf("Allowed(%q) = %v, want %v", expr, got, ok)
		}
	}
}

// TestDetect: the common licence texts are recognised, several in one module are joined by AND.
func TestDetect(t *testing.T) {
	if got := notices.Detect(mitText); got != "MIT" {
		t.Errorf("Detect(MIT) = %q", got)
	}
	dir := t.TempDir()
	for name, text := range map[string]string{"LICENSE": mitText, "LICENSE.gpl": gplText, "README.md": gplText} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := notices.DetectDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "MIT AND GPL-3.0-only" && got != "GPL-3.0-only AND MIT" {
		t.Errorf("DetectDir = %q", got)
	}
	if _, err := notices.DetectDir(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing module folder is not an error")
	}
}

// TestCollectNeeded: a module compiled in on only some platforms names them.
func TestCollectNeeded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(mitText), 0o644); err != nil {
		t.Fatal(err)
	}
	list := func(_ context.Context, goos string) ([]notices.Module, error) {
		mods := []notices.Module{{Path: "example.com/all", Version: "v1", Dir: dir}}
		if goos == "windows" {
			mods = append(mods, notices.Module{Path: "example.com/win", Version: "v2", Dir: dir})
		}
		return mods, nil
	}
	deps, err := notices.Collect(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 || deps[0].Needed != "" || deps[1].Needed != "windows" || deps[1].Licence != "MIT" {
		t.Fatalf("deps = %+v", deps)
	}
	text := notices.Render(deps)
	if problems := notices.Check(text, deps, policy(t)); len(problems) != 0 {
		t.Fatalf("a rendered file has problems: %v", problems)
	}
	if !hasProblem(notices.Check(strings.Replace(text, "| windows |", "|  |", 1), deps, policy(t)), "needed on") {
		t.Error("a changed platform list was not reported")
	}
}

// TestLoadPolicy: a missing, malformed or empty policy is an error.
func TestLoadPolicy(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"bad.json": "{", "empty.json": `{"allowed": []}`} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := notices.LoadPolicy(path); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := notices.LoadPolicy(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("missing policy: no error")
	}
}
