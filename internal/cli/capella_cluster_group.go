package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/spf13/cobra"
)

func newCapellaClusterGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Service groups (multi-dimensional scaling): list, add, remove",
		Long: "Service groups let a cluster run services on separately sized nodes. Change a group's node count with\n" +
			"'cluster scale --group' and its compute or disk with 'cluster update --group'.",
		Run: func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newGroupListCmd(), newGroupAddCmd(), newGroupRemoveCmd())
	return cmd
}

func newGroupListCmd() *cobra.Command {
	var project string
	cmd := capellaLeaf("list CLUSTER", "List a cluster's service groups", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			cl, err := rt.readCluster(t.Project.ID, t.Cluster.ID, t.freeTier())
			if err != nil {
				return err
			}
			groups := groupDetails(cl.ServiceGroups)
			rt.Out.List(groups)
			rows := make([][]string, len(groups))
			for i, g := range groups {
				rows[i] = []string{g.Services, strconv.Itoa(g.Nodes), strconv.Itoa(g.CPU), strconv.Itoa(g.RAM), g.Disk}
			}
			rt.Out.Table([]string{"SERVICES", "NODES", "vCPU", "RAM GB", "DISK"}, rows)
			rt.Out.Line("total nodes: %d of %d", totalNodes(cl.ServiceGroups), maxClusterNodes)
			return nil
		})
	addProjectFlag(cmd, &project)
	cmd.Long = "List a cluster's service groups: services, nodes, compute and disk, and the cluster's total nodes.\n\n" +
		"Examples:\n  cbctl capella cluster group list prod-01\n  cbctl capella -o json cluster group list prod-01"
	return cmd
}

// groupChangeResult is the JSON document of group add and remove.
type groupChangeResult struct {
	Cluster string `json:"cluster"`
	Group   string `json:"group"`
	Nodes   int    `json:"nodes,omitempty"`
	Status  string `json:"status"` // added | removed | unchanged | accepted | cancelled
}

func newGroupAddCmd() *cobra.Command {
	var project, services, diskType string
	var nodes, cpu, ram, diskSize, diskIOPS int
	var autoExpansion bool
	var cmd *cobra.Command
	cmd = capellaLeaf("add CLUSTER", "Add a service group (scale out)", cobra.ExactArgs(1),
		func([]string) error {
			if services == "" {
				return usageError("pass --services, for example --services search", "the services of the new group are required")
			}
			if _, err := parseServices(services, "--services"); err != nil {
				return err
			}
			if cmd.Flags().Changed("nodes") && (nodes < 2 || nodes > 24) {
				return usageError("choose 2-24 nodes", "--nodes %d is outside the limits of an extra group (2-24)", nodes)
			}
			return nil
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			if t.freeTier() {
				return blockedError("create a provisioned cluster for that ('cbctl capella cluster create')", "%s", freeTierBlocked)
			}
			names, _ := parseServices(services, "--services")
			key := strings.Join(names, ",")
			want := nodes
			if !cmd.Flags().Changed("nodes") {
				want = 2
			}
			opts := updateOptions{cpu: cpu, ram: ram, diskType: diskType, diskSize: diskSize, diskIOPS: diskIOPS, autoExpansion: autoExpansion}
			res := groupChangeResult{Cluster: t.Cluster.Name, Group: key, Nodes: want}
			_, wrote, err := rt.putCluster(t.Project.ID, t.Cluster.ID, func(cl *capellaapi.Cluster) (*capellaapi.UpdateClusterRequest, error) {
				if cl.ConfigurationType == "singleNode" {
					return nil, usageError("create a multiNode cluster to add groups", "singleNode clusters have exactly one service group")
				}
				req := baseUpdateRequest(cl)
				if i := slices.IndexFunc(req.ServiceGroups, func(g capellaapi.ServiceGroup) bool { return groupKey(g) == key }); i >= 0 {
					g := req.ServiceGroups[i]
					if cmd.Flags().Changed("nodes") && g.NumOfNodes != nodes {
						return nil, usageError(fmt.Sprintf("change its node count with 'cbctl capella cluster scale %s --group %s --nodes N'", cl.Name, key),
							"group %s already exists with %d nodes", key, g.NumOfNodes)
					}
					return nil, nil
				}
				if total := totalNodes(req.ServiceGroups) + want; total > maxClusterNodes {
					return nil, usageError("choose fewer nodes", "adding %d nodes would make %d; a cluster has at most %d", want, total, maxClusterNodes)
				}
				idx := slices.IndexFunc(req.ServiceGroups, func(g capellaapi.ServiceGroup) bool { return hasService(g, "data") })
				var g capellaapi.ServiceGroup
				if idx >= 0 {
					g = cloneGroups(req.ServiceGroups[idx : idx+1])[0]
				}
				g.Services, g.NumOfNodes = names, want
				if err := applyGroupChanges(rt, cl, &g, opts); err != nil {
					return nil, err
				}
				req.ServiceGroups = append(req.ServiceGroups, g)
				return &req, nil
			})
			if err != nil {
				return err
			}
			if !wrote {
				res.Status = "unchanged"
				rt.report(res, "group %s already exists on %s; nothing to add", key, t.Cluster.Name)
				return nil
			}
			noWait, err := rt.settle(t.Project.ID, t.Cluster.ID, t.Cluster.Name, "add group", engine.OpUpdate, false)
			if err != nil {
				return err
			}
			res.Status = "added"
			if noWait {
				res.Status = "accepted"
			}
			rt.report(res, "group %s %s on %s (%d nodes)", key, res.Status, t.Cluster.Name, want)
			return nil
		})
	cmd.Long = "Add a service group. Options not given are copied from the group that runs data.\n\n" +
		"Examples:\n  cbctl capella cluster group add prod-01 --services search --nodes 2\n" +
		"  cbctl capella cluster group add prod-01 --services index,query --nodes 2 --cpu 16 --ram 64"
	addProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&services, "services", "", "Comma-separated services of the new group (required)")
	cmd.Flags().IntVar(&nodes, "nodes", 2, "Nodes in the group (2-24)")
	cmd.Flags().IntVar(&cpu, "cpu", 0, "vCPUs per node (default: as the data group)")
	cmd.Flags().IntVar(&ram, "ram", 0, "RAM GB per node (default: as the data group)")
	cmd.Flags().StringVar(&diskType, "disk-type", "", "Disk type (default: as the data group; see 'cbctl capella cluster disk-types')")
	cmd.Flags().IntVar(&diskSize, "disk-size", 0, "Disk GB per node (default: as the data group)")
	cmd.Flags().IntVar(&diskIOPS, "disk-iops", 0, "Disk IOPS (AWS; default: as the data group)")
	cmd.Flags().BoolVar(&autoExpansion, "disk-auto-expansion", false, "Azure: let disk storage expand automatically")
	return cmd
}

func newGroupRemoveCmd() *cobra.Command {
	var project string
	var allowServiceRemoval, skipCapacity bool
	cmd := capellaLeaf("remove CLUSTER SERVICES", "Remove a service group (scale in, with the capacity checks)", cobra.ExactArgs(2),
		func(args []string) error {
			_, err := parseServices(args[1], "SERVICES")
			return err
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			if t.freeTier() {
				return blockedError("none needed: a free-tier cluster has no groups to remove", "%s", freeTierBlocked)
			}
			names, _ := parseServices(args[1], "SERVICES")
			key := strings.Join(names, ",")
			res := groupChangeResult{Cluster: t.Cluster.Name, Group: key}
			confirmed := false
			_, wrote, err := rt.putCluster(t.Project.ID, t.Cluster.ID, func(cl *capellaapi.Cluster) (*capellaapi.UpdateClusterRequest, error) {
				req := baseUpdateRequest(cl)
				i := slices.IndexFunc(req.ServiceGroups, func(g capellaapi.ServiceGroup) bool { return groupKey(g) == key })
				if i < 0 {
					return nil, nil
				}
				target := req.ServiceGroups[i]
				res.Nodes = target.NumOfNodes
				others := slices.Delete(slices.Clone(req.ServiceGroups), i, i+1)
				if hasService(target, "data") && dataNodes(others) == 0 {
					return nil, blockedError("every cluster needs a group that runs data; add the new data group first ('cbctl capella cluster group add'), then remove this one",
						"the last group running data cannot be removed")
				}
				if !allowServiceRemoval {
					for _, s := range target.Services {
						if !slices.ContainsFunc(others, func(g capellaapi.ServiceGroup) bool { return hasService(g, s) }) {
							return nil, blockedError("pass --allow-service-removal to remove the last nodes that run "+s,
								"removing group %s leaves no node running %s", key, s)
						}
					}
				}
				if err := rt.checkCapacity(t.Project.ID, cl.ID, cl.ServiceGroups, others, skipCapacity); err != nil {
					return nil, err
				}
				if !confirmed {
					ok, err := confirm(rt, fmt.Sprintf("Remove service group %s (%d nodes) from %s?", key, target.NumOfNodes, cl.Name))
					if err != nil {
						return nil, err
					}
					if !ok {
						res.Status = "cancelled"
						return nil, nil
					}
					confirmed = true
				}
				req.ServiceGroups = others
				return &req, nil
			})
			if err != nil {
				return err
			}
			switch {
			case res.Status == "cancelled":
				rt.report(res, "cancelled; the cluster is unchanged")
				return nil
			case !wrote:
				res.Status = "unchanged"
				rt.report(res, "no service group '%s' on %s; nothing to remove", key, t.Cluster.Name)
				return nil
			}
			noWait, err := rt.settle(t.Project.ID, t.Cluster.ID, t.Cluster.Name, "remove group", engine.OpUpdate, false)
			if err != nil {
				return err
			}
			res.Status = "removed"
			if noWait {
				res.Status = "accepted"
			}
			rt.report(res, "group %s %s from %s", key, res.Status, t.Cluster.Name)
			return nil
		})
	cmd.Long = "Remove a service group (a guarded scale in).\n\nExamples:\n  cbctl capella cluster group remove prod-01 search\n" +
		"  cbctl capella cluster group remove prod-01 analytics --allow-service-removal --yes"
	addProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&allowServiceRemoval, "allow-service-removal", false, "Allow removing the last nodes that run a service")
	cmd.Flags().BoolVar(&skipCapacity, "skip-capacity-check", false, "Remove even when cluster stats cannot be read (at your own risk)")
	return cmd
}
