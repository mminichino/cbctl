package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/spf13/cobra"
)

// runDirectClusterCreate creates a cluster in Capella without a capella.yaml (or with --direct).
// It builds the same cluster entry as the file-managed create, puts it in a throw-away
// capella.yaml that pins the existing project, and applies only that cluster, so the request
// body, plan, confirmation and wait are those of the managed path.
func runDirectClusterCreate(cmd *cobra.Command, args []string) error {
	name, err := resolveClusterName(cmd, args, "name")
	if err != nil {
		return err
	}
	af := readAPIFlags(cmd)
	af.apiHost = flagIfChanged(cmd, "api-host")
	rt, err := newRuntime(cmd, af)
	if err != nil {
		return err
	}
	project, err := pickCapellaProject(rt, cmd)
	if err != nil {
		return err
	}
	clusters, err := rt.Client.ListClusters(rt.Ctx, project.ID)
	if err != nil {
		return fmt.Errorf("listing clusters in project %s: %w", project.Name, err)
	}
	var used []usedCIDR
	for _, c := range clusters {
		if c.CloudProvider != nil && c.CloudProvider.CIDR != "" {
			used = append(used, usedCIDR{cluster: c.Name, cidr: c.CloudProvider.CIDR})
		}
	}
	usedList := make([]string, len(used))
	for i, u := range used {
		usedList[i] = u.cidr
	}

	if name == "" {
		if _, tty := rt.terminalIn(); !tty {
			return usageError("pass NAME, plus --size or --group, --provider and --region as needed",
				"the cluster name is required (no terminal for the wizard)")
		}
		w := newClusterWizard(shareStdin(cmd), stderrFrom(cmd))
		spec, err := w.run(cmd, suggestCIDR(usedList))
		if err != nil {
			return err
		}
		if err := spec.apply(cmd); err != nil {
			return err
		}
		name = spec.Name
	}
	if slices.ContainsFunc(clusters, func(c capellaapi.Cluster) bool { return c.Name == name }) {
		rt.report(map[string]any{"status": "exists", "cluster": name, "project": project.Name},
			"cluster %s already exists in project %s", name, project.Name)
		return nil
	}
	if !flagBool(cmd, "free-tier") {
		if cmd.Flags().Changed("cidr") {
			if clash, ok := overlapOf(flagString(cmd, "cidr"), used); ok {
				return usageError("pass --cidr with a free range, or leave it out to get one that does not overlap",
					"%s overlaps the network of cluster %s (%s)", flagString(cmd, "cidr"), clash.cluster, clash.cidr)
			}
		} else if err := cmd.Flags().Set("cidr", suggestCIDR(usedList)); err != nil {
			return err
		}
	}

	dir, err := os.MkdirTemp("", "cbctl-create-")
	if err != nil {
		return fmt.Errorf("creating a work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "capella.yaml")
	doc := fmt.Sprintf("apiVersion: cbctl/v1\nproject:\n  id: %s\n  name: %s\nclusters: []\n",
		strconv.Quote(project.ID), strconv.Quote(project.Name))
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		return fmt.Errorf("writing the work file: %w", err)
	}
	ed, err := capellafile.OpenEditor(path)
	if err != nil {
		return err
	}
	e := &envRuntime{runtime: rt, Disc: capellafile.Discovery{Path: path, Exists: true}, Ephemeral: true,
		TreeOptions: resources.Options{NoRenameHint: true}}
	e.RC = newRunContext(rt, nil)
	if err := cmd.Flags().Set("name", name); err != nil {
		return err
	}
	if cmd.Flags().Changed("project") {
		// The work file declares exactly this project; the builder must not look it up by flag.
		cmd.Flags().Lookup("project").Changed = false
	}
	m, err := managedClusterCreate(cmd, nil, e, ed)
	if err != nil {
		return err
	}
	m.Question = fmt.Sprintf("Create cluster %s in project %s?", name, project.Name)
	_, err = ChangeAndApply(rt.Ctx, e, ed, m, false)
	return err
}

// pickCapellaProject chooses the project of a direct create: --project, else the organization's
// only project.
func pickCapellaProject(rt *runtime, cmd *cobra.Command) (capellaapi.Project, error) {
	projects, err := rt.Client.ListProjects(rt.Ctx)
	if err != nil {
		return capellaapi.Project{}, fmt.Errorf("listing projects: %w", err)
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if cmd.Flags().Changed("project") {
		want := flagString(cmd, "project")
		for _, p := range projects {
			if p.Name == want {
				return p, nil
			}
		}
		return capellaapi.Project{}, usageError("create it with 'cbctl capella project create "+want+"'", "project %s not found", want)
	}
	switch len(projects) {
	case 0:
		return capellaapi.Project{}, usageError("create one with 'cbctl capella project create NAME'", "the organization has no project")
	case 1:
		return projects[0], nil
	}
	return capellaapi.Project{}, usageError("add --project NAME ("+strings.Join(names, ", ")+")", "the organization has several projects")
}
