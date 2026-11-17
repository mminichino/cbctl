package cli

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/plan"
)

// recordServerVersion writes the Couchbase Server version Capella chose for a cluster the
// command just deployed, so 'sync' has nothing to add afterwards (as capellactl did). It is
// informational: a failed read or edit leaves the file as apply saved it.
func recordServerVersion(e *envRuntime, p *plan.Plan, project, name string) {
	var action *plan.Action
	for _, a := range p.Actions {
		if a.Kind == "cluster" && a.Key == name && a.Type == plan.Create && a.Status == "done" && a.ID() != "" {
			action = a
			break
		}
	}
	if action == nil {
		return
	}
	pa := action.Ancestor("project")
	if pa == nil || pa.ID() == "" {
		return
	}
	cl, err := e.Client.GetCluster(e.Ctx, pa.ID(), action.ID())
	if err != nil || cl.CouchbaseServer == nil || cl.CouchbaseServer.Version == "" {
		return
	}
	path := e.Cfg.Path
	if path == "" {
		path = e.Disc.Path
	}
	ed, err := capellafile.OpenEditor(path)
	if err != nil {
		return
	}
	_, node, err := ed.FindCluster(name, project)
	if err != nil || node == nil || ed.Child(node, "couchbaseServer") != nil || ed.Child(node, "tier") != nil {
		return
	}
	capellafile.PutKey(node, "couchbaseServer", capellafile.Obj{"version": cl.CouchbaseServer.Version}, capellafile.ClusterKeyOrder)
	text, err := ed.Text()
	if err != nil {
		return
	}
	if _, err := ed.Save(text); err != nil {
		printNotice(e.errOut(), fmt.Sprintf("could not record the server version of %s in %s: %v", name, displayPath(path), err), "")
	}
}

// capellaNetworks is what Capella runs next to a cluster that a file-managed create adds: the
// clusters of the target project that capella.yaml does not declare, and the clusters of the
// organization's other projects. capellactl checked both before it wrote the file.
type capellaNetworks struct {
	// names are the clusters of the target project in Capella.
	names map[string]bool
	// undeclared are the target project's clusters that are in Capella but not in the file.
	undeclared []usedCIDR
	// elsewhere are the clusters of the other projects; cluster is "NAME in project P".
	elsewhere []usedCIDR
}

// readCapellaNetworks lists the organization's clusters around project (the file's project
// name). A project that is not in Capella yet has no clusters of its own.
func readCapellaNetworks(e *envRuntime, ed *capellafile.Editor, project string) (*capellaNetworks, error) {
	projects, err := e.Client.ListProjects(e.Ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects to check the cluster name and network: %w", err)
	}
	declared := map[string]bool{}
	if pn, ok := ed.FindProject(project); ok {
		for _, c := range ed.ClusterNodes(pn) {
			if v := ed.Child(c, "name"); v != nil {
				declared[v.Value] = true
			}
		}
	}
	fileID := ""
	if pn, ok := ed.FindProject(project); ok {
		if v := ed.Child(pn, "id"); v != nil {
			fileID = v.Value
		}
	}
	n := &capellaNetworks{names: map[string]bool{}}
	for _, p := range projects {
		clusters, err := e.Client.ListClusters(e.Ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
		}
		same := p.ID == fileID || (fileID == "" && p.Name == project)
		for _, c := range clusters {
			if same {
				n.names[c.Name] = true
			}
			if c.CloudProvider == nil || c.CloudProvider.CIDR == "" {
				continue
			}
			switch {
			case same && !declared[c.Name]:
				n.undeclared = append(n.undeclared, usedCIDR{cluster: c.Name, cidr: c.CloudProvider.CIDR})
			case !same:
				n.elsewhere = append(n.elsewhere, usedCIDR{cluster: c.Name + " (" + c.CloudProvider.CIDR + ") in project " + p.Name, cidr: c.CloudProvider.CIDR})
			}
		}
	}
	return n, nil
}

// used is every network the suggestion for a new cluster must avoid.
func (n *capellaNetworks) used(file []string) []string {
	out := append([]string(nil), file...)
	if n == nil {
		return out
	}
	for _, u := range n.undeclared {
		out = append(out, u.cidr)
	}
	for _, u := range n.elsewhere {
		out = append(out, u.cidr)
	}
	return out
}

// overlapError refuses a CIDR that overlaps a cluster of this project that only Capella knows
// (the file rules cover the declared ones). suggest is a free range for the action.
func (n *capellaNetworks) overlapError(cidr, suggest string) error {
	if n == nil {
		return nil
	}
	if clash, ok := overlapOf(cidr, n.undeclared); ok {
		return usageError("use a range that overlaps no other cluster, for example --cidr "+suggest,
			"%s overlaps the network of cluster %s (%s), which is in this project in Capella but not in capella.yaml",
			cidr, clash.cluster, clash.cidr)
	}
	return nil
}

// elsewhereOverlap is the cluster of another project whose network overlaps cidr, if any. That
// is allowed (projects do not share networks unless peered), so it is only a warning.
func (n *capellaNetworks) elsewhereOverlap(cidr string) (string, bool) {
	if n == nil {
		return "", false
	}
	clash, ok := overlapOf(cidr, n.elsewhere)
	return clash.cluster, ok
}

// wizardCIDRCheck validates a CIDR typed in the create wizard: a network address that overlaps
// no cluster of the file or of this project in Capella. A bad answer is asked again.
func wizardCIDRCheck(file []string, n *capellaNetworks) func(string) error {
	return func(v string) error {
		p, err := netip.ParsePrefix(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("give a CIDR such as 10.0.0.0/24")
		}
		if p.Masked() != p {
			return fmt.Errorf("%s is not a network address (did you mean %s?); try again", v, p.Masked())
		}
		used := make([]usedCIDR, 0, len(file))
		for _, c := range file {
			used = append(used, usedCIDR{cidr: c})
		}
		if clash, ok := overlapOf(v, used); ok {
			return fmt.Errorf("%s overlaps the network %s of a cluster in capella.yaml; try again with a range that overlaps no other cluster", v, clash.cidr)
		}
		if n != nil {
			if clash, ok := overlapOf(v, n.undeclared); ok {
				return fmt.Errorf("%s overlaps the network of cluster %s (%s), which is in this project in Capella but not in capella.yaml; "+
					"try again with a range that overlaps no other cluster", v, clash.cluster, clash.cidr)
			}
		}
		return nil
	}
}

// undeployedEntry reports whether cluster name is declared in project without an id and is not
// in Capella: it was recorded (for example with --no-apply) but never deployed.
func undeployedEntry(ed *capellafile.Editor, project, name string, n *capellaNetworks) bool {
	if n == nil || n.names[name] {
		return false
	}
	_, cl, err := ed.FindCluster(name, project)
	if err != nil || cl == nil {
		return false
	}
	v := ed.Child(cl, "id")
	return v == nil || v.Value == ""
}

// keptOnReplace are the parts of an undeployed cluster entry that a new create keeps: its child
// resources and settings that create does not set.
var keptOnReplace = []string{"deletionProtection", "hibernation", "backupSchedule", "allowedCidrs", "allowAnyIp",
	"buckets", "databaseCredentials", "appServices"}

// replaceUndeployed swaps the undeployed entry name of project for cluster, keeping its children.
func replaceUndeployed(ed *capellafile.Editor, project string, cluster capellafile.Obj) error {
	pn, old, err := ed.FindCluster(fmt.Sprint(cluster["name"]), project)
	if err != nil {
		return err
	}
	if old != nil {
		prev, err := ed.Decode(old)
		if err != nil {
			return fmt.Errorf("reading cluster %s: %w", cluster["name"], err)
		}
		for _, k := range keptOnReplace {
			if v, ok := prev[k]; ok {
				if _, set := cluster[k]; !set {
					cluster[k] = v
				}
			}
		}
		ed.RemoveCluster(pn, fmt.Sprint(cluster["name"]))
	}
	ed.AddCluster(pn, cluster)
	return nil
}
