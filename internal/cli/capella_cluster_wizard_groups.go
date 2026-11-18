package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/presets"
)

// Node limits of service groups (Capella's MDS rules).
const (
	firstGroupMin = 3
	otherGroupMin = 2
	otherGroupMax = 24
)

// diskAnswer is a disk chosen in the disk picker. Zero size or IOPS means "set by the type".
type diskAnswer struct {
	diskType   string
	size, iops int
}

// spec renders the answer as the DISK part of a --group spec.
func (d diskAnswer) spec() string {
	parts := []string{"type=" + d.diskType}
	if d.size > 0 {
		parts = append(parts, "size="+strconv.Itoa(d.size))
	}
	if d.iops > 0 {
		parts = append(parts, "iops="+strconv.Itoa(d.iops))
	}
	return strings.Join(parts, ",")
}

// askGroups asks for service groups one at a time, checking each answer as it is given, and
// returns them as --group specs (capellactl's interactive multi-group create).
func (w *clusterWizard) askGroups(provider string) ([]string, error) {
	type group struct {
		key   string
		nodes int
	}
	var groups []group
	var specs []string
	for {
		total := 0
		for _, g := range groups {
			total += g.nodes
		}
		head := fmt.Sprintf("Service group %d", len(groups)+1)
		if len(groups) > 0 {
			head += fmt.Sprintf("  (nodes so far: %d of %d)", total, maxClusterNodes)
		}
		writeProse(w.out, "%s", head)
		dataTaken := false
		var unused []string
		for _, s := range clusterServices {
			if !slices.ContainsFunc(groups, func(g group) bool { return slices.Contains(strings.Split(g.key, ","), s) }) {
				unused = append(unused, s)
			} else if s == "data" {
				dataTaken = true
			}
		}
		answer, err := w.ask("  Services", strings.Join(unused, ","), func(v string) error {
			names, err := parseServices(v, "services")
			if err != nil {
				return err
			}
			key := strings.Join(names, ",")
			if slices.ContainsFunc(groups, func(g group) bool { return g.key == key }) {
				return fmt.Errorf("a group already runs %s; each group needs a different set of services", key)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		names, _ := parseServices(answer, "services") // checked above
		key := strings.Join(names, ",")
		low, high := otherGroupMin, otherGroupMax
		if slices.Contains(names, "data") && !dataTaken {
			low, high = firstGroupMin, maxClusterNodes
		}
		nodes, err := w.askInt(fmt.Sprintf("  Nodes (%d–%d)", low, high), low, func(n int) error {
			switch {
			case n < low || n > high:
				return fmt.Errorf("this group needs %d–%d nodes", low, high)
			case total+n > maxClusterNodes:
				return fmt.Errorf("a cluster can have at most %d nodes; %d left", maxClusterNodes, maxClusterNodes-total)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		size, err := w.askNodeSize(provider, presets.Size{CPU: 4, RAM: 16})
		if err != nil {
			return nil, err
		}
		disk, err := w.askDisk(provider, nil)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group{key: key, nodes: nodes})
		specs = append(specs, fmt.Sprintf("%s:%dx%d/%d:%s", key, nodes, size.CPU, size.RAM, disk.spec()))
		if total+nodes > maxClusterNodes-otherGroupMin {
			return specs, nil
		}
		more, err := w.yesNo("Add another service group?", false)
		if err != nil {
			return nil, err
		}
		if !more {
			return specs, nil
		}
	}
}

// askNodeSize offers the provider's known node sizes (compute.yaml) and "other", which asks
// for vCPUs and RAM.
func (w *clusterWizard) askNodeSize(provider string, def presets.Size) (presets.Size, error) {
	sizes := presets.ComputeSizes()[provider].Sizes
	writeProse(w.out, "  Node size (vCPU / RAM GB):")
	for row := 0; row < len(sizes); row += 6 {
		var cells []string
		for i := row; i < min(row+6, len(sizes)); i++ {
			cells = append(cells, fmt.Sprintf("%2d) %s", i+1, sizes[i]))
		}
		writeProse(w.out, "%s", "    "+strings.Join(cells, "  "))
	}
	writeProse(w.out, "    %2d) other", len(sizes)+1)
	pick := 1
	if i := slices.Index(sizes, def); i >= 0 {
		pick = i + 1
	}
	n, err := w.askInt("  Choose", pick, between(1, len(sizes)+1))
	if err != nil {
		return presets.Size{}, err
	}
	if n <= len(sizes) {
		return sizes[n-1], nil
	}
	cpu, err := w.askInt("  vCPUs per node", def.CPU, nil)
	if err != nil {
		return presets.Size{}, err
	}
	ram, err := w.askInt("  RAM GB per node", def.RAM, nil)
	if err != nil {
		return presets.Size{}, err
	}
	return presets.Size{CPU: cpu, RAM: ram}, nil
}

func between(lo, hi int) func(int) error {
	return func(n int) error {
		if n < lo || n > hi {
			return fmt.Errorf("choose a number from %d to %d", lo, hi)
		}
		return nil
	}
}

// askDisk lists the provider's disk types with what they mean, then asks only what the chosen
// type lets you set: the size (within its range) and the IOPS (default: Capella's
// recommendation for the size). current (optional) is the disk to start from.
func (w *clusterWizard) askDisk(provider string, current *capellaapi.Disk) (diskAnswer, error) {
	names := presets.DiskTypeNames(provider)
	if current == nil {
		current = makeDisk(provider, "", nil, nil, nil)
	}
	writeProse(w.out, "  Disk type:")
	tw := tabwriter.NewWriter(w.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "    S.NO\tTYPE\tSIZE (GB)\tIOPS\tFOR")
	all := presets.DiskTypes()[provider]
	for i, n := range names {
		d := all[n]
		fmt.Fprintf(tw, "    %d\t%s\t%s\t%s\t%s\n", i+1, n, diskSizeText(d), diskIOPSText(d), d.For)
	}
	if err := tw.Flush(); err != nil {
		return diskAnswer{}, fmt.Errorf("writing the disk types: %w", err)
	}
	def := 1
	if i := slices.Index(names, current.Type); i >= 0 {
		def = i + 1
	}
	n, err := w.askInt("  Choose", def, between(1, len(names)))
	if err != nil {
		return diskAnswer{}, err
	}
	name := names[n-1]
	facts := all[name]
	if facts.FixedSize > 0 {
		writeProse(w.out, "    %s: %d GB, %d IOPS (set by the type)", name, facts.FixedSize, facts.FixedIOPS)
		return diskAnswer{diskType: name}, nil
	}
	same := current.Type == name
	start := smallestDiskSize(provider, name)
	if same && current.Storage != nil {
		start = *current.Storage
	}
	var size int
	switch {
	case len(facts.Sizes) > 0:
		shown := facts.Sizes[:min(5, len(facts.Sizes))]
		label := fmt.Sprintf("  Disk GB per node (%s, … %d)", joinInts(shown), facts.Sizes[len(facts.Sizes)-1])
		size, err = w.askInt(label, start, func(v int) error {
			if !slices.Contains(facts.Sizes, v) {
				return fmt.Errorf("choose one of %s", joinInts(facts.Sizes))
			}
			return nil
		})
	case len(facts.Size) >= 2:
		lo, hi := facts.Size[0], facts.Size[1]
		size, err = w.askInt(fmt.Sprintf("  Disk GB per node (%d–%d)", lo, hi), start, func(v int) error {
			if v < lo || v > hi {
				return fmt.Errorf("give a size from %d to %d GB", lo, hi)
			}
			return nil
		})
	default:
		lo := 50
		if len(facts.Size) == 1 {
			lo = facts.Size[0]
		}
		size, err = w.askInt(fmt.Sprintf("  Disk GB per node (at least %d)", lo), start, func(v int) error {
			if v < lo {
				return fmt.Errorf("give at least %d GB", lo)
			}
			return nil
		})
	}
	if err != nil {
		return diskAnswer{}, err
	}
	if facts.PerGB > 0 {
		per := facts.PerGB / 2
		writeProse(w.out, "    %s %d GB: up to %d read and %d write IOPS", name, size, per*size, per*size)
		return diskAnswer{diskType: name, size: size}, nil
	}
	lo, hi, ok := iopsRange(facts, size)
	rec := facts.DefaultIOPS(size)
	if rec == 0 {
		rec = 3000
		if ok {
			rec = lo
		}
	}
	keep := rec
	if same && current.IOPS != nil && *current.IOPS >= rec {
		keep = *current.IOPS
	}
	label := "  IOPS"
	var check func(int) error
	if ok {
		label = fmt.Sprintf("  IOPS (%d–%d; recommended %d for %d GB)", lo, hi, rec, size)
		check = func(v int) error {
			if v < lo || v > hi {
				return fmt.Errorf("%s disks of %d GB take %d–%d IOPS", name, size, lo, hi)
			}
			return nil
		}
	}
	iops, err := w.askInt(label, keep, check)
	if err != nil {
		return diskAnswer{}, err
	}
	return diskAnswer{diskType: name, size: size, iops: iops}, nil
}

// iopsRange is the IOPS a disk of sizeGB may have: the type's range, capped by its per-size cap.
func iopsRange(d presets.DiskType, sizeGB int) (lo, hi int, ok bool) {
	if len(d.IOPS) < 2 {
		return 0, 0, false
	}
	lo, hi = d.IOPS[0], d.IOPS[1]
	for _, c := range d.IOPSCap {
		if len(c) == 2 && sizeGB >= c[0] {
			hi = min(d.IOPS[1], c[1])
		}
	}
	return lo, hi, true
}

func joinInts(v []int) string {
	out := make([]string, len(v))
	for i, n := range v {
		out[i] = strconv.Itoa(n)
	}
	return strings.Join(out, ", ")
}
