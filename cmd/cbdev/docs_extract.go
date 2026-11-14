package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/docgen/port"
	"gopkg.in/yaml.v3"
)

// cmdDocsExtractData runs a one-shot python3 importer against a capellactl checkout
// (scripts/update-api-coverage.py + update-story-index.py) and writes
// internal/docgen/data/{commands,workflow,ops,areas,gaps,as_ops,as_areas}.json
// plus phases.yaml. Existing JSON is refreshed in place so generators keep working.
// Free-text fields are rewritten with port rules R1/R5/R6; REVIEW.txt lists flags.
func cmdDocsExtractData(args []string, stdout, stderr io.Writer) int {
	var capellaDir, outDir string
	check := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--capellactl" && i+1 < len(args):
			i++
			capellaDir = args[i]
		case strings.HasPrefix(a, "--capellactl="):
			capellaDir = strings.TrimPrefix(a, "--capellactl=")
		case a == "--out" && i+1 < len(args):
			i++
			outDir = args[i]
		case strings.HasPrefix(a, "--out="):
			outDir = strings.TrimPrefix(a, "--out=")
		case a == "--check":
			check = true
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, `usage: cbdev docs extract-data --capellactl DIR [--out internal/docgen/data] [--check]

One-shot extractor (needs python3). Imports capellactl scripts/update-api-coverage.py
and update-story-index.py, dumps dictionaries as JSON/YAML for the Go generators.
With --check, compares counts against the committed data and exits 1 on mismatch
without writing.
`)
			return 0
		default:
			fmt.Fprintf(stderr, "cbdev docs extract-data: unknown argument %q\n", a)
			return 2
		}
	}
	if capellaDir == "" {
		fmt.Fprintln(stderr, "cbdev docs extract-data: --capellactl is required")
		return 2
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs extract-data: %v\n", err)
		return 1
	}
	if outDir == "" {
		outDir = filepath.Join(root, "internal", "docgen", "data")
	} else if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(root, outDir)
	}

	raw, err := runExtractPython(capellaDir)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs extract-data: %v\n", err)
		return 1
	}
	var payload extractPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		fmt.Fprintf(stderr, "cbdev docs extract-data: decode: %v\n", err)
		return 1
	}
	if err := validateExtractCounts(payload, stderr); err != nil {
		fmt.Fprintf(stderr, "cbdev docs extract-data: %v\n", err)
		return 1
	}

	files, review, err := materializeExtract(payload)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev docs extract-data: %v\n", err)
		return 1
	}

	if check {
		return checkExtractOut(outDir, files, stdout, stderr)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "cbdev docs extract-data: %v\n", err)
		return 1
	}
	for name, body := range files {
		path := filepath.Join(outDir, name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			fmt.Fprintf(stderr, "cbdev docs extract-data: write %s: %v\n", path, err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", path, len(body))
	}
	reviewPath := filepath.Join(outDir, "REVIEW.txt")
	if err := os.WriteFile(reviewPath, []byte(review), 0o644); err != nil {
		fmt.Fprintf(stderr, "cbdev docs extract-data: write REVIEW.txt: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (%d review lines)\n", reviewPath, strings.Count(review, "\n"))
	return 0
}

type extractPayload struct {
	Commands map[string]extractCmd     `json:"commands"`
	Workflow []extractWorkflow         `json:"workflow"`
	Ops      map[string]extractOp      `json:"ops"`
	Areas    []extractArea             `json:"areas"`
	Gaps     []string                  `json:"gaps"`
	ASOps    map[string]extractASOp    `json:"as_ops"`
	ASAreas  []extractASArea           `json:"as_areas"`
	Releases map[string]extractRelease `json:"releases"`
}

type extractCmd struct {
	Stories []string `json:"stories"`
	Left    string   `json:"left"`
}

type extractWorkflow struct {
	Name    string   `json:"name"`
	Stories []string `json:"stories"`
	Note    string   `json:"note"`
}

type extractOp struct {
	Commands   string   `json:"commands"`
	Note       string   `json:"note"`
	Left       string   `json:"left"`
	Status     *string  `json:"status"`
	Stories    []string `json:"stories"`
	Requires   []string `json:"requires"`
	PartialUse bool     `json:"partial_use"`
}

type extractArea struct {
	Tag   string `json:"tag"`
	Label string `json:"label"`
	In10  bool   `json:"in_1_0"`
}

type extractASOp struct {
	Command string `json:"command"`
	Note    string `json:"note"`
}

type extractASArea struct {
	Label string `json:"label"`
	Tag   string `json:"tag"`
}

type extractRelease struct {
	Architecture string         `json:"architecture"`
	Scope        string         `json:"scope"`
	Phases       []extractPhase `json:"phases"`
	Checklist    []string       `json:"checklist"`
}

type extractPhase struct {
	Name    string   `json:"name"`
	Outcome string   `json:"outcome"`
	IDs     []string `json:"ids"`
}

func runExtractPython(capellaDir string) ([]byte, error) {
	script := `#!/usr/bin/env python3
import importlib.util, json, sys
from pathlib import Path

root = Path(sys.argv[1]).resolve()
sys.path.insert(0, str(root))

def load(name, rel):
    path = root / rel
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod

uac = load("uac", "scripts/update-api-coverage.py")
usi = load("usi", "scripts/update-story-index.py")

commands = {}
for name, (stories, left) in uac.COMMANDS.items():
    commands[name] = {"stories": list(stories), "left": left or ""}

workflow = []
for name, stories, note in uac.WORKFLOW:
    sts = [s.strip() for s in stories.split(",") if s.strip()] if isinstance(stories, str) else list(stories)
    workflow.append({"name": name, "stories": sts, "note": note or ""})

ops = {}
for (method, path), meta in uac.OPS.items():
    key = f"{method} {path}"
    status = meta.get("status")
    ops[key] = {
        "commands": meta.get("commands") or "",
        "note": meta.get("note") or "",
        "left": meta.get("left") or "",
        "status": status,
        "stories": list(meta.get("stories") or ()),
        "requires": list(meta.get("requires") or ()),
        "partial_use": bool(meta.get("partial_use")),
    }

areas = [{"tag": t, "label": lab, "in_1_0": bool(in10)} for (t, lab, in10) in uac.AREAS]
gaps = list(uac.GAPS)
as_ops = {}
for (method, path), (command, note) in uac.AS_OPS.items():
    as_ops[f"{method} {path}"] = {"command": command or "", "note": note or ""}
as_areas = [{"label": lab, "tag": tag} for (lab, tag) in uac.AS_AREAS]

releases = {}
for rel, cfg in usi.RELEASES.items():
    phases = []
    for name, outcome, ids in cfg.get("phases", []):
        phases.append({"name": name, "outcome": outcome, "ids": list(ids)})
    releases[rel] = {
        "architecture": cfg.get("architecture") or "",
        "scope": cfg.get("scope") or "",
        "phases": phases,
        "checklist": list(cfg.get("checklist") or []),
    }

json.dump({
    "commands": commands,
    "workflow": workflow,
    "ops": ops,
    "areas": areas,
    "gaps": gaps,
    "as_ops": as_ops,
    "as_areas": as_areas,
    "releases": releases,
}, sys.stdout, ensure_ascii=False)
`
	cmd := exec.Command("python3", "-c", script, capellaDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("python3 extract failed: %s", msg)
	}
	return stdout.Bytes(), nil
}

func validateExtractCounts(p extractPayload, stderr io.Writer) error {
	checks := []struct {
		name    string
		n, want int
	}{
		{"commands", len(p.Commands), 194},
		{"workflow", len(p.Workflow), 12},
		{"ops", len(p.Ops), 220},
		{"as_ops", len(p.ASOps), 73},
	}
	var errs []string
	for _, c := range checks {
		if c.n != c.want {
			errs = append(errs, fmt.Sprintf("%s: got %d want %d", c.name, c.n, c.want))
		}
	}
	if len(errs) > 0 {
		fmt.Fprintln(stderr, "warning: unexpected extract counts (continuing): "+strings.Join(errs, "; "))
	}
	return nil
}

func rewriteField(s string) (string, []string) {
	if s == "" {
		return s, nil
	}
	out, flags := port.Rewrite(s, port.RewriteOpts{Profile: "guide"})
	out = strings.TrimSuffix(out, "\n")
	var reviews []string
	for _, f := range flags {
		reviews = append(reviews, fmt.Sprintf("%s: %s", f.Rule, f.Text))
	}
	return out, reviews
}

func materializeExtract(p extractPayload) (map[string][]byte, string, error) {
	var reviewLines []string
	note := func(field, text string, reviews []string) {
		for _, r := range reviews {
			reviewLines = append(reviewLines, field+": "+r+" :: "+text)
		}
	}

	cmds := map[string]extractCmd{}
	for name, c := range p.Commands {
		left, rev := rewriteField(c.Left)
		note("commands["+name+"].left", c.Left, rev)
		cmds[name] = extractCmd{Stories: c.Stories, Left: left}
	}
	workflow := make([]extractWorkflow, 0, len(p.Workflow))
	for _, w := range p.Workflow {
		noteText, rev := rewriteField(w.Note)
		note("workflow["+w.Name+"].note", w.Note, rev)
		workflow = append(workflow, extractWorkflow{Name: w.Name, Stories: w.Stories, Note: noteText})
	}
	ops := map[string]extractOp{}
	for k, o := range p.Ops {
		commands, r1 := rewriteField(o.Commands)
		n, r2 := rewriteField(o.Note)
		left, r3 := rewriteField(o.Left)
		note("ops["+k+"].commands", o.Commands, r1)
		note("ops["+k+"].note", o.Note, r2)
		note("ops["+k+"].left", o.Left, r3)
		ops[k] = extractOp{
			Commands: commands, Note: n, Left: left, Status: o.Status,
			Stories: o.Stories, Requires: o.Requires, PartialUse: o.PartialUse,
		}
	}
	gaps := make([]string, 0, len(p.Gaps))
	for i, g := range p.Gaps {
		gg, rev := rewriteField(g)
		note(fmt.Sprintf("gaps[%d]", i), g, rev)
		gaps = append(gaps, gg)
	}
	asOps := map[string]extractASOp{}
	for k, o := range p.ASOps {
		cmd, r1 := rewriteField(o.Command)
		n, r2 := rewriteField(o.Note)
		note("as_ops["+k+"].command", o.Command, r1)
		note("as_ops["+k+"].note", o.Note, r2)
		asOps[k] = extractASOp{Command: cmd, Note: n}
	}
	releases := map[string]extractRelease{}
	for rel, cfg := range p.Releases {
		phases := make([]extractPhase, 0, len(cfg.Phases))
		for _, ph := range cfg.Phases {
			outc, rev := rewriteField(ph.Outcome)
			note("releases["+rel+"].phases["+ph.Name+"].outcome", ph.Outcome, rev)
			phases = append(phases, extractPhase{Name: ph.Name, Outcome: outc, IDs: ph.IDs})
		}
		checklist := make([]string, 0, len(cfg.Checklist))
		for i, item := range cfg.Checklist {
			ii, rev := rewriteField(item)
			note(fmt.Sprintf("releases[%s].checklist[%d]", rel, i), item, rev)
			checklist = append(checklist, ii)
		}
		scope, rev := rewriteField(cfg.Scope)
		note("releases["+rel+"].scope", cfg.Scope, rev)
		releases[rel] = extractRelease{
			Architecture: cfg.Architecture,
			Scope:        scope,
			Phases:       phases,
			Checklist:    checklist,
		}
	}

	enc := func(v any) ([]byte, error) {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	files := map[string][]byte{}
	var err error
	if files["commands.json"], err = enc(cmds); err != nil {
		return nil, "", err
	}
	if files["workflow.json"], err = enc(workflow); err != nil {
		return nil, "", err
	}
	if files["ops.json"], err = enc(ops); err != nil {
		return nil, "", err
	}
	if files["areas.json"], err = enc(p.Areas); err != nil {
		return nil, "", err
	}
	if files["gaps.json"], err = enc(gaps); err != nil {
		return nil, "", err
	}
	if files["as_ops.json"], err = enc(asOps); err != nil {
		return nil, "", err
	}
	if files["as_areas.json"], err = enc(p.ASAreas); err != nil {
		return nil, "", err
	}
	phaseDoc := map[string]any{"releases": releases}
	yb, err := yaml.Marshal(phaseDoc)
	if err != nil {
		return nil, "", err
	}
	header := []byte("# Generated by `cbdev docs extract-data` from capellactl scripts; checklist text adapted for cbctl.\n")
	files["phases.yaml"] = append(header, yb...)

	sort.Strings(reviewLines)
	review := strings.Join(reviewLines, "\n")
	if review != "" {
		review += "\n"
	}
	return files, review, nil
}

func checkExtractOut(outDir string, files map[string][]byte, stdout, stderr io.Writer) int {
	stale := false
	for name, body := range files {
		path := filepath.Join(outDir, name)
		cur, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "stale: missing %s\n", path)
			stale = true
			continue
		}
		if string(cur) != string(body) {
			fmt.Fprintf(stderr, "stale: %s; run 'cbdev docs extract-data --capellactl <dir>'\n", path)
			stale = true
		} else {
			fmt.Fprintf(stdout, "%s is up to date\n", path)
		}
	}
	if stale {
		return 1
	}
	return 0
}
