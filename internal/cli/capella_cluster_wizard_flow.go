package cli

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/presets"
	"github.com/spf13/cobra"
)

// run asks for every field cmd's flags did not set, in capellactl's order: the cluster type,
// provider, size (custom: the service layout and each group), region, network and support
// plan. name is the NAME argument (asked when empty); cidr is the suggested network.
func (w *clusterWizard) run(cmd *cobra.Command, name, cidr string) (ClusterSpec, error) {
	ch := cmd.Flags().Changed
	spec := ClusterSpec{Name: name}
	var err error
	fmt.Fprintln(w.out, "New Capella cluster (press Enter to accept a default; Ctrl-D cancels)")
	if spec.Name == "" {
		spec.Name, err = w.ask("Cluster name", "", func(v string) error {
			if !clusterNamePattern.MatchString(v) {
				return fmt.Errorf("give a name of letters, digits, spaces, '.', '_' or '-'")
			}
			return nil
		})
		if err != nil {
			return spec, err
		}
	}
	sized := sizedByFlags(cmd)
	free := flagBool(cmd, "free-tier")
	if !free && !sized && !w.noFree {
		fmt.Fprintf(w.out, "Cluster types: free (%s) or provisioned (you choose size and support plan)\n", freeTierNote)
		kind, err := w.choose("Cluster type", "provisioned", []string{"free", "provisioned"})
		if err != nil {
			return spec, err
		}
		free = kind == "free"
		spec.Free = free
	}
	spec.Provider = flagString(cmd, "provider")
	if !ch("provider") {
		if spec.Provider, err = w.choose("Cloud provider", "aws", cloudProviders); err != nil {
			return spec, err
		}
	}
	if !free && !sized {
		if err := w.askSize(cmd, &spec); err != nil {
			return spec, err
		}
	}
	if !ch("region") {
		def, label := regionDefaults[spec.Provider], "Region"
		if free {
			def = freeTierRegions[spec.Provider][0]
			label = "Region (free-tier regions) (" + strings.Join(freeTierRegions[spec.Provider], ", ") + ")"
		}
		if spec.Region, err = w.ask(label, def, nonEmpty("a region is required")); err != nil {
			return spec, err
		}
	}
	if free {
		return spec, nil
	}
	if !ch("cidr") {
		if spec.CIDR, err = w.ask("Network CIDR", cidr, func(v string) error {
			if _, err := netip.ParsePrefix(v); err != nil {
				return fmt.Errorf("give a CIDR such as 10.0.0.0/24")
			}
			return nil
		}); err != nil {
			return spec, err
		}
	}
	if !ch("support-plan") {
		if spec.Support, err = w.choose("Support plan", "developer pro", supportPlans); err != nil {
			return spec, err
		}
	}
	return spec, nil
}

// askSize asks the size preset; custom asks the service layout, then the one group's nodes,
// vCPUs, RAM and disk, or each of several service groups.
func (w *clusterWizard) askSize(cmd *cobra.Command, spec *ClusterSpec) error {
	size, err := w.choose("Size", "small", append(presets.PresetNames(), "custom"))
	if err != nil {
		return err
	}
	if size != "custom" {
		spec.Size = size
		return nil
	}
	layout, err := w.choose("Service layout", "one group", []string{"one group", "multiple groups"})
	if err != nil {
		return err
	}
	if layout == "multiple groups" {
		spec.Groups, err = w.askGroups(spec.Provider)
		return err
	}
	spec.Size = "custom"
	nodes, err := w.askInt("Nodes", 3, nil)
	if err != nil {
		return err
	}
	cpu, err := w.askInt("vCPUs per node", 4, nil)
	if err != nil {
		return err
	}
	ram, err := w.askInt("RAM GB per node", 16, nil)
	if err != nil {
		return err
	}
	spec.Nodes, spec.CPU, spec.RAM = strconv.Itoa(nodes), strconv.Itoa(cpu), strconv.Itoa(ram)
	d, err := w.askDisk(spec.Provider, nil)
	if err != nil {
		return err
	}
	spec.Disk = &d
	return nil
}

// apply writes the answers into cmd's flags, so the builder reads them like typed flags.
func (s ClusterSpec) apply(cmd *cobra.Command) error {
	set := func(name, v string) error {
		if v == "" {
			return nil
		}
		return cmd.Flags().Set(name, v)
	}
	pairs := [][2]string{{"name", s.Name}, {"provider", s.Provider}, {"region", s.Region}, {"size", s.Size},
		{"cidr", s.CIDR}, {"support-plan", s.Support}, {"nodes", s.Nodes}, {"cpu", s.CPU}, {"ram", s.RAM}}
	if s.Free {
		pairs = append(pairs, [2]string{"free-tier", "true"})
	}
	if s.Disk != nil {
		pairs = append(pairs, [2]string{"disk-type", s.Disk.diskType})
		if s.Disk.size > 0 {
			pairs = append(pairs, [2]string{"disk-size", strconv.Itoa(s.Disk.size)})
		}
		if s.Disk.iops > 0 {
			pairs = append(pairs, [2]string{"disk-iops", strconv.Itoa(s.Disk.iops)})
		}
	}
	for _, kv := range pairs {
		if err := set(kv[0], kv[1]); err != nil {
			return err
		}
	}
	for _, g := range s.Groups {
		if err := cmd.Flags().Set("group", g); err != nil {
			return err
		}
	}
	return nil
}
