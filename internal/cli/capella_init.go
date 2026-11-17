package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/importer"
	"github.com/mminichino/cbctl/internal/output"
	"github.com/mminichino/cbctl/internal/presets"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
)

const (
	initOfficeHours = "08:00-18:00"
	initTimezone    = "US/Pacific"
	// initPlaceholder stands in for password variables while the answers are validated; it meets
	// Capella's password rules.
	initPlaceholder = "Cbctl-Init-Placeholder-1!"
)

var (
	initAccess = map[string][]any{
		"read":       {"data_reader"},
		"write":      {"data_writer"},
		"read-write": {"data_reader", "data_writer"},
	}
	envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

const initHeader = `# capella.yaml: your Capella environment, written by ` + "`cbctl capella init`" + `__ORG__.
# Next: ` + "`cbctl capella validate`, `cbctl capella plan`, `cbctl capella apply`" + `. Change it later with cbctl commands
# (` + "`cluster update|scale|protect`, `bucket …`, `cidr …`); `cbctl capella guide config <field>`" + ` explains every field.
# No secrets are written here: the API key stays in your keychain or CAPELLA_API_KEY, and passwords are
# ${ENV_VAR} references read when you run apply.

`

var initComments = map[string]string{
	"project":             "The Capella project (cbctl capella guide config project).",
	"clusters":            "Clusters of the project (cbctl capella guide config clusters).",
	"configurationType":   "singleNode: one node for development; it cannot be scaled out later.",
	"cloudProvider":       "Immutable after creation: provider, region and network (cbctl capella guide config clusters.cloudProvider).",
	"support":             "Updatable in place (cbctl capella guide config clusters.support).",
	"serviceGroups":       "Nodes and services; change with `cbctl capella cluster scale|update` (cbctl capella guide config clusters.serviceGroups).",
	"deletionProtection":  "true blocks deleting the cluster and its buckets (cbctl capella guide config clusters.deletionProtection).",
	"hibernation":         "Capella turns the cluster off outside these on-windows (cbctl capella guide config clusters.hibernation).",
	"allowAnyIp":          "Confirms that 0.0.0.0/0 (any IP address) is intended.",
	"allowedCidrs":        "IP ranges allowed to connect (cbctl capella guide config clusters.allowedCidrs).",
	"buckets":             "Buckets, scopes and collections (cbctl capella guide config clusters.buckets).",
	"databaseCredentials": "Passwords come from the environment variables named here (cbctl capella guide config clusters.databaseCredentials).",
}

// initScope is a scope and its collections.
type initScope struct {
	name  string
	colls []string
}

// initAnswers are the values the document is built from.
type initAnswers struct {
	orgID, orgName, project, projectID, cluster string
	provider, region, networkCIDR, preset       string
	nodes, cpu, ram                             int
	supportPlan                                 string
	protect                                     bool
	officeHours, timezone                       string
	cidrs                                       []string
	allowAnyIP                                  bool
	bucket                                      string
	bucketMemory                                int
	storageBackend                              string
	scopes                                      []initScope
	credential, access, passwordEnv             string
}

func (a *initAnswers) passwordEnvName() string {
	if a.passwordEnv != "" {
		return a.passwordEnv
	}
	return importer.PasswordEnvName(a.cluster, a.credential)
}

func (a *initAnswers) size() presets.Preset {
	if a.preset == "custom" {
		return presets.Preset{ConfigurationType: "multiNode", Availability: "multi", NumOfNodes: a.nodes, CPU: a.cpu, RAM: a.ram,
			Services: []string{"data", "index", "query"}}
	}
	return presets.Presets()[a.preset]
}

func diskObj(provider string) map[string]any {
	d := defaultDisk(provider)
	out := map[string]any{"type": d.Type}
	if d.Storage != nil {
		out["storage"] = int64(*d.Storage)
	}
	if d.IOPS != nil {
		out["iops"] = int64(*d.IOPS)
	}
	return out
}

// document builds the capella.yaml mapping from the answers.
func (a *initAnswers) document() map[string]any {
	size := a.size()
	cluster := map[string]any{"name": a.cluster}
	if size.ConfigurationType == "singleNode" {
		cluster["configurationType"] = "singleNode"
	}
	cluster["cloudProvider"] = map[string]any{"type": a.provider, "region": a.region, "cidr": a.networkCIDR}
	cluster["availability"] = map[string]any{"type": size.Availability}
	cluster["support"] = map[string]any{"plan": a.supportPlan, "timezone": "PT"}
	services := append([]string(nil), size.Services...)
	sort.Strings(services)
	svc := make([]any, len(services))
	for i, s := range services {
		svc[i] = s
	}
	cluster["serviceGroups"] = []any{map[string]any{
		"node": map[string]any{
			"compute": map[string]any{"cpu": int64(size.CPU), "ram": int64(size.RAM)},
			"disk":    diskObj(a.provider),
		},
		"numOfNodes": int64(size.NumOfNodes),
		"services":   svc,
	}}
	cluster["deletionProtection"] = a.protect
	if a.officeHours != "" {
		cluster["hibernation"] = map[string]any{"timezone": a.timezone, "weekdays": a.officeHours, "weekend": "off"}
	}
	if len(a.cidrs) > 0 {
		if a.allowAnyIP {
			cluster["allowAnyIp"] = true
		}
		list := make([]any, len(a.cidrs))
		for i, c := range a.cidrs {
			list[i] = map[string]any{"cidr": c}
		}
		cluster["allowedCidrs"] = list
	}
	if a.bucket != "" {
		bucket := map[string]any{"name": a.bucket, "memoryAllocationInMb": int64(a.bucketMemory), "storageBackend": a.storageBackend}
		if len(a.scopes) > 0 {
			scopes := make([]any, len(a.scopes))
			for i, s := range a.scopes {
				sc := map[string]any{"name": s.name}
				if len(s.colls) > 0 {
					colls := make([]any, len(s.colls))
					for j, c := range s.colls {
						colls[j] = map[string]any{"name": c}
					}
					sc["collections"] = colls
				}
				scopes[i] = sc
			}
			bucket["scopes"] = scopes
		}
		cluster["buckets"] = []any{bucket}
		if a.credential != "" {
			privs := initAccess[a.access]
			cluster["databaseCredentials"] = []any{map[string]any{
				"name":     a.credential,
				"password": "${" + a.passwordEnvName() + "}",
				"access": []any{map[string]any{
					"privileges": privs,
					"resources":  map[string]any{"buckets": []any{map[string]any{"name": a.bucket}}},
				}},
			}}
		}
	}
	project := map[string]any{"name": a.project}
	if a.projectID != "" {
		project["id"] = a.projectID
	}
	return map[string]any{
		"apiVersion":     capellafile.APIVersion,
		"organizationId": a.orgID,
		"project":        project,
		"clusters":       []any{cluster},
	}
}

var initKeyLine = regexp.MustCompile(`^( {0,4})(- )?([A-Za-z]+):`)

// render writes the document as YAML with a header and a comment above each section.
func (a *initAnswers) render() (string, error) {
	body, err := capellafile.Dump(a.document())
	if err != nil {
		return "", err
	}
	org := " for organization " + a.orgID
	if a.orgName != "" {
		org = fmt.Sprintf(" for organization %s (%s)", a.orgName, a.orgID)
	}
	var lines []string
	done := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if m := initKeyLine.FindStringSubmatch(line); m != nil && m[2] == "" {
			if c, ok := initComments[m[3]]; ok && !done[m[3]] {
				done[m[3]] = true
				lines = append(lines, m[1]+"# "+c)
			}
		}
		lines = append(lines, line)
	}
	return strings.Replace(initHeader, "__ORG__", org, 1) + strings.Join(lines, "\n") + "\n", nil
}

// issues validates the document the answers describe, offline.
func (a *initAnswers) issues() []capellafile.Issue {
	text, err := a.render()
	if err != nil {
		return []capellafile.Issue{{Path: "", Message: err.Error()}}
	}
	pwEnv := ""
	if a.credential != "" {
		pwEnv = a.passwordEnvName()
	}
	env := func(name string) string {
		if pwEnv != "" && name == pwEnv {
			return initPlaceholder
		}
		return DefaultDeps().Env(name)
	}
	_, err = capellafile.LoadText(text, "capella.yaml", ".", env)
	var ce *capellafile.ConfigError
	if err != nil {
		if asConfigError(err, &ce) {
			return ce.Issues
		}
		return []capellafile.Issue{{Message: err.Error()}}
	}
	return nil
}

func asConfigError(err error, target **capellafile.ConfigError) bool {
	ce, ok := err.(*capellafile.ConfigError)
	if ok {
		*target = ce
	}
	return ok
}

func issueAbout(i capellafile.Issue, prefixes []string) bool {
	for _, p := range prefixes {
		if i.Path == p || strings.HasPrefix(i.Path, p+".") || strings.HasPrefix(i.Path, p+"[") {
			return true
		}
	}
	return false
}

func issueMessage(i capellafile.Issue) string {
	where := i.Where
	if where == "" {
		where = i.Path
	}
	return where + ": " + i.Message
}

// ---- parsing helpers --------------------------------------------------------------------------

func parseCIDRs(text string) []string {
	var out []string
	for _, c := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func canonicalOrRaw(cidr string) string {
	if n, err := capellafile.NormalizeCIDR(cidr); err == nil {
		return n
	}
	return cidr
}

// parseInitScopes turns "inventory:items,suppliers;billing" into scopes with collections.
func parseInitScopes(items []string) []initScope {
	var out []initScope
	for _, item := range items {
		for _, part := range strings.Split(item, ";") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			name, colls, _ := strings.Cut(part, ":")
			s := initScope{name: strings.TrimSpace(name)}
			for _, c := range strings.Split(colls, ",") {
				if c = strings.TrimSpace(c); c != "" {
					s.colls = append(s.colls, c)
				}
			}
			out = append(out, s)
		}
	}
	return out
}

func formatInitScopes(scopes []initScope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = s.name
		if len(s.colls) > 0 {
			parts[i] += ":" + strings.Join(s.colls, ",")
		}
	}
	return strings.Join(parts, ";")
}

func envNameCheck(v string) []string {
	if envNameRE.MatchString(v) {
		return nil
	}
	return []string{fmt.Sprintf("%s is not an environment variable name (letters, digits, _)", v)}
}

// ---- command ----------------------------------------------------------------------------------

type initFlags struct {
	file                                  string
	nonInteractive, dryRun, force         bool
	project, projectID, cluster           string
	provider, region, networkCIDR, preset string
	nodes, cpu, ram                       int
	supportPlan                           string
	protect, noProtect                    bool
	hibernation, officeHours, timezone    string
	cidrs                                 []string
	allowAnyIP                            bool
	bucket                                string
	bucketMemory                          int
	storageBackend                        string
	scopes                                []string
	credential, access, passwordEnv       string
}

func newCapellaInitCmd() *cobra.Command {
	var f initFlags
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create capella.yaml by answering a few questions (or from flags)",
		Long: "Create capella.yaml by answering a few questions, or from flags with --non-interactive. " +
			"Read-only towards Capella: with an API key it lists organizations and projects; without one the " +
			"organization ID is typed. Every answer is validated like 'validate' does; nothing is written until " +
			"the whole file validates. Passwords are never written: credentials get password: ${ENV_VAR}.",
		Example: "  cbctl capella init\n  cbctl capella init --dry-run\n" +
			"  cbctl capella init --non-interactive --org-id ORG --project nova-dev --cluster nova-dev-01 \\\n" +
			"      --provider aws --region us-east-1 --preset small --hibernation office-hours --timezone US/Pacific --cidr 203.0.113.10/32",
		Args: cobra.NoArgs,
	}
	af := addAPIFlags(cmd)
	fl := cmd.Flags()
	fl.StringVar(&f.file, "file", "", "Output file (default: capella.yaml; .yml accepted)")
	fl.BoolVar(&f.nonInteractive, "non-interactive", false, "Take every answer from flags; never prompt")
	fl.BoolVar(&f.dryRun, "dry-run", false, "Print the YAML instead of writing the file")
	fl.BoolVar(&f.force, "force", false, "Overwrite an existing file without asking (a .bak is kept)")
	fl.StringVar(&f.project, "project", "", "Project name (new or existing)")
	fl.StringVar(&f.projectID, "project-id", "", "Existing project, when several share a name")
	fl.StringVar(&f.cluster, "cluster", "", "Cluster name")
	fl.StringVar(&f.provider, "provider", "", "Cloud provider: aws, gcp or azure (default aws)")
	fl.StringVar(&f.region, "region", "", "Region (default per provider)")
	fl.StringVar(&f.networkCIDR, "network-cidr", "", "Cluster network (default: first free 10.x.y.0/24)")
	fl.StringVar(&f.preset, "preset", "", "Size preset ("+strings.Join(presets.PresetNames(), ", ")+", custom; default small)")
	fl.IntVar(&f.nodes, "nodes", 0, "With --preset custom: nodes")
	fl.IntVar(&f.cpu, "cpu", 0, "With --preset custom: vCPUs per node")
	fl.IntVar(&f.ram, "ram", 0, "With --preset custom: RAM GB per node")
	fl.StringVar(&f.supportPlan, "support-plan", "", "Support plan: "+strings.Join(supportPlans, ", ")+" (default developer pro)")
	fl.BoolVar(&f.protect, "protect", false, "Turn deletion protection on (default)")
	fl.BoolVar(&f.noProtect, "no-protect", false, "Turn deletion protection off")
	fl.StringVar(&f.hibernation, "hibernation", "", "none or office-hours: turn the cluster off outside office hours")
	fl.StringVar(&f.officeHours, "office-hours", "", "Weekday on-window HH:MM-HH:MM (default "+initOfficeHours+")")
	fl.StringVar(&f.timezone, "timezone", "", "Hibernation timezone (default "+initTimezone+")")
	fl.StringArrayVar(&f.cidrs, "cidr", nil, "Allowed IP range (repeatable)")
	fl.BoolVar(&f.allowAnyIP, "allow-any-ip", false, "Required with --cidr 0.0.0.0/0")
	fl.StringVar(&f.bucket, "bucket", "", "Add a bucket")
	fl.IntVar(&f.bucketMemory, "bucket-memory", 0, "Bucket memory in MB (default 1024)")
	fl.StringVar(&f.storageBackend, "storage-backend", "", "Bucket storage: magma or couchstore (default magma)")
	fl.StringArrayVar(&f.scopes, "scope", nil, "Scope and collections, SCOPE[:COLL,...] (repeatable)")
	fl.StringVar(&f.credential, "credential", "", "Add an app credential with access to the bucket")
	fl.StringVar(&f.access, "access", "", "Credential access: read, write or read-write (default read-write)")
	fl.StringVar(&f.passwordEnv, "password-env", "", "Environment variable holding the credential password (default CBCTL_PWD_<CLUSTER>_<NAME>)")
	cmd.MarkFlagsMutuallyExclusive("protect", "no-protect")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return runInit(cmd, af, &f) }
	return cmd
}

// initIO prompts on stderr and reads answers from one buffered reader, or from the Prompter
// injected through Deps (tests script the interview with it).
type initIO struct {
	in  *bufio.Reader
	out io.Writer
	p   Prompter
}

// readAnswer prints label and returns the raw answer ("" for an empty line).
func (w *initIO) readAnswer(label, def string) (string, error) {
	fmt.Fprintf(w.out, "%s: ", label)
	if w.p != nil {
		v, err := w.p.Ask(label, def)
		fmt.Fprintln(w.out, v)
		if err != nil {
			return "", fmt.Errorf("answering %q: %w", label, err)
		}
		return v, nil
	}
	line, err := w.in.ReadString('\n')
	if err != nil && line == "" {
		return "", errInitEOF
	}
	return line, nil
}

var errInitEOF = Errorf(ExitError, "Run 'cbctl capella init --non-interactive' with flags instead.", "input ended before every question was answered")

func (w *initIO) ask(prompt, def string, choices []string) (string, error) {
	for {
		label := prompt
		if len(choices) > 0 {
			label += " (" + strings.Join(choices, "/") + ")"
		}
		if def != "" {
			label += " [" + def + "]"
		}
		line, err := w.readAnswer(label, def)
		if err != nil {
			return "", err
		}
		v := strings.TrimSpace(line)
		if v == "" {
			v = def
		}
		if len(choices) > 0 && !slices.Contains(choices, v) {
			writeLabeled(w.out, output.KindError, "%s is not one of %s", v, strings.Join(choices, ", "))
			continue
		}
		return v, nil
	}
}

func (w *initIO) yesNo(prompt string, def bool) (bool, error) {
	d := "n"
	if def {
		d = "y"
	}
	for {
		v, err := w.ask(prompt+" (y/n)", d, nil)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(v) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		writeLabeled(w.out, output.KindError, "answer y or n")
	}
}

func runInit(cmd *cobra.Command, af *apiFlags, f *initFlags) error {
	g, err := readGlobals(cmd)
	if err != nil {
		return err
	}
	out := newRenderer(cmd.OutOrStdout(), cmd.ErrOrStderr(), g.Output == "json", g.Quiet)
	in := cmd.InOrStdin()
	interactive := ttyCheck(in) && !f.nonInteractive
	errOut := cmd.ErrOrStderr()
	w := &initIO{in: bufio.NewReader(in), out: errOut, p: depsFromCmd(cmd).Prompter}

	disc, err := discoverConfig(cmd)
	if err != nil {
		return err
	}
	path := disc.Path
	if f.file != "" {
		path = f.file
		if !filepath.IsAbs(path) {
			if wd, werr := cwdFrom(cmd); werr == nil {
				path = filepath.Join(wd, path)
			}
		}
	}
	name := filepath.Base(path)
	_, statErr := os.Stat(path)
	exists := statErr == nil
	if exists && !f.dryRun && !f.force {
		if !interactive {
			return Errorf(ExitError,
				fmt.Sprintf("Pass --force to overwrite it (the current file is kept as %s.bak), or run 'cbctl capella init' in another folder.", name),
				"%s already exists", name)
		}
		ok, err := w.yesNo(name+" exists. Overwrite?", false)
		if err != nil {
			return err
		}
		if !ok {
			out.Line("Nothing was written.")
			return nil
		}
	}

	if interactive {
		start, err := w.ask("Start from a new environment, or an existing Capella project", "new", []string{"new", "import"})
		if err != nil {
			return err
		}
		if start == "import" {
			return initImport(cmd, af, f, path, w)
		}
	}

	client, orgID, orgName, missing, err := initOrganization(cmd, af, interactive, w)
	if err != nil {
		return err
	}
	if !interactive {
		if f.project == "" && f.projectID == "" {
			missing = append(missing, "--project")
		}
		if f.cluster == "" {
			missing = append(missing, "--cluster")
		}
		if len(missing) > 0 {
			return Errorf(ExitConfigInvalid, "Add them to the command ('cbctl capella init --help' describes each one).",
				"missing required options for --non-interactive: %s", strings.Join(missing, ", "))
		}
	}

	a := &initAnswers{orgID: orgID, orgName: orgName}
	project, pin, existing, err := initProject(cmd.Context(), client, f.project, f.projectID, interactive, w, out)
	if err != nil {
		return err
	}
	a.project, a.projectID = project, pin
	var used []string
	taken := map[string]bool{}
	for _, c := range existing {
		taken[c.Name] = true
		if c.CloudProvider != nil {
			used = append(used, c.CloudProvider.CIDR)
		}
	}
	freeName := func(v string) []string {
		if taken[v] {
			return []string{fmt.Sprintf("cluster %s already exists in project %s; use 'cbctl capella sync' to describe it, or choose another name", v, a.project)}
		}
		return nil
	}

	a.provider = orDefault(f.provider, "aws")
	a.region = orDefault(f.region, regionDefaults[a.provider])
	a.networkCIDR = orDefault(f.networkCIDR, suggestCIDR(used))
	a.preset = orDefault(f.preset, "small")
	a.nodes, a.cpu, a.ram = intOr(f.nodes, 3), intOr(f.cpu, 4), intOr(f.ram, 16)
	a.supportPlan = orDefault(f.supportPlan, "developer pro")
	a.protect = !f.noProtect
	if f.hibernation == "office-hours" {
		a.officeHours = orDefault(f.officeHours, initOfficeHours)
	}
	a.timezone = orDefault(f.timezone, initTimezone)
	for _, c := range f.cidrs {
		a.cidrs = append(a.cidrs, canonicalOrRaw(c))
	}
	a.allowAnyIP = f.allowAnyIP
	a.bucket = f.bucket
	a.bucketMemory = intOr(f.bucketMemory, 1024)
	a.storageBackend = orDefault(f.storageBackend, "magma")
	a.scopes = parseInitScopes(f.scopes)
	a.credential = f.credential
	a.access = orDefault(f.access, "read-write")
	a.passwordEnv = f.passwordEnv
	a.cluster = orDefault(f.cluster, a.project+"-01")

	if err := initCheckFlags(f); err != nil {
		return err
	}
	if !interactive {
		problems := freeName(a.cluster)
		if a.credential != "" {
			problems = append(problems, envNameCheck(a.passwordEnvName())...)
		}
		if len(problems) > 0 {
			return Errorf(ExitConfigInvalid, "Change the values named above, then run the command again.", "%s", strings.Join(problems, "\n"))
		}
	} else if err := initInterview(w, a, freeName, f.region != ""); err != nil {
		return err
	}
	if found := a.issues(); len(found) > 0 {
		return &CLIError{Code: ExitConfigInvalid,
			Message: capellafile.RenderIssues(fmt.Sprintf("The answers give a capella.yaml with %d problem(s):", len(found)), found),
			Action:  "Change the options named above ('cbctl capella init --help' lists them), then run the command again."}
	}

	text, err := a.render()
	if err != nil {
		return err
	}
	if f.dryRun {
		fmt.Fprint(cmd.OutOrStdout(), strings.TrimRight(text, "\n")+"\n")
		return nil
	}
	if exists {
		old, rerr := os.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("reading %s: %w", path, rerr)
		}
		backup := path + ".bak"
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", backup, err)
		}
		if !g.Quiet {
			fmt.Fprintf(errOut, "previous file saved as %s\n", filepath.Base(backup))
		}
	}
	if err := writeFileAtomic(path, text); err != nil {
		return err
	}
	ignoreCredentials(errOut, path, g.Quiet)
	if g.Output == "json" {
		out.Object(map[string]any{"file": path, "written": true, "valid": true})
		return nil
	}
	out.Line("✔ Wrote %s (validated)", displayPath(path))
	if a.credential != "" {
		out.Line("Next: export %s=…   then   cbctl capella plan", a.passwordEnvName())
	} else {
		out.Line("Next: cbctl capella plan")
	}
	return nil
}

func intOr(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

// initCheckFlags rejects flag values that are not one of the known choices.
func initCheckFlags(f *initFlags) error {
	check := func(flag, value string, allowed []string) error {
		if value == "" || slices.Contains(allowed, value) {
			return nil
		}
		return usageError("use one of "+strings.Join(allowed, ", "), "--%s %q is not valid", flag, value)
	}
	for _, c := range []struct {
		flag, v string
		allowed []string
	}{
		{"provider", f.provider, cloudProviders},
		{"preset", f.preset, append(presets.PresetNames(), "custom")},
		{"support-plan", f.supportPlan, supportPlans},
		{"hibernation", f.hibernation, []string{"none", "office-hours"}},
		{"storage-backend", f.storageBackend, []string{"magma", "couchstore"}},
		{"access", f.access, []string{"read", "write", "read-write"}},
	} {
		if err := check(c.flag, c.v, c.allowed); err != nil {
			return err
		}
	}
	return nil
}

// initOrganization finds the organization (and a client for it when an API key is available).
func initOrganization(cmd *cobra.Command, af *apiFlags, interactive bool, w *initIO) (client *capellaapi.Client, orgID, orgName string, missing []string, err error) {
	resolved, err := resolveSettings(cmd, settings.Inputs{
		Profile: mustProfile(cmd), Token: af.token, OrgID: af.orgID,
		APIHost: flagIfChanged(cmd, "api-host"), ConfigFile: flagIfChanged(cmd, "config"),
	})
	if err != nil {
		return nil, "", "", nil, Errorf(ExitConfigInvalid, settingsAction, "%v", err)
	}
	defaultOrg := af.orgID
	if defaultOrg == "" && resolved.OrgID.IsSet() {
		defaultOrg = resolved.OrgID.Value
	}
	if !resolved.APIKey.IsSet() {
		printNotice(cmd.ErrOrStderr(), "No API key found, so init cannot list your organizations and projects",
			"Type the organization ID at the next question, or save a key first with 'cbctl capella configure'.")
		switch {
		case af.orgID != "" || (defaultOrg != "" && !interactive):
			return nil, defaultOrg, "", nil, nil
		case !interactive:
			return nil, "", "", []string{"--org-id"}, nil
		}
		id, aerr := w.ask("Organization ID", defaultOrg, nil)
		return nil, strings.TrimSpace(id), "", nil, aerr
	}
	opts := []capellaapi.Option{
		capellaapi.WithBaseURL(resolved.BaseURL.Value),
		capellaapi.WithClock(clockFactory()),
		capellaapi.WithUserAgent(cliVersion()),
	}
	c, err := capellaapi.New(resolved.APIKey.Value, opts...)
	if err != nil {
		return nil, "", "", nil, Errorf(ExitConfigInvalid, settingsAction, "%v", err)
	}
	ctx := cmd.Context()
	orgs, err := c.ListOrganizations(ctx)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("listing organizations: %w", err)
	}
	var chosen *capellaapi.Organization
	find := func(id string) *capellaapi.Organization {
		for i := range orgs {
			if orgs[i].ID == id {
				return &orgs[i]
			}
		}
		return &capellaapi.Organization{ID: id}
	}
	switch {
	case af.orgID != "":
		chosen = find(af.orgID)
	case len(orgs) == 1:
		chosen = &orgs[0]
	case len(orgs) > 0 && interactive:
		fmt.Fprintln(cmd.ErrOrStderr(), "Organizations:")
		for i, o := range orgs {
			fmt.Fprintf(cmd.ErrOrStderr(), "  %d. %s (%s)\n", i+1, o.Name, o.ID)
		}
		idx, aerr := askIndex(w, "Organization", len(orgs), indexOfOrg(orgs, defaultOrg))
		if aerr != nil {
			return nil, "", "", nil, aerr
		}
		chosen = &orgs[idx-1]
	case defaultOrg != "":
		chosen = find(defaultOrg)
	}
	if chosen == nil {
		return nil, "", "", []string{"--org-id"}, nil
	}
	c.SetOrgID(chosen.ID)
	if chosen.Name != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Organization: %s (%s)\n", chosen.Name, chosen.ID)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "Organization: %s\n", chosen.ID)
	}
	return c, chosen.ID, chosen.Name, nil, nil
}

func mustProfile(cmd *cobra.Command) string {
	g, err := readGlobals(cmd)
	if err != nil {
		return ""
	}
	return g.Profile
}

func indexOfOrg(orgs []capellaapi.Organization, id string) int {
	for i, o := range orgs {
		if o.ID == id {
			return i + 1
		}
	}
	return 1
}

func askIndex(w *initIO, prompt string, n, def int) (int, error) {
	for {
		v, err := w.ask(prompt, strconv.Itoa(def), nil)
		if err != nil {
			return 0, err
		}
		i, convErr := strconv.Atoi(v)
		if convErr == nil && i >= 1 && i <= n {
			return i, nil
		}
		writeLabeled(w.out, output.KindError, "enter a number from 1 to %d", n)
	}
}

// initProject returns the project name, the id to pin when the name is ambiguous, and the clusters
// the project already has in Capella.
func initProject(ctx context.Context, client *capellaapi.Client, wanted, wantedID string, interactive bool, w *initIO, out Renderer) (string, string, []capellaapi.Cluster, error) {
	var projects []capellaapi.Project
	if client != nil {
		var err error
		if projects, err = client.ListProjects(ctx); err != nil {
			return "", "", nil, fmt.Errorf("listing projects: %w", err)
		}
	}
	clustersOf := func(p capellaapi.Project) ([]capellaapi.Cluster, error) {
		if client == nil {
			return nil, nil
		}
		return client.ListClusters(ctx, p.ID)
	}
	if wantedID != "" {
		found := false
		for _, p := range projects {
			if p.ID == wantedID {
				wanted, found = p.Name, true
			}
		}
		if !found {
			return "", "", nil, Errorf(ExitConfigInvalid, "Check the ID with 'cbctl capella project list', then run the command again.",
				"project %s not found", wantedID)
		}
	}
	if interactive && wanted == "" {
		if len(projects) > 0 {
			fmt.Fprintln(w.out, "Existing projects:")
			for i, p := range projects {
				fmt.Fprintf(w.out, "  %d. %s\n", i+1, p.Name)
			}
		}
		answer, err := w.ask("Project (number of an existing project, or a new name)", "", nil)
		if err != nil {
			return "", "", nil, err
		}
		if n, convErr := strconv.Atoi(answer); convErr == nil && n >= 1 && n <= len(projects) {
			chosen := projects[n-1]
			same := 0
			for _, p := range projects {
				if p.Name == chosen.Name {
					same++
				}
			}
			pin := ""
			if same > 1 {
				pin = chosen.ID
			}
			cl, err := clustersOf(chosen)
			return chosen.Name, pin, cl, err
		}
		wanted = answer
	}
	var same []capellaapi.Project
	for _, p := range projects {
		if p.Name == wanted && (wantedID == "" || p.ID == wantedID) {
			same = append(same, p)
		}
	}
	total := 0
	for _, p := range projects {
		if p.Name == wanted {
			total++
		}
	}
	switch {
	case len(same) > 1 && !interactive:
		ids := make([]string, len(same))
		for i, p := range same {
			ids[i] = p.ID
		}
		return "", "", nil, Errorf(ExitConfigInvalid, "Add --project-id with the ID of the one to use.",
			"%d projects are named %s (ids: %s)", len(same), wanted, strings.Join(ids, ", "))
	case len(same) > 1:
		fmt.Fprintf(w.out, "%d projects are named %s:\n", len(same), wanted)
		for i, p := range same {
			fmt.Fprintf(w.out, "  %d. %s (%s)\n", i+1, p.Name, p.ID)
		}
		idx, err := askIndex(w, "Which one", len(same), 1)
		if err != nil {
			return "", "", nil, err
		}
		cl, err := clustersOf(same[idx-1])
		return wanted, same[idx-1].ID, cl, err
	case len(same) == 1:
		pin := ""
		if wantedID != "" && total > 1 {
			pin = same[0].ID
		}
		cl, err := clustersOf(same[0])
		return wanted, pin, cl, err
	}
	if client != nil {
		fmt.Fprintf(w.out, "Project %s does not exist yet; apply will create it.\n", wanted)
	}
	return wanted, "", nil, nil
}

// initInterview asks the questions of the interactive flow, validating each answer as it is given.
func initInterview(w *initIO, a *initAnswers, freeName func(string) []string, regionGiven bool) error {
	ask := func(prompt, def string, choices []string, about []string, set func(string) []string, check func(string) []string) error {
		for {
			v, err := w.ask(prompt, def, choices)
			if err != nil {
				return err
			}
			var problems []string
			if check != nil {
				problems = check(v)
			}
			if len(problems) == 0 {
				problems = set(v)
			}
			if len(problems) == 0 {
				for _, i := range a.issues() {
					if issueAbout(i, about) {
						problems = append(problems, issueMessage(i))
					}
				}
			}
			if len(problems) == 0 {
				return nil
			}
			writeLabeled(w.out, output.KindError, "%s", strings.Join(problems, "\n"))
			writeAction(w.out, "Please answer again.")
			def = v
		}
	}
	str := func(dst *string) func(string) []string {
		return func(v string) []string { *dst = v; return nil }
	}
	num := func(dst *int) func(string) []string {
		return func(v string) []string {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return []string{v + " is not a positive number"}
			}
			*dst = n
			return nil
		}
	}
	if err := ask("Cluster name", a.cluster, nil, []string{"clusters[0].name"}, str(&a.cluster), freeName); err != nil {
		return err
	}
	if err := ask("Cloud provider", a.provider, cloudProviders, []string{"clusters[0].cloudProvider.type"}, str(&a.provider), nil); err != nil {
		return err
	}
	if !regionGiven {
		a.region = regionDefaults[a.provider]
	}
	if err := ask("Region", a.region, nil, []string{"clusters[0].cloudProvider.region"}, str(&a.region), nil); err != nil {
		return err
	}
	if err := ask("Cluster network (must not overlap other clusters)", a.networkCIDR, nil, []string{"clusters[0].cloudProvider.cidr"},
		func(v string) []string { a.networkCIDR = canonicalOrRaw(v); return nil }, nil); err != nil {
		return err
	}
	names := presets.PresetNames()
	var described []string
	for _, n := range names {
		described = append(described, fmt.Sprintf("%s (%s)", n, presets.Presets()[n].Description))
	}
	fmt.Fprintf(w.out, "Size presets: %s\n", strings.Join(described, "; "))
	if err := ask("Size preset", a.preset, append(append([]string(nil), names...), "custom"),
		[]string{"clusters[0].cloudProvider.type", "clusters[0].configurationType", "clusters[0].availability", "clusters[0].serviceGroups"},
		str(&a.preset), nil); err != nil {
		return err
	}
	if a.preset == "custom" {
		group := []string{"clusters[0].serviceGroups"}
		for _, q := range []struct {
			prompt string
			dst    *int
		}{{"Nodes", &a.nodes}, {"vCPUs per node", &a.cpu}, {"RAM GB per node", &a.ram}} {
			if err := ask(q.prompt, strconv.Itoa(*q.dst), nil, group, num(q.dst), nil); err != nil {
				return err
			}
		}
	}
	if err := ask("Support plan", a.supportPlan, supportPlans, []string{"clusters[0].support"}, str(&a.supportPlan), nil); err != nil {
		return err
	}
	protect, err := w.yesNo("Protect from deletion?", a.protect)
	if err != nil {
		return err
	}
	a.protect = protect
	hibernate, err := w.yesNo("Hibernate outside office hours (weekdays only)?", a.officeHours != "")
	if err != nil {
		return err
	}
	if hibernate {
		if a.officeHours == "" {
			a.officeHours = initOfficeHours
		}
		if err := ask("Weekday on-window (HH:MM-HH:MM, minutes 00 or 30)", a.officeHours, nil, []string{"clusters[0].hibernation.weekdays"}, str(&a.officeHours), nil); err != nil {
			return err
		}
		if err := ask("Timezone", a.timezone, nil, []string{"clusters[0].hibernation.timezone"}, str(&a.timezone), nil); err != nil {
			return err
		}
	} else {
		a.officeHours = ""
	}

	if err := ask("Allowed IP ranges (CIDR, comma-separated; empty: none)", strings.Join(a.cidrs, ","), nil, []string{"clusters[0].allowedCidrs"},
		func(v string) []string {
			a.cidrs = nil
			a.allowAnyIP = false
			for _, c := range parseCIDRs(v) {
				a.cidrs = append(a.cidrs, canonicalOrRaw(c))
			}
			if hasAnyIP(a.cidrs) {
				ok, err := w.yesNo("0.0.0.0/0 lets any IP address connect. Allow it?", false)
				if err != nil {
					return []string{err.Error()}
				}
				a.allowAnyIP = ok
			}
			return nil
		}, nil); err != nil {
		return err
	}

	bucketPath := []string{"clusters[0].buckets"}
	if err := ask("Add a bucket now? Bucket name (empty: none)", a.bucket, nil, bucketPath,
		func(v string) []string { a.bucket = strings.TrimSpace(v); return nil }, nil); err != nil {
		return err
	}
	if a.bucket == "" {
		a.credential = ""
		return nil
	}
	if err := ask("Bucket memory (MB)", strconv.Itoa(a.bucketMemory), nil, bucketPath, num(&a.bucketMemory), nil); err != nil {
		return err
	}
	if err := ask("Storage backend", a.storageBackend, []string{"magma", "couchstore"}, bucketPath, str(&a.storageBackend), nil); err != nil {
		return err
	}
	if err := ask("Scopes and collections (scope:coll1,coll2;scope2; empty: none)", formatInitScopes(a.scopes), nil, bucketPath,
		func(v string) []string { a.scopes = parseInitScopes([]string{v}); return nil }, nil); err != nil {
		return err
	}
	credPath := []string{"clusters[0].databaseCredentials"}
	if err := ask("Add an app credential? Name (empty: none)", a.credential, nil, credPath,
		func(v string) []string { a.credential = strings.TrimSpace(v); return nil }, nil); err != nil {
		return err
	}
	if a.credential == "" {
		return nil
	}
	if err := ask("Access to "+a.bucket, a.access, []string{"read", "write", "read-write"}, credPath, str(&a.access), nil); err != nil {
		return err
	}
	return ask("Password environment variable", a.passwordEnvName(), nil, credPath, str(&a.passwordEnv), envNameCheck)
}

func hasAnyIP(cidrs []string) bool {
	for _, c := range cidrs {
		if c == "0.0.0.0/0" || c == "::/0" {
			return true
		}
	}
	return false
}

// initImport is the "existing Capella project" path: choose the project (and clusters) and write
// the file with the sync importer.
func initImport(cmd *cobra.Command, af *apiFlags, f *initFlags, path string, w *initIO) error {
	rt, err := newRuntimeWith(cmd, af, runtimeExtras{})
	if err != nil {
		var ce *CLIError
		if errors.As(err, &ce) && strings.Contains(ce.Message, "API key not set") {
			return Errorf(ExitConfigInvalid,
				"Set CAPELLA_API_KEY (or run 'cbctl configure'), or choose 'new' to describe a new environment without a key.",
				"importing an existing Capella project needs an API key")
		}
		return err
	}
	projects, err := rt.Client.ListProjects(rt.Ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	if len(projects) == 0 {
		return Errorf(ExitError, "Choose 'new' to describe a new environment instead.", "this organization has no projects to import")
	}
	fmt.Fprintln(w.out, "Projects:")
	for i, p := range projects {
		fmt.Fprintf(w.out, "  %d. %s (%s)\n", i+1, p.Name, p.ID)
	}
	idx, err := askIndex(w, "Project to import", len(projects), 1)
	if err != nil {
		return err
	}
	names, err := w.ask("Clusters to import (comma-separated names; empty: all)", "", nil)
	if err != nil {
		return err
	}
	var clusters []string
	for _, n := range strings.Split(names, ",") {
		if n = strings.TrimSpace(n); n != "" {
			clusters = append(clusters, n)
		}
	}
	sf := &syncFlags{projectIDs: []string{projects[idx-1].ID}, clusters: clusters, force: true, dryRun: f.dryRun, stdout: f.dryRun}
	s := &syncer{rt: rt, sf: sf, path: path, name: filepath.Base(path)}
	old, rerr := os.ReadFile(path)
	return s.fresh(rerr == nil, string(old))
}
