package cli

import (
	"bufio"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

// followUp is what configure learned while saving a profile, for the step after it.
type followUp struct {
	key, base      string
	orgID, orgName string
	interactive    bool // a person can answer: a terminal, and stdin did not carry the key
}

// configureFollowUp is the step after a saved profile (capellactl CAP-036/CAP-047): it points to
// capella.yaml, or writes it with sync. An existing capella.yaml is checked against the key's
// organization and is never rewritten without --import or a yes. Everything here is advice: a
// failure to list projects is not an error, the profile is already saved.
func configureFollowUp(ctx context.Context, cmd *cobra.Command, f *configureFlags, in *bufio.Reader, fu followUp) error {
	if f.output == "json" {
		return nil
	}
	disc, err := discoverConfig(cmd)
	if err != nil {
		logging.Debug("configure: not checking capella.yaml: %v", err)
		return nil
	}
	name := filepath.Base(disc.Path)
	label := fu.orgName
	if label == "" {
		label = fu.orgID
	}
	client, err := capellaapi.New(fu.key, capellaapi.WithBaseURL(fu.base), capellaapi.WithOrgID(fu.orgID),
		capellaapi.WithClock(clockFactory()), capellaapi.WithUserAgent(cliVersion()))
	if err != nil {
		return nil //nolint:nilerr // advice only: the profile is saved
	}
	if disc.Exists && !f.doImport {
		return checkExistingFile(ctx, cmd, f, in, fu, client, disc.Path, label)
	}
	if f.noImport {
		return nil
	}
	if !disc.Exists {
		if projects, lerr := client.ListProjects(ctx); lerr == nil && len(projects) == 0 {
			logging.Info("This organization has no projects yet. Next: cbctl capella project create NAME    (creates it in Capella)")
			return nil
		}
	}
	if !f.doImport {
		if !fu.interactive {
			logging.Info("Next: cbctl capella sync    (writes %s from your Capella projects)", name)
			return nil
		}
		if !askYesNo(cmd, in, fmt.Sprintf("Discover your projects and clusters and write %s now?", name), true) {
			logging.Info("Later: cbctl capella sync")
			return nil
		}
	}
	return runSync(cmd, &apiFlags{token: fu.key, orgID: fu.orgID, apiHost: fu.base}, &syncFlags{force: disc.Exists})
}

// checkExistingFile warns when capella.yaml belongs to another organization or declares projects
// the organization does not have. It only rewrites the file after an explicit yes.
func checkExistingFile(ctx context.Context, cmd *cobra.Command, f *configureFlags, in *bufio.Reader, fu followUp,
	client *capellaapi.Client, path, label string) error {
	name := filepath.Base(path)
	fileOrg, err := capellafile.DeclaredOrgID(path, envFrom(cmd))
	if err == nil && fileOrg != "" && fileOrg != fu.orgID {
		printNotice(stderrFrom(cmd), fmt.Sprintf("%s is for organization %s, but this key is for %s (%s); commands that read %s will use organization %s and fail with this key",
			name, fileOrg, label, fu.orgID, name, fileOrg), fmt.Sprintf("Either rewrite it for %s with 'cbctl capella sync --force' (the current file is kept as %s.bak), "+
			"or keep it and save a key for %s in its own profile with 'cbctl configure --profile NAME'.", label, name, fileOrg))
		if fu.interactive && !f.noImport && askYesNo(cmd, in, fmt.Sprintf("Back up %s and re-import it for %s?", name, label), false) {
			return runSync(cmd, &apiFlags{token: fu.key, orgID: fu.orgID, apiHost: fu.base}, &syncFlags{force: true})
		}
		return nil
	}
	if !f.noImport {
		logging.Info("%s already exists; to refresh it from Capella: cbctl capella sync", name)
	}
	ed, err := capellafile.OpenEditor(path)
	if err != nil {
		return nil //nolint:nilerr // advice only: the profile is saved
	}
	declared := ed.ProjectNames()
	if len(declared) == 0 {
		return nil
	}
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil //nolint:nilerr // advice only: the profile is saved
	}
	have := map[string]bool{}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		have[p.Name] = true
		names = append(names, p.Name)
	}
	sort.Strings(names)
	list := strings.Join(names, ", ")
	if list == "" {
		list = "none"
	}
	for _, p := range declared {
		if p != "" && !have[p] {
			printNotice(stderrFrom(cmd), fmt.Sprintf("%s declares project %s, which is not in organization %s (projects: %s)", name, p, label, list), fmt.Sprintf("Run 'cbctl capella project create %s' to create it, or 'cbctl capella sync --force' to rewrite %s for %s.", p, name, label))
		}
	}
	return nil
}

// askYesNo asks a yes/no question with the scripted prompter when one is set, else on in. An empty
// answer is def.
func askYesNo(cmd *cobra.Command, in *bufio.Reader, question string, def bool) bool {
	if p := depsFromCmd(cmd).Prompter; p != nil {
		ok, err := p.Confirm(question, def)
		return err == nil && ok
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(stderrFrom(cmd), "%s %s: ", question, hint)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}
