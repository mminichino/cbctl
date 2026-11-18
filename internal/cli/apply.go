package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/yamlio"
	"github.com/spf13/cobra"
)

// stdinFile is the -f value that reads the manifest from standard input.
const stdinFile = "-"

// applyEnv is what a kind handler gets besides its document.
type applyEnv struct {
	// Deps are the invocation's dependencies (environment, streams, clock).
	Deps Deps
	// Set is the manifest file the document came from.
	Set *manifest.Set
	// DryRun is true when nothing may be written.
	DryRun bool
	// Yes is true when confirmation prompts are skipped (-y).
	Yes bool
}

// kindHandler applies one manifest document. It returns the report of what it did even when it
// fails (the error says why), so a partial apply is reported. It prints its own progress lines
// through the logging printer and must check ctx between steps.
type kindHandler func(ctx context.Context, env *applyEnv, doc *manifest.Document) (*manifest.Report, error)

// applyHandlers is the kind dispatch table: one function per manifest kind. A new kind is one
// line here and one file with its handler.
var applyHandlers = map[string]kindHandler{
	manifest.KindServer:  applyServerDoc,
	manifest.KindCapella: applyCapellaDoc,
}

// newApplyCmd builds `cbctl apply`.
func newApplyCmd() *cobra.Command {
	var files []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply -f FILE|-|DIR",
		Short: "Apply a cbctl/v1 manifest",
		Long: "Apply the CouchbaseServer and CouchbaseCapella documents of a cbctl/v1 manifest.\n\n" +
			"-f takes a file, - for standard input, or a directory (its *.yaml and *.yml files, in name order).\n" +
			"The whole manifest is validated and its ${VAR} references are resolved before anything changes.\n" +
			"Applying again changes nothing that already matches. A CouchbaseServer document needs the nodes\n" +
			"to be reachable; the passwords it names are only read from the environment or files.\n\n" +
			"Exit codes: 0 ok, 1 failure, 3 invalid manifest or usage, 4 blocked, 5 timeout, 130 interrupted.",
		Example: "  cbctl apply -f couchbase.yaml\n" +
			"  cbctl apply -f couchbase.yaml --dry-run\n" +
			"  cbctl apply -f manifests/ -o json\n" +
			"  cat couchbase.yaml | cbctl apply -f -",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd, files, dryRun)
		},
	}
	f := cmd.Flags()
	f.StringArrayVarP(&files, "filename", "f", nil, "Manifest file, - for stdin, or a directory (required)")
	f.BoolVar(&dryRun, "dry-run", false, "Validate the manifest and show what would be applied; change nothing")
	f.StringP("output", "o", "text", "Output format: text|json")
	f.BoolP("yes", "y", false, "Skip confirmation prompts")
	f.Bool("auto-approve", false, "Alias for --yes")
	f.BoolP("quiet", "q", false, "Only log warnings and errors, no progress output (env CBCTL_QUIET=1)")
	f.String("log-level", "info", "Log level: error|warn|info|debug")
	return cmd
}

// runApply is the body of `cbctl apply`: load and validate every document, resolve every secret,
// then hand the documents to their kind handlers in order.
func runApply(cmd *cobra.Command, files []string, dryRun bool) error {
	g, err := readGlobals(cmd)
	if err != nil {
		return err
	}
	logging.SetLevel(g.logLevel())
	d := depsFromCmd(cmd)
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if len(files) == 0 {
		return Errorf(ExitConfigInvalid, "pass a manifest file, - for stdin, or a directory: cbctl apply -f couchbase.yaml", "no manifest given (-f is required)")
	}

	sets, err := loadManifests(ctx, d, files)
	if err != nil {
		return err
	}
	if err := resolveManifests(sets); err != nil {
		return err
	}

	report := &manifest.ApplyReport{APIVersion: manifest.APIVersion, DryRun: dryRun}
	runErr := applySets(ctx, d, sets, dryRun, g.Yes, report)
	finishApplyReport(report)
	if g.Output == "json" {
		logging.Current().Renderer().Object(report)
	} else if runErr == nil {
		summarizeApply(report)
	}
	return runErr
}

// loadManifests loads every -f source offline. Problems of all files come back as one
// *manifest.ValidationError (exit 3); an unreadable source is a usage error too.
func loadManifests(ctx context.Context, d Deps, files []string) ([]*manifest.Set, error) {
	cwd, err := d.Cwd()
	if err != nil {
		return nil, fmt.Errorf("finding the working directory: %w", err)
	}
	paths, err := expandSources(d, cwd, files)
	if err != nil {
		return nil, err
	}
	var sets []*manifest.Set
	var problems *manifest.ValidationError
	seen := map[string]string{}
	for _, p := range paths {
		set, err := manifest.Load(ctx, manifest.Source{Path: p, Stdin: stdinReader(d), Cwd: cwd, Env: d.Env})
		var verr *manifest.ValidationError
		switch {
		case errors.As(err, &verr):
			if hint := capellaFileHint(cwd, p); hint != nil {
				return nil, hint
			}
			problems = mergeProblems(problems, verr)
			continue
		case err != nil:
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			return nil, Errorf(ExitConfigInvalid, "check the path given to -f", "%v", err)
		}
		for _, doc := range set.Docs {
			key := doc.Envelope.Kind + "/" + doc.Envelope.Metadata.Name
			if first, dup := seen[key]; dup {
				return nil, Errorf(ExitConfigInvalid, "each kind and name may appear once across the files given to -f",
					"%s declares %s, which %s already declares", set.Name, key, first)
			}
			seen[key] = set.Name
		}
		sets = append(sets, set)
	}
	if problems != nil {
		return nil, problems
	}
	return sets, nil
}

// stdinReader is the manifest input: standard input, limited to the manifest size cap so a
// runaway pipe cannot fill memory.
func stdinReader(d Deps) io.Reader {
	if d.Stdin == nil {
		return nil
	}
	return io.LimitReader(d.Stdin, yamlio.MaxInputBytes+1)
}

// expandSources turns the -f values into file paths: a directory becomes its *.yaml and *.yml
// files in name order, - stays as it is (once, and not from a terminal).
func expandSources(d Deps, cwd string, files []string) ([]string, error) {
	var out []string
	stdinSeen := false
	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			return nil, Errorf(ExitConfigInvalid, "pass a manifest file, - for stdin, or a directory", "-f needs a value")
		}
		if f == stdinFile {
			if stdinSeen {
				return nil, Errorf(ExitConfigInvalid, "read stdin once", "-f - was given twice")
			}
			stdinSeen = true
			if d.Stdin == nil || (d.IsTTY != nil && d.IsTTY(d.Stdin)) {
				return nil, Errorf(ExitConfigInvalid, "pipe the manifest in: cat couchbase.yaml | cbctl apply -f -", "-f - reads the manifest from stdin, but stdin is a terminal")
			}
			out = append(out, f)
			continue
		}
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, Errorf(ExitConfigInvalid, "check the path given to -f", "cannot read %s: %v", f, errors.Unwrap(err))
		}
		if !info.IsDir() {
			out = append(out, f)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, Errorf(ExitConfigInvalid, "check the path given to -f", "cannot read directory %s: %v", f, err)
		}
		var names []string
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
				names = append(names, e.Name())
			}
		}
		if len(names) == 0 {
			return nil, Errorf(ExitConfigInvalid, "put *.yaml or *.yml manifests in the directory", "directory %s holds no .yaml or .yml files", f)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, filepath.Join(f, n))
		}
	}
	return out, nil
}

// mergeProblems appends the issues of next to acc, keeping the first source name.
func mergeProblems(acc, next *manifest.ValidationError) *manifest.ValidationError {
	if acc == nil {
		return &manifest.ValidationError{Source: next.Source, Issues: append([]manifest.Issue(nil), next.Issues...)}
	}
	acc.Source += ", " + next.Source
	acc.Issues = append(acc.Issues, next.Issues...)
	return acc
}

var (
	kindLine       = regexp.MustCompile(`(?m)^kind:`)
	capellaFileKey = regexp.MustCompile(`(?m)^(projects|organizationId|credentialsOutput|skipProjects):|^apiVersion:\s*["']?capellactl/v1`)
)

// capellaFileHint returns the usage error for a file that is a capella.yaml (no kind, the
// capella.yaml keys) given to apply; nil for anything else.
func capellaFileHint(cwd, path string) error {
	if path == stdinFile {
		return nil
	}
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	text, err := os.ReadFile(p)
	if err != nil || bytes.Contains(text, []byte("\n---")) || kindLine.Match(text) || !capellaFileKey.Match(text) {
		return nil
	}
	return Errorf(ExitConfigInvalid, fmt.Sprintf("use 'cbctl capella apply -c %s' for capella.yaml files; 'cbctl apply -f' takes cbctl/v1 manifests with a kind", path),
		"%s is a capella.yaml file, not a cbctl/v1 manifest (it has no kind)", path)
}

// resolveManifests resolves the secret references of every document before anything is applied,
// so a missing variable stops the run with nothing changed.
func resolveManifests(sets []*manifest.Set) error {
	var problems *manifest.ValidationError
	for _, set := range sets {
		if err := set.Resolve(); err != nil {
			var verr *manifest.ValidationError
			if !errors.As(err, &verr) {
				return fmt.Errorf("resolving secrets of %s: %w", set.Name, err)
			}
			problems = mergeProblems(problems, verr)
		}
	}
	if problems != nil {
		return problems
	}
	return nil
}

// applySets runs the handlers of all documents in order and stops at the first failure; the
// documents after it are reported as not run. Cancellation is checked before every document.
func applySets(ctx context.Context, d Deps, sets []*manifest.Set, dryRun, yes bool, out *manifest.ApplyReport) error {
	var failed error
	for _, set := range sets {
		env := &applyEnv{Deps: d, Set: set, DryRun: dryRun, Yes: yes}
		for i := range set.Docs {
			doc := &set.Docs[i]
			name := doc.Envelope.Kind + "/" + doc.Envelope.Metadata.Name
			if failed != nil {
				out.Documents = append(out.Documents, &manifest.Report{Kind: doc.Envelope.Kind, Name: doc.Envelope.Metadata.Name,
					Source: set.Name, Status: manifest.ReportNotRun, Steps: []manifest.Step{}})
				continue
			}
			if err := ctx.Err(); err != nil {
				failed = interrupted(name, err)
				out.Documents = append(out.Documents, &manifest.Report{Kind: doc.Envelope.Kind, Name: doc.Envelope.Metadata.Name,
					Source: set.Name, Status: manifest.ReportNotRun, Steps: []manifest.Step{}})
				continue
			}
			handler, ok := applyHandlers[doc.Envelope.Kind]
			if !ok { // manifest.Load rejects other kinds; keep the table and the loader in step
				failed = Errorf(ExitConfigInvalid, "use kind CouchbaseServer or CouchbaseCapella", "%s has kind %q, which apply does not handle", name, doc.Envelope.Kind)
				continue
			}
			rep, err := handler(ctx, env, doc)
			if rep == nil {
				rep = &manifest.Report{Kind: doc.Envelope.Kind, Name: doc.Envelope.Metadata.Name}
			}
			rep.Kind, rep.Name, rep.Source = doc.Envelope.Kind, doc.Envelope.Metadata.Name, set.Name
			if rep.Steps == nil {
				rep.Steps = []manifest.Step{}
			}
			if err != nil {
				rep.Status = manifest.ReportFailed
				rep.Error = err.Error()
				failed = err
				if errors.Is(err, context.Canceled) {
					failed = interrupted(name, err)
				}
			}
			rep.Finish()
			out.Documents = append(out.Documents, rep)
		}
	}
	return failed
}

// interrupted is the error for a run stopped by Ctrl-C (exit 130).
func interrupted(doc string, err error) error {
	return &CLIError{Code: ExitInterrupted, Message: "interrupted while applying " + doc + "; steps that completed stay in place",
		Action: "run the same command again to finish; steps already done are skipped", Err: errors.Join(context.Canceled, err)}
}

// finishApplyReport sets the overall status of the JSON report from its documents.
func finishApplyReport(r *manifest.ApplyReport) {
	status := manifest.ReportUnchanged
	for _, d := range r.Documents {
		switch d.Status {
		case manifest.ReportFailed, manifest.ReportNotRun:
			r.Status = manifest.ReportFailed
			return
		case manifest.ReportPlanned:
			status = manifest.ReportPlanned
		case manifest.ReportApplied:
			if status != manifest.ReportPlanned {
				status = manifest.ReportApplied
			}
		}
	}
	r.Status = status
}

// summarizeApply prints the closing lines of a text run.
func summarizeApply(r *manifest.ApplyReport) {
	p := logging.Current()
	for _, d := range r.Documents {
		name := d.Kind + "/" + d.Name
		switch d.Status {
		case manifest.ReportApplied:
			p.OK("%s applied", name)
		case manifest.ReportPlanned:
			p.Info("%s: dry run, nothing was changed", name)
		default:
			p.Info("%s: no changes, already matches the manifest", name)
		}
	}
}
