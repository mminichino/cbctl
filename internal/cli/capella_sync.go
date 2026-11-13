package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/importer"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type syncFlags struct {
	projects, skip, projectIDs, clusters, clusterIDs []string
	file                                             string
	dryRun, stdout, full, force                      bool
}

func newCapellaSyncCmd() *cobra.Command {
	var sf syncFlags
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Bring capella.yaml in line with Capella (writes it when there is none)",
		Long: "Bring capella.yaml in line with Capella: writes it when there is none, refreshes every project and adds " +
			"the organization's projects that are missing. Read-only towards Capella.",
		Example: "  cbctl capella sync                       (every project of the organization)\n" +
			"  cbctl capella sync --dry-run             (CI: exit 2 when capella.yaml is out of date)\n" +
			"  cbctl capella sync --project non-prod    (only this project; added when it is new)\n" +
			"  cbctl capella sync --stdout              (print the YAML, write nothing)",
		Args: cobra.NoArgs,
	}
	af := addAPIFlags(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&sf.projects, "project", nil, "Only these projects (repeatable; default: every project)")
	f.StringArrayVar(&sf.skip, "skip-project", nil, "Leave this project out now and on later syncs (recorded in capella.yaml; repeatable)")
	f.StringArrayVar(&sf.projectIDs, "project-id", nil, "Only these project IDs (repeatable), when several projects share a name")
	f.StringArrayVar(&sf.clusters, "cluster", nil, "Only these clusters (repeatable)")
	f.StringArrayVar(&sf.clusterIDs, "cluster-id", nil, "Only these cluster IDs (repeatable)")
	f.StringVar(&sf.file, "file", "", "File to write (default: capella.yaml, or --config)")
	f.BoolVar(&sf.dryRun, "dry-run", false, "Show the difference without writing; exit 2 when out of sync")
	f.BoolVar(&sf.stdout, "stdout", false, "Print the YAML instead of writing the file")
	f.BoolVar(&sf.full, "full", false, "Write every field, including values equal to the defaults")
	f.BoolVar(&sf.force, "force", false, "Replace a file that is not a cbctl file, or one for another organization (a .bak is kept)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runSync(cmd, af, &sf)
	}
	return cmd
}

// readOldFile reads path and reports whether it is a cbctl/capellactl file (projects: or project:).
func readOldFile(path string) (text string, old capellafile.Obj, ok bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, false, err
	}
	var doc map[string]any
	if yerr := yaml.Unmarshal(raw, &doc); yerr != nil || doc == nil {
		return string(raw), nil, false, nil
	}
	old, _ = capellafile.NormalizeNumbers(doc).(map[string]any)
	_, hasProjects := old["projects"]
	_, hasProject := old["project"]
	return string(raw), old, hasProjects || hasProject, nil
}

func runSync(cmd *cobra.Command, af *apiFlags, sf *syncFlags) error {
	disc, err := discoverConfig(cmd)
	if err != nil {
		return err
	}
	path := disc.Path
	if sf.file != "" {
		path = sf.file
		if !filepath.IsAbs(path) {
			if wd, werr := os.Getwd(); werr == nil {
				path = filepath.Join(wd, path)
			}
		}
	}
	_, statErr := os.Stat(path)
	exists := statErr == nil

	var oldText string
	var old capellafile.Obj
	isCbctl := false
	if exists {
		oldText, old, isCbctl, err = readOldFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
	}
	// The file's organization is only the last resort for choosing one. When the flag, environment
	// or profile already name an organization, the file is checked against it instead, so a file
	// for another organization is never rewritten with this one's projects.
	fileOrg := ""
	if isCbctl {
		if org, derr := capellafile.DeclaredOrgID(path, os.Getenv); derr == nil {
			fileOrg = org
		}
	}
	extra := runtimeExtras{}
	if fileOrg != "" {
		explicit, rerr := settings.Resolve(settings.Inputs{Profile: mustProfile(cmd), OrgID: af.orgID, ConfigFile: flagIfChanged(cmd, "config")})
		if rerr != nil || !explicit.OrgID.IsSet() {
			extra = runtimeExtras{FileOrgID: fileOrg, FileOrgSource: filepath.Base(path)}
		}
	}
	rt, err := newRuntimeWith(cmd, af, extra)
	if err != nil {
		return err
	}
	s := &syncer{rt: rt, sf: sf, path: path, name: filepath.Base(path)}

	switch {
	case exists && isCbctl && !sf.force:
		clientOrg := rt.Client.OrgID()
		if fileOrg != "" && clientOrg != "" && fileOrg != clientOrg {
			if !sf.stdout {
				return Errorf(ExitConfigInvalid,
					fmt.Sprintf("Pass --force to rewrite it for %s (the current file is kept as %s.bak), or --file PATH to write a separate file.", clientOrg, s.name),
					"%s is for organization %s, not %s; nothing was written", displayPath(path), fileOrg, clientOrg)
			}
			return s.fresh(false, "")
		}
		return s.refresh(oldText, old)
	case exists && !isCbctl && !sf.force && !sf.stdout:
		if rt.Yes || !ttyIn(rt) {
			return Errorf(ExitError,
				fmt.Sprintf("Pass --force to overwrite it (the current file is kept as %s.bak), or --file PATH to write a separate file.", s.name),
				"%s already exists and is not a cbctl file", displayPath(path))
		}
		ok, cerr := confirm(rt, fmt.Sprintf("%s exists. Overwrite it (a backup is kept as %s.bak)?", displayPath(path), s.name))
		if cerr != nil {
			return cerr
		}
		if !ok {
			rt.Out.Line("Sync cancelled; nothing was written.")
			return nil
		}
		return s.fresh(true, oldText)
	default:
		return s.fresh(exists, oldText)
	}
}

func ttyIn(rt *runtime) bool {
	_, ok := rt.terminalIn()
	return ok
}

type syncer struct {
	rt   *runtime
	sf   *syncFlags
	path string
	name string
}

// selectProjects resolves --project, --project-id and --skip-project against the organization.
func (s *syncer) selectProjects(all []capellaapi.Project) ([]capellaapi.Project, error) {
	if len(all) == 0 {
		return nil, Errorf(ExitError, "Create one with 'cbctl capella project create NAME'.", "this organization has no projects yet")
	}
	var out []capellaapi.Project
	seen := map[string]bool{}
	add := func(p capellaapi.Project) {
		if !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	for _, id := range s.sf.projectIDs {
		found := false
		for _, p := range all {
			if p.ID == id {
				add(p)
				found = true
			}
		}
		if !found {
			return nil, Errorf(ExitError, "Check the ID with 'cbctl capella project list', then run the command again.", "project %s not found", id)
		}
	}
	for _, name := range s.sf.projects {
		var match []capellaapi.Project
		for _, p := range all {
			if p.Name == name {
				match = append(match, p)
			}
		}
		switch {
		case len(match) == 0:
			names := make([]string, 0, len(all))
			for _, p := range all {
				names = append(names, p.Name)
			}
			sort.Strings(names)
			return nil, Errorf(ExitError, "Use one of the projects listed, then run the command again.",
				"project %s not found (projects: %s)", name, strings.Join(names, ", "))
		case len(match) > 1:
			ids := make([]string, len(match))
			for i, p := range match {
				ids[i] = p.ID
			}
			return nil, Errorf(ExitBlocked, "Add --project-id with the ID of the one to use.",
				"%d projects are named %s (ids: %s)", len(match), name, strings.Join(ids, ", "))
		}
		add(match[0])
	}
	if len(s.sf.projects) == 0 && len(s.sf.projectIDs) == 0 {
		for _, p := range all {
			if !contains(s.sf.skip, p.Name) {
				add(p)
			}
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *syncer) orgName() string {
	org, err := s.rt.Client.GetOrganization(s.rt.Ctx)
	if err != nil || org == nil {
		return ""
	}
	return org.Name
}

// importError maps importer failures to CLI errors.
func importError(err error) error {
	var mc *importer.MissingClustersError
	if errors.As(err, &mc) {
		return Errorf(ExitError, "Check the names with 'cbctl capella cluster list', then run the command again.", "%s", mc.Error())
	}
	return err
}

// fresh writes a new document from Capella.
func (s *syncer) fresh(exists bool, oldText string) error {
	rt, sf := s.rt, s.sf
	projects, err := rt.Client.ListProjects(rt.Ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	selected, err := s.selectProjects(projects)
	if err != nil {
		return err
	}
	res, err := importer.BuildDocument(rt.Ctx, rt.Client, selected, importer.Options{
		Full: sf.full, ClusterNames: sf.clusters, ClusterIDs: sf.clusterIDs,
	})
	if err != nil {
		return importError(err)
	}
	if len(sf.skip) > 0 {
		skips := make([]any, 0, len(sf.skip))
		for _, n := range sortedUnique(sf.skip) {
			skips = append(skips, n)
		}
		res.Data["skipProjects"] = skips
	}
	text, err := importer.Render(res, s.orgName())
	if err != nil {
		return fmt.Errorf("rendering %s: %w", s.name, err)
	}
	if sf.stdout {
		fmt.Fprint(rt.cmd.OutOrStdout(), strings.TrimRight(text, "\n")+"\n")
		return nil
	}
	if sf.dryRun {
		fmt.Fprint(rt.cmd.OutOrStdout(), strings.TrimRight(text, "\n")+"\n")
		return Errorf(ExitChanges, "Run 'cbctl capella sync' to write it from Capella.", "%s does not exist yet", s.name)
	}
	if exists {
		backup := s.path + ".bak"
		if err := os.WriteFile(backup, []byte(oldText), 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", backup, err)
		}
		rt.note("previous file saved as %s", displayPath(backup))
	}
	if err := writeFileAtomic(s.path, text); err != nil {
		return err
	}
	for _, w := range res.Warnings {
		printNotice(rt.errOut(), w.What, w.Action)
	}
	verb := "Imported"
	summary := importer.Summary(res)
	if rt.JSON {
		s.report(res, res.Warnings)
		return nil
	}
	rt.Out.Line("%s into %s", strings.Replace(summary, "Imported", verb, 1), displayPath(s.path))
	if res.Counts["cluster"] > 0 {
		rt.Out.Line("Next: cbctl capella plan    (reports no changes for what was imported)")
	} else {
		rt.Out.Line("Next: cbctl capella cluster create NAME    (deploys the first cluster of a project)")
	}
	return nil
}

func sortedUnique(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func writeFileAtomic(path, text string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".cbctl-*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// report prints the JSON result of sync.
func (s *syncer) report(res *importer.Result, warnings []importer.Notice) {
	clusters := 0
	projects := 0
	if ps, ok := res.Data["projects"].([]any); ok {
		projects = len(ps)
		for _, p := range ps {
			if m, ok := p.(map[string]any); ok {
				if cl, ok := m["clusters"].([]any); ok {
					clusters += len(cl)
				}
			}
		}
	}
	ws := make([]any, 0, len(warnings))
	for _, w := range warnings {
		ws = append(ws, map[string]any{"what": w.What, "action": w.Action})
	}
	s.rt.Out.Object(map[string]any{"file": s.path, "projects": projects, "clusters": clusters, "warnings": ws})
}

const syncGone = "None needed if it was deleted on purpose; otherwise restore %s from %s.bak and run 'cbctl capella apply' to create it again."

func objStr(o map[string]any, k string) string {
	s, _ := o[k].(string)
	return s
}

func objList(v any) []map[string]any {
	var out []map[string]any
	l, _ := v.([]any)
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// refresh brings an existing cbctl file in line with Capella, carrying over what only the file
// knows (password references, allowAnyIp) and what could not be read now.
func (s *syncer) refresh(oldText string, old capellafile.Obj) error {
	rt, sf := s.rt, s.sf
	ctx, client := rt.Ctx, rt.Client
	entries := (&capellafile.Config{Raw: old}).Projects()
	org, _ := old["organizationId"].(string)

	capella, err := client.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	inFile := map[string]bool{}
	for _, e := range entries {
		inFile[e.Name()] = true
	}
	var unknown []string
	for _, n := range sf.projects {
		found := inFile[n]
		for _, p := range capella {
			found = found || p.Name == n
		}
		if !found {
			unknown = append(unknown, n)
		}
	}
	for _, id := range sf.projectIDs {
		found := false
		for _, p := range capella {
			found = found || p.ID == id
		}
		if !found {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return Errorf(ExitConfigInvalid, "Check the names with 'cbctl capella project list', then run the command again.",
			"not in %s or in Capella: %s", s.name, strings.Join(unknown, ", "))
	}

	var oldSkipped []string
	if l, ok := old["skipProjects"].([]any); ok {
		for _, x := range l {
			if str, ok := x.(string); ok {
				oldSkipped = append(oldSkipped, str)
			}
		}
	}
	var skipped []string
	for _, n := range oldSkipped {
		if !contains(sf.projects, n) {
			skipped = append(skipped, n)
		}
	}
	for _, n := range sf.skip {
		if !contains(oldSkipped, n) {
			skipped = append(skipped, n)
		}
	}
	named := map[string]bool{}
	for _, n := range sf.projects {
		named[n] = true
	}
	for _, p := range capella {
		if contains(sf.projectIDs, p.ID) {
			named[p.Name] = true
		}
	}
	clusterFilter := len(sf.clusters) > 0 || len(sf.clusterIDs) > 0
	clusterWanted := func(c capellaapi.Cluster) bool {
		return !clusterFilter || contains(sf.clusters, c.Name) || contains(sf.clusterIDs, c.ID)
	}

	var results []*importer.Result
	var kept []string
	seen := map[string]bool{}
	covered := map[string]bool{}
	var warnings []importer.Notice
	addWarning := func(what, action string) { warnings = append(warnings, importer.Notice{What: what, Action: action}) }

	for _, e := range entries {
		name := e.Name()
		if contains(skipped, name) {
			addWarning(fmt.Sprintf("project %s removed from %s and skipped from now on", name, s.name),
				fmt.Sprintf("None needed. Run 'cbctl capella sync --project %s' to add it back.", name))
			continue
		}
		prev := map[string]any{}
		for k, v := range e.Data {
			prev[k] = v
		}
		prevClusters := make([]any, len(e.Clusters))
		for i, c := range e.Clusters {
			prevClusters[i] = c
		}
		prev["clusters"] = prevClusters
		refresh := len(named) == 0 || named[name]
		pinned := objStr(e.Data, "id")
		var match []capellaapi.Project
		for _, p := range capella {
			if (pinned != "" && p.ID == pinned) || (pinned == "" && p.Name == name) {
				match = append(match, p)
			}
		}
		if refresh && len(match) == 0 && pinned == "" {
			renamed, rerr := s.renamedProject(capella, entries, e)
			if rerr != nil {
				return rerr
			}
			if renamed != nil {
				match = []capellaapi.Project{*renamed}
				addWarning(fmt.Sprintf("project %s was renamed in Capella to %s; %s now uses the new name and its id", name, renamed.Name, s.name),
					fmt.Sprintf("Use --project %s in your commands and scripts from now on.", renamed.Name))
			}
		}
		for _, p := range match {
			covered[p.ID] = true
		}
		if refresh && len(match) > 1 {
			return Errorf(ExitBlocked,
				fmt.Sprintf("Run 'cbctl capella sync --force' to rewrite the file from Capella with each one's id (the current file is kept as %s.bak).", s.name),
				"%d projects are named %s, so cbctl cannot tell which one %s means", len(match), name, s.name)
		}
		if refresh && len(match) == 0 && wasDeployed(e) {
			addWarning(fmt.Sprintf("project %s no longer exists in Capella; removed from %s with its clusters and everything in them", name, s.name),
				fmt.Sprintf(syncGone, s.name, s.name))
			continue
		}
		if !refresh || len(match) == 0 {
			if refresh {
				addWarning(fmt.Sprintf("project %s is not in Capella yet; kept as it is in %s", name, s.name), "Run 'cbctl capella apply' to create it.")
			}
			proj := map[string]any{}
			for k, v := range e.Data {
				if k != "clusters" {
					proj[k] = v
				}
			}
			results = append(results, &importer.Result{Data: map[string]any{"project": proj, "clusters": prevClusters}})
			continue
		}
		proj := match[0]
		found, err := client.ListClusters(ctx, proj.ID)
		if err != nil {
			return fmt.Errorf("listing clusters in project %s: %w", proj.Name, err)
		}
		var chosen []capellaapi.Cluster
		for _, c := range found {
			if clusterWanted(c) {
				chosen = append(chosen, c)
				seen[c.Name], seen[c.ID] = true, true
			}
		}
		type recordedCred struct {
			access   any
			password string
		}
		recorded := map[[2]string]recordedCred{}
		for _, c := range e.Clusters {
			for _, u := range objList(c["databaseCredentials"]) {
				if u["access"] != nil {
					pw, _ := u["password"].(string)
					recorded[[2]string{objStr(c, "name"), objStr(u, "name")}] = recordedCred{u["access"], pw}
				}
			}
		}
		res, err := importer.BuildProject(ctx, client, proj, chosen, found, importer.Options{
			Full: sf.full,
			KnownAccess: func(cluster, credential string) (any, string, bool) {
				r, ok := recorded[[2]string{cluster, credential}]
				return r.access, r.password, ok
			},
		})
		if err != nil {
			return importError(err)
		}
		if pinned != "" {
			p := map[string]any{"id": pinned}
			if cur, ok := res.Data["project"].(map[string]any); ok {
				for k, v := range cur {
					p[k] = v
				}
			}
			res.Data["project"] = p
		}
		kept = append(kept, importer.CarryOverProject(prev, res)...)
		importer.KeepUnread(prev, res, s.name)
		newClusters := objList(res.Data["clusters"])
		if clusterFilter {
			done := map[string]bool{}
			for _, c := range newClusters {
				done[objStr(c, "name")] = true
			}
			gone := map[string]bool{}
			for _, c := range e.Clusters {
				n := objStr(c, "name")
				if objStr(c, "id") != "" && (contains(sf.clusters, n) || contains(sf.clusterIDs, objStr(c, "id"))) && !done[n] {
					gone[n] = true
				}
			}
			for n := range gone {
				seen[n] = true
			}
			for _, n := range sortedKeys(gone) {
				addWarning(fmt.Sprintf("cluster %s no longer exists in Capella; removed from %s with its buckets, credentials and other settings", n, s.name),
					fmt.Sprintf(syncGone, s.name, s.name))
			}
			merged := []any{}
			for _, c := range e.Clusters {
				if n := objStr(c, "name"); !done[n] && !gone[n] {
					merged = append(merged, c)
				}
			}
			for _, c := range newClusters {
				merged = append(merged, c)
			}
			res.Data["clusters"] = merged
		} else {
			live := map[string]bool{}
			for _, c := range newClusters {
				live[objStr(c, "name")] = true
			}
			for _, c := range e.Clusters {
				if objStr(c, "id") != "" && !live[objStr(c, "name")] {
					addWarning(fmt.Sprintf("cluster %s no longer exists in Capella; removed from %s with its buckets, credentials and other settings", objStr(c, "name"), s.name),
						fmt.Sprintf(syncGone, s.name, s.name))
				}
			}
		}
		results = append(results, res)
	}

	type addedProject struct {
		name string
		n    int
	}
	var added []addedProject
	for _, proj := range capella {
		if covered[proj.ID] || inFile[proj.Name] || contains(skipped, proj.Name) {
			continue
		}
		if (len(named) > 0 && !named[proj.Name]) || (clusterFilter && len(named) == 0) {
			continue
		}
		found, err := client.ListClusters(ctx, proj.ID)
		if err != nil {
			return fmt.Errorf("listing clusters in project %s: %w", proj.Name, err)
		}
		var chosen []capellaapi.Cluster
		for _, c := range found {
			if clusterWanted(c) {
				chosen = append(chosen, c)
				seen[c.Name], seen[c.ID] = true, true
			}
		}
		res, err := importer.BuildProject(ctx, client, proj, chosen, found, importer.Options{Full: sf.full})
		if err != nil {
			return importError(err)
		}
		results = append(results, res)
		added = append(added, addedProject{proj.Name, len(chosen)})
	}
	var missing []string
	for _, n := range append(append([]string(nil), sf.clusters...), sf.clusterIDs...) {
		if !seen[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return Errorf(ExitError, "Check the names with 'cbctl capella cluster list', then run the command again.",
			"cluster(s) not found in Capella: %s", strings.Join(missing, ", "))
	}

	combined := importer.Combine(results, client.OrgID())
	if strings.HasPrefix(org, "${") {
		combined.Data["organizationId"] = org
	}
	if out, ok := old["credentialsOutput"].(string); ok && out != "" {
		combined.Data["credentialsOutput"] = out
	}
	if len(skipped) > 0 {
		list := make([]any, len(skipped))
		for i, n := range skipped {
			list[i] = n
		}
		combined.Data["skipProjects"] = list
	}
	newText, err := importer.Render(combined, s.orgName())
	if err != nil {
		return fmt.Errorf("rendering %s: %w", s.name, err)
	}
	if sf.stdout {
		fmt.Fprint(rt.cmd.OutOrStdout(), strings.TrimRight(newText, "\n")+"\n")
		return nil
	}

	all := warnings
	for _, w := range combined.Warnings {
		dup := false
		for _, x := range warnings {
			dup = dup || x == w
		}
		if !dup {
			all = append(all, w)
		}
	}
	for _, w := range all {
		printNotice(rt.errOut(), w.What, w.Action)
	}
	for _, a := range added {
		rt.Out.Line("added project %s (%d cluster%s)", a.name, a.n, plural(a.n))
	}
	var off, offKept, offNot []string
	for _, info := range importer.SortedUnread(combined) {
		if importer.PoweredOff(info.Reason) {
			off = append(off, info.Name)
			if info.Kept {
				offKept = append(offKept, info.Name)
			} else {
				offNot = append(offNot, info.Name)
			}
		}
	}
	if len(off) > 0 {
		verb := "is"
		if len(off) != 1 {
			verb = "are"
		}
		rt.Out.Line("%d cluster%s %s turned off: %s", len(off), plural(len(off)), verb, strings.Join(off, ", "))
		if len(offKept) > 0 {
			rt.Out.Line("  kept from %s: buckets and credentials of %s (refreshed when they are on)", s.name, strings.Join(offKept, ", "))
		}
		if len(offNot) > 0 {
			rt.Out.Line("  not read yet: buckets and credentials of %s — run 'cbctl capella sync' again when they are on", strings.Join(offNot, ", "))
		}
	}

	var newDoc map[string]any
	if err := yaml.Unmarshal([]byte(newText), &newDoc); err != nil {
		return fmt.Errorf("checking the new %s: %w", s.name, err)
	}
	before := importer.Comparable(importer.ToProjectsForm(old))
	after := importer.Comparable(capellafile.NormalizeNumbers(newDoc))
	if reflect.DeepEqual(before, after) {
		nProjects, nClusters := 0, 0
		if ps, ok := combined.Data["projects"].([]any); ok {
			nProjects = len(ps)
			for _, p := range ps {
				if m, ok := p.(map[string]any); ok {
					nClusters += len(objList(m["clusters"]))
				}
			}
		}
		scope := ""
		if len(named) > 0 {
			scope = fmt.Sprintf(" for %s (other projects not checked)", strings.Join(sortedKeys(named), ", "))
		}
		skip := ""
		if len(skipped) > 0 {
			skip = "; skipped: " + strings.Join(skipped, ", ")
		}
		if rt.JSON {
			s.report(combined, all)
			return nil
		}
		rt.Out.Line("%s is already in sync with Capella%s: %d project%s, %d cluster%s%s.", s.name, scope, nProjects, plural(nProjects), nClusters, plural(nClusters), skip)
		return nil
	}
	beforeText, _ := capellafile.Dump(before)
	afterText, _ := capellafile.Dump(after)
	diff := unifiedDiff(strings.Split(strings.TrimRight(beforeText, "\n"), "\n"), strings.Split(strings.TrimRight(afterText, "\n"), "\n"),
		s.name+" (now)", s.name+" (from Capella)", 2)
	if !rt.JSON {
		rt.Out.Line("%s", diff)
	}
	if sf.dryRun {
		if rt.JSON {
			s.report(combined, all)
		}
		return Errorf(ExitChanges, "Run 'cbctl capella sync' to update it.", "%s is out of sync with Capella; nothing was written", s.name)
	}
	backup := s.path + ".bak"
	if err := os.WriteFile(backup, []byte(oldText), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", backup, err)
	}
	if err := writeFileAtomic(s.path, newText); err != nil {
		return err
	}
	for _, k := range kept {
		fmt.Fprintf(rt.errOut(), "kept %s\n", k)
	}
	if rt.JSON {
		s.report(combined, all)
		return nil
	}
	rt.Out.Line("%s updated from Capella (previous version kept as %s).", s.name, filepath.Base(backup))
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// wasDeployed is true when the project or one of its clusters carries an id, i.e. it existed in
// Capella once.
func wasDeployed(e capellafile.ProjectEntry) bool {
	if objStr(e.Data, "id") != "" {
		return true
	}
	for _, c := range e.Clusters {
		if objStr(c, "id") != "" {
			return true
		}
	}
	return false
}

// renamedProject finds the one Capella project, not in the file, that holds a cluster declared
// under entry: the project was renamed in Capella.
func (s *syncer) renamedProject(capella []capellaapi.Project, entries []capellafile.ProjectEntry, entry capellafile.ProjectEntry) (*capellaapi.Project, error) {
	declared := map[string]bool{}
	for _, c := range entry.Clusters {
		declared[objStr(c, "name")] = true
	}
	if len(declared) == 0 {
		return nil, nil
	}
	inFile := map[string]bool{}
	for _, e := range entries {
		inFile[e.Name()] = true
	}
	var found []capellaapi.Project
	for _, p := range capella {
		if inFile[p.Name] {
			continue
		}
		clusters, err := s.rt.Client.ListClusters(s.rt.Ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
		}
		for _, c := range clusters {
			if declared[c.Name] {
				found = append(found, p)
				break
			}
		}
	}
	if len(found) != 1 {
		return nil, nil
	}
	return &found[0], nil
}

// unifiedDiff renders a unified diff of a and b with n lines of context.
func unifiedDiff(a, b []string, aName, bName string, n int) string {
	type op struct {
		kind byte // ' ', '-', '+'
		text string
	}
	// Longest common subsequence table.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, op{'+', b[j]})
	}

	lines := []string{"--- " + aName, "+++ " + bName}
	for start := 0; start < len(ops); {
		for start < len(ops) && ops[start].kind == ' ' {
			start++
		}
		if start >= len(ops) {
			break
		}
		from := start - n
		if from < 0 {
			from = 0
		}
		end := start
		lastChange := start
		for end < len(ops) {
			if ops[end].kind != ' ' {
				lastChange = end
			} else if end-lastChange > 2*n {
				break
			}
			end++
		}
		to := lastChange + n + 1
		if to > len(ops) {
			to = len(ops)
		}
		aLine, bLine := 1, 1
		for _, o := range ops[:from] {
			if o.kind != '+' {
				aLine++
			}
			if o.kind != '-' {
				bLine++
			}
		}
		aCount, bCount := 0, 0
		var body []string
		for _, o := range ops[from:to] {
			if o.kind != '+' {
				aCount++
			}
			if o.kind != '-' {
				bCount++
			}
			body = append(body, string(o.kind)+o.text)
		}
		lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@", aLine, aCount, bLine, bCount))
		lines = append(lines, body...)
		start = to
	}
	return strings.Join(lines, "\n")
}
