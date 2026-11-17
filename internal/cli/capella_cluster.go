package cli

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/presets"
	"github.com/spf13/cobra"
)

// registerCapellaClusterCommands adds the Capella cluster commands beyond the legacy
// create|exists|destroy|test to the cluster command.
func registerCapellaClusterCommands(cmd *cobra.Command) {
	cmd.AddCommand(newCapellaClusterListCmd())
	cmd.AddCommand(newCapellaClusterGetCmd())
	cmd.AddCommand(newCapellaClusterOptionsCmd())
	cmd.AddCommand(newCapellaClusterDiskTypesCmd())
	cmd.AddCommand(newCapellaClusterScaleCmd())
	cmd.AddCommand(newCapellaClusterUpdateCmd())
	cmd.AddCommand(newCapellaClusterPowerCmd())
	cmd.AddCommand(newCapellaClusterProtectCmd())
	cmd.AddCommand(newCapellaClusterDeleteCmd())
	cmd.AddCommand(newCapellaClusterCloneCmd())
	cmd.AddCommand(newCapellaClusterGroupCmd())
	cmd.AddCommand(newCapellaClusterScheduleCmd())
	cmd.AddCommand(newCapellaClusterBackupCmd())
}

// clusterSummary is one row of cluster list. ProjectName repeats Project under capellactl's key, so
// scripts written for capellactl keep working.
type clusterSummary struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	Project     string `json:"project"`
	ProjectName string `json:"projectName"`
	ProjectID   string `json:"projectId"`
	Provider    string `json:"provider"`
	Region      string `json:"region"`
	Nodes       int    `json:"nodes"`
	State       string `json:"state"`
}

func newCapellaClusterListCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("list", "List clusters in all projects, or in one project", cobra.NoArgs, nil,
		func(rt *runtime, _ []string) error {
			var projects []capellaapi.Project
			if project != "" {
				p, err := rt.resolveProject(project)
				if err != nil {
					return err
				}
				projects = []capellaapi.Project{*p}
			} else {
				var err error
				if projects, err = rt.Client.ListProjects(rt.Ctx); err != nil {
					return fmt.Errorf("listing projects: %w", err)
				}
			}
			var items []clusterSummary
			for _, p := range projects {
				clusters, err := rt.Client.ListClusters(rt.Ctx, p.ID)
				if err != nil {
					return fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
				}
				for i := range clusters {
					c := &clusters[i]
					items = append(items, clusterSummary{
						Name: c.Name, ID: c.ID, Project: p.Name, ProjectName: p.Name, ProjectID: p.ID,
						Provider: clusterProvider(c), Region: clusterRegion(c),
						Nodes: totalNodes(c.ServiceGroups), State: c.CurrentState,
					})
				}
			}
			// By project, then by name, as capellactl lists them.
			sort.SliceStable(items, func(i, j int) bool {
				if items[i].Project != items[j].Project {
					return items[i].Project < items[j].Project
				}
				return items[i].Name < items[j].Name
			})
			rt.Out.List(items)
			rows := make([][]string, len(items))
			for i, c := range items {
				rows[i] = []string{c.Name, c.Project, c.Provider, c.Region, strconv.Itoa(c.Nodes), c.State}
			}
			rt.Out.Table([]string{"NAME", "PROJECT", "PROVIDER", "REGION", "NODES", "STATE"}, rows)
			return nil
		})
	cmd.Flags().StringVar(&project, "project", "", "Only clusters of this project")
	return cmd
}

// groupDetail is a service group in cluster get output.
type groupDetail struct {
	Services string `json:"services"`
	Nodes    int    `json:"nodes"`
	CPU      int    `json:"cpu"`
	RAM      int    `json:"ram"`
	Disk     string `json:"disk"`
}

// clusterDetail is the cluster get document.
type clusterDetail struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Project            string        `json:"project"`
	State              string        `json:"state"`
	Provider           string        `json:"provider"`
	Region             string        `json:"region"`
	CIDR               string        `json:"cidr"`
	ServerVersion      string        `json:"serverVersion"`
	Availability       string        `json:"availability"`
	SupportPlan        string        `json:"supportPlan"`
	SupportTimezone    string        `json:"supportTimezone"`
	ServiceGroups      []groupDetail `json:"serviceGroups"`
	ConnectionString   string        `json:"connectionString"`
	DeletionProtection bool          `json:"deletionProtection"`
}

func groupDetails(groups []capellaapi.ServiceGroup) []groupDetail {
	out := make([]groupDetail, 0, len(groups))
	for _, g := range groups {
		d := groupDetail{Services: groupKey(g), Nodes: g.NumOfNodes}
		if g.Node != nil {
			if g.Node.Compute != nil {
				d.CPU, d.RAM = g.Node.Compute.CPU, g.Node.Compute.RAM
			}
			d.Disk = diskText(g.Node.Disk)
		}
		out = append(out, d)
	}
	return out
}

// diskText renders a disk as "gp3 50 GB 3000 IOPS".
func diskText(d *capellaapi.Disk) string {
	if d == nil {
		return ""
	}
	parts := []string{d.Type}
	if d.Storage != nil {
		parts = append(parts, fmt.Sprintf("%d GB", *d.Storage))
	}
	if d.IOPS != nil {
		parts = append(parts, fmt.Sprintf("%d IOPS", *d.IOPS))
	}
	if d.AutoExpansion != nil && *d.AutoExpansion {
		parts = append(parts, "auto-expansion")
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func newCapellaClusterGetCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("get CLUSTER", "Show one cluster", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			p, listed, err := rt.resolveCluster(args[0], project)
			if err != nil {
				return err
			}
			cl, err := rt.readCluster(p.ID, listed.ID, isFreeCluster(listed))
			if err != nil {
				return err
			}
			d := clusterDetail{
				ID: cl.ID, Name: cl.Name, Project: p.Name, State: cl.CurrentState,
				Provider: clusterProvider(cl), Region: clusterRegion(cl),
				ConnectionString: cl.ConnectionString, ServiceGroups: groupDetails(cl.ServiceGroups),
			}
			if cl.CloudProvider != nil {
				d.CIDR = cl.CloudProvider.CIDR
			}
			if cl.CouchbaseServer != nil {
				d.ServerVersion = cl.CouchbaseServer.Version
			}
			if cl.Availability != nil {
				d.Availability = cl.Availability.Type
			}
			if cl.Support != nil {
				d.SupportPlan, d.SupportTimezone = cl.Support.Plan, cl.Support.Timezone
			}
			d.DeletionProtection = cl.DeletionProtection != nil && *cl.DeletionProtection
			rt.Out.Object(d)
			return nil
		})
	addProjectFlag(cmd, &project)
	return cmd
}

// ---- options ---------------------------------------------------------------------------------

// freeTierUser names the cluster that uses the organization's one free-tier cluster.
func freeTierUser(cluster, project string) string {
	return fmt.Sprintf("%s in project %s", cluster, project)
}

// freeTierUsedBy returns freeTierUser of the organization's free-tier cluster, or "" when the
// free tier is still available.
func (r *runtime) freeTierUsedBy() (string, error) {
	projects, err := r.Client.ListProjects(r.Ctx)
	if err != nil {
		return "", fmt.Errorf("listing projects: %w", err)
	}
	for _, p := range projects {
		clusters, err := r.Client.ListClusters(r.Ctx, p.ID)
		if err != nil {
			return "", fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
		}
		for i := range clusters {
			if isFreeCluster(&clusters[i]) {
				return freeTierUser(clusters[i].Name, p.Name), nil
			}
		}
	}
	return "", nil
}

// freeTierInUse is the refusal to create a second free-tier cluster (Capella allows one per
// organization).
func freeTierInUse(usedBy string) error {
	return blockedError("delete that cluster first, or create a provisioned cluster without --free-tier",
		"the free tier is already used by %s", usedBy)
}

type freeTierOptions struct {
	Available   bool                `json:"available"`
	UsedBy      string              `json:"usedBy"`
	Regions     map[string][]string `json:"regions"`
	Includes    string              `json:"includes"`
	NotIncluded string              `json:"notIncluded"`
}

type presetOption struct {
	Nodes   int    `json:"nodes"`
	CPU     int    `json:"cpu"`
	RAM     int    `json:"ram"`
	Single  bool   `json:"single,omitempty"`
	Summary string `json:"description"`
}

type provisionedOptions struct {
	Providers           []string                `json:"providers"`
	Sizes               map[string]presetOption `json:"sizes"`
	SupportPlans        []string                `json:"supportPlans"`
	SupportTimezones    []string                `json:"supportTimezones"`
	RegionsInUse        []string                `json:"regionsInUse"`
	RegionDefaults      map[string]string       `json:"regionDefaults"`
	Services            []string                `json:"services"`
	GroupLimits         string                  `json:"groupLimits"`
	NodeSizes           map[string][]string     `json:"nodeSizes"`
	DiskTypes           map[string][]string     `json:"diskTypes"`
	ServerVersionsInUse []string                `json:"serverVersionsInUse"`
}

type clusterOptions struct {
	FreeTier    freeTierOptions    `json:"freeTier"`
	Provisioned provisionedOptions `json:"provisioned"`
}

func newCapellaClusterOptionsCmd() *cobra.Command {
	return capellaLeaf("options", "Show which clusters you can deploy: free tier, providers, sizes, plans", cobra.NoArgs, nil,
		func(rt *runtime, _ []string) error {
			projects, err := rt.Client.ListProjects(rt.Ctx)
			if err != nil {
				return fmt.Errorf("listing projects: %w", err)
			}
			usedBy := ""
			regions, versions := map[string]bool{}, map[string]bool{}
			for _, p := range projects {
				clusters, err := rt.Client.ListClusters(rt.Ctx, p.ID)
				if err != nil {
					return fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
				}
				for i := range clusters {
					c := &clusters[i]
					if isFreeCluster(c) && usedBy == "" {
						usedBy = freeTierUser(c.Name, p.Name)
					}
					if c.CloudProvider != nil {
						regions[c.CloudProvider.Type+" "+c.CloudProvider.Region] = true
					}
					if c.CouchbaseServer != nil && c.CouchbaseServer.Version != "" {
						versions[c.CouchbaseServer.Version] = true
					}
				}
			}
			opts := buildClusterOptions(usedBy, sortedKeys(regions), sortedKeys(versions))
			if rt.JSON {
				rt.Out.Object(opts)
				return nil
			}
			printClusterOptions(rt, rt.Client.OrgID(), opts)
			return nil
		})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func buildClusterOptions(usedBy string, regionsInUse, versionsInUse []string) clusterOptions {
	sizes := map[string]presetOption{}
	for name, p := range presets.Presets() {
		sizes[name] = presetOption{Nodes: p.NumOfNodes, CPU: p.CPU, RAM: p.RAM,
			Single: p.ConfigurationType == "singleNode", Summary: p.Description}
	}
	nodeSizes := map[string][]string{}
	for prov, c := range presets.ComputeSizes() {
		list := make([]string, 0, len(c.Sizes))
		for _, s := range c.Sizes {
			list = append(list, s.String())
		}
		nodeSizes[prov] = list
	}
	disks := map[string][]string{}
	for prov, types := range presets.DiskTypes() {
		names := make([]string, 0, len(types))
		for n := range types {
			names = append(names, n)
		}
		sort.Strings(names)
		disks[prov] = names
	}
	return clusterOptions{
		FreeTier: freeTierOptions{Available: usedBy == "", UsedBy: usedBy, Regions: freeTierRegions,
			Includes: freeTierNote, NotIncluded: freeTierMissing},
		Provisioned: provisionedOptions{
			Providers: cloudProviders, Sizes: sizes, SupportPlans: supportPlans, SupportTimezones: supportTimezones,
			RegionsInUse: nonNil(regionsInUse), RegionDefaults: regionDefaults, Services: clusterServices,
			GroupLimits: groupLimitsText, NodeSizes: nodeSizes, DiskTypes: disks,
			ServerVersionsInUse: nonNil(versionsInUse),
		},
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func printClusterOptions(rt *runtime, org string, o clusterOptions) {
	out := rt.Out
	out.Line("Cluster options for organization %s", org)
	out.Line("")
	if o.FreeTier.Available {
		out.Line("Free tier          available")
	} else {
		out.Line("Free tier          not available: already used by %s", o.FreeTier.UsedBy)
	}
	out.Line("  %s", o.FreeTier.Includes)
	for _, prov := range cloudProviders {
		out.Line("  %-6s %s", prov, strings.Join(o.FreeTier.Regions[prov], ", "))
	}
	out.Line("  not included: %s", o.FreeTier.NotIncluded)
	out.Line("")
	out.Line("Provisioned        available")
	p := o.Provisioned
	out.Line("  providers        %s", strings.Join(p.Providers, ", "))
	names := presets.PresetNames()
	parts := make([]string, 0, len(names))
	for _, n := range names {
		s := p.Sizes[n]
		single := ""
		if s.Single {
			single = ", single node"
		}
		parts = append(parts, fmt.Sprintf("%s (%d × %d vCPU/%d GB%s)", n, s.Nodes, s.CPU, s.RAM, single))
	}
	out.Line("  sizes            %s", strings.Join(parts, ", "))
	out.Line("  support plans    %s (timezones %s)", strings.Join(p.SupportPlans, ", "), strings.Join(p.SupportTimezones, ", "))
	out.Line("  services         %s", strings.Join(p.Services, ", "))
	out.Line("  service groups   %s", p.GroupLimits)
	out.Line("  node sizes       vCPU/RAM GB (from Capella's documentation)")
	for _, prov := range cloudProviders {
		out.Line("    %-6s %s", prov, strings.Join(p.NodeSizes[prov], ", "))
	}
	disks := make([]string, 0, len(p.DiskTypes))
	for _, prov := range cloudProviders {
		if t := p.DiskTypes[prov]; len(t) > 0 {
			disks = append(disks, prov+" "+strings.Join(t, ", "))
		}
	}
	out.Line("  disk types       %s", strings.Join(disks, " · "))
	out.Line("                   (size, speed and use of each: cbctl capella cluster disk-types)")
	if len(p.ServerVersionsInUse) > 0 {
		out.Line("  server versions  in use by your clusters: %s", strings.Join(p.ServerVersionsInUse, ", "))
	}
	regions := "any region of the provider"
	if len(p.RegionsInUse) > 0 {
		regions += "; in use by your clusters: " + strings.Join(p.RegionsInUse, ", ")
	}
	out.Line("  regions          %s", regions)
	out.Line("  deploy: cbctl capella cluster create ... (see 'cbctl capella cluster create --help')")
}

// ---- disk types ------------------------------------------------------------------------------

// diskTypeView is one disk type in JSON output.
type diskTypeView struct {
	For       string  `json:"for"`
	Size      []int   `json:"size,omitempty"`
	Sizes     []int   `json:"sizes,omitempty"`
	FixedSize int     `json:"fixedSize,omitempty"`
	IOPS      []int   `json:"iops,omitempty"`
	IOPSCap   [][]int `json:"iopsCap,omitempty"`
	FixedIOPS int     `json:"fixedIops,omitempty"`
	PerGB     int     `json:"perGb,omitempty"`
	Defaults  [][]int `json:"defaults,omitempty"`
}

func presetDisk(provider, diskType string) (presets.DiskType, bool) {
	d, ok := presets.DiskTypes()[provider][diskType]
	return d, ok
}

func diskSizeText(d presets.DiskType) string {
	switch {
	case d.FixedSize > 0:
		return strconv.Itoa(d.FixedSize)
	case len(d.Sizes) >= 2:
		return fmt.Sprintf("%d, %d, … %d", d.Sizes[0], d.Sizes[1], d.Sizes[len(d.Sizes)-1])
	case len(d.Sizes) == 1:
		return strconv.Itoa(d.Sizes[0])
	case len(d.Size) >= 2:
		return fmt.Sprintf("%d–%d", d.Size[0], d.Size[1])
	case len(d.Size) == 1:
		return fmt.Sprintf("%d and up", d.Size[0])
	}
	return ""
}

func diskIOPSText(d presets.DiskType) string {
	switch {
	case d.FixedIOPS > 0:
		return strconv.Itoa(d.FixedIOPS)
	case d.PerGB > 0:
		return fmt.Sprintf("%d per GB", d.PerGB)
	case len(d.IOPS) >= 2:
		return fmt.Sprintf("%d–%d", d.IOPS[0], d.IOPS[1])
	}
	return ""
}

func newCapellaClusterDiskTypesCmd() *cobra.Command {
	var provider string
	cmd := capellaLeaf("disk-types", "List the disk types per cloud provider: size range, IOPS and use", cobra.NoArgs,
		func([]string) error {
			if provider != "" && !slices.Contains(cloudProviders, provider) {
				return usageError("use one of "+strings.Join(cloudProviders, ", "), "unknown provider %q", provider)
			}
			return nil
		},
		func(rt *runtime, _ []string) error {
			all := presets.DiskTypes()
			doc := map[string]map[string]diskTypeView{}
			var rows [][]string
			for _, prov := range cloudProviders {
				if provider != "" && provider != prov {
					continue
				}
				names := make([]string, 0, len(all[prov]))
				for n := range all[prov] {
					names = append(names, n)
				}
				sort.Strings(names)
				doc[prov] = map[string]diskTypeView{}
				for _, n := range names {
					d := all[prov][n]
					doc[prov][n] = diskTypeView{For: d.For, Size: d.Size, Sizes: d.Sizes, FixedSize: d.FixedSize, IOPS: d.IOPS,
						IOPSCap: d.IOPSCap, FixedIOPS: d.FixedIOPS, PerGB: d.PerGB, Defaults: d.Defaults}
					rows = append(rows, []string{prov, n, diskSizeText(d), diskIOPSText(d), d.For})
				}
			}
			if rt.JSON {
				rt.Out.Object(doc)
				return nil
			}
			rt.Out.Table([]string{"PROVIDER", "TYPE", "SIZE (GB)", "IOPS", "FOR"}, rows)
			rt.Out.Line("Without --disk-iops, gp3, io2 and Ultra disks get Capella's recommended IOPS for their size; a higher value is allowed.")
			return nil
		})
	cmd.Flags().StringVar(&provider, "provider", "", "Only this cloud provider (aws, gcp, azure)")
	return cmd
}
