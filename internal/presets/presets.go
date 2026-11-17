// Package presets embeds the cluster size presets, node compute sizes and disk types that the
// Capella commands offer. The YAML files are the source of truth; change sizes there, not in code.
package presets

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed presets.yaml
var presetsYAML []byte

//go:embed compute.yaml
var computeYAML []byte

//go:embed disks.yaml
var disksYAML []byte

// Preset is a named cluster size from presets.yaml.
type Preset struct {
	Description       string   `yaml:"description"`
	ConfigurationType string   `yaml:"configurationType"` // singleNode | multiNode
	Availability      string   `yaml:"availability"`      // single | multi
	NumOfNodes        int      `yaml:"numOfNodes"`
	CPU               int      `yaml:"cpu"` // vCPUs per node
	RAM               int      `yaml:"ram"` // GB per node
	Services          []string `yaml:"services"`
}

// Size is a node size written "<vCPU>/<RAM GB>" in compute.yaml.
type Size struct {
	CPU int
	RAM int
}

// String returns the "cpu/ram" form.
func (s Size) String() string { return fmt.Sprintf("%d/%d", s.CPU, s.RAM) }

// ParseSize parses "cpu/ram", for example "4/16".
func ParseSize(s string) (Size, error) {
	cpu, ram, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok {
		return Size{}, fmt.Errorf("size %q is not cpu/ram", s)
	}
	c, err := strconv.Atoi(cpu)
	if err != nil {
		return Size{}, fmt.Errorf("size %q: bad cpu: %w", s, err)
	}
	r, err := strconv.Atoi(ram)
	if err != nil {
		return Size{}, fmt.Errorf("size %q: bad ram: %w", s, err)
	}
	return Size{CPU: c, RAM: r}, nil
}

// UnmarshalYAML decodes a "cpu/ram" scalar.
func (s *Size) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	v, err := ParseSize(raw)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// Compute lists the node sizes one cloud provider offers.
type Compute struct {
	// SingleNode sizes are offered only for single-node clusters.
	SingleNode []Size `yaml:"singleNode"`
	// Sizes are offered for every cluster.
	Sizes []Size `yaml:"sizes"`
}

// Offered reports whether size is offered, counting the single-node sizes only when singleNode.
func (c Compute) Offered(size Size, singleNode bool) bool {
	for _, s := range c.Sizes {
		if s == size {
			return true
		}
	}
	if singleNode {
		for _, s := range c.SingleNode {
			if s == size {
				return true
			}
		}
	}
	return false
}

// DiskType describes one disk type of one cloud provider from disks.yaml. Which fields are set
// depends on the type; see the comments in disks.yaml.
type DiskType struct {
	For string `yaml:"for"` // what the type is for
	// Size is [min, max] GB per node; a single element means there is no documented maximum.
	Size []int `yaml:"size"`
	// Sizes, when set, are the only sizes allowed.
	Sizes []int `yaml:"sizes"`
	// FixedSize is set when the type determines the size (no choice).
	FixedSize int `yaml:"fixedSize"`
	// IOPS is [min, max].
	IOPS []int `yaml:"iops"`
	// IOPSCap is the highest IOPS per size as [size GB, IOPS] pairs.
	IOPSCap [][]int `yaml:"iopsCap"`
	// FixedIOPS is set when the type determines the IOPS.
	FixedIOPS int `yaml:"fixedIops"`
	// PerGB is set when IOPS follow the size.
	PerGB int `yaml:"perGb"`
	// Defaults are the recommended IOPS per size as [from GB, IOPS] steps.
	Defaults [][]int `yaml:"defaults"`
}

// DefaultIOPS returns the recommended IOPS for a disk of sizeGB, or 0 when the type has no
// documented defaults.
func (d DiskType) DefaultIOPS(sizeGB int) int {
	iops := 0
	for _, step := range d.Defaults {
		if len(step) == 2 && sizeGB >= step[0] {
			iops = step[1]
		}
	}
	return iops
}

var (
	loadOnce sync.Once

	presetNames []string
	presetMap   map[string]Preset
	computeMap  map[string]Compute
	diskMap     map[string]map[string]DiskType
	diskOrder   map[string][]string
)

// mappingKeys returns the keys of a YAML mapping node in file order.
func mappingKeys(n *yaml.Node) []string {
	var out []string
	if n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	}
	return out
}

// load parses the embedded files once. They are compiled in and covered by tests, so a parse
// failure is a programmer error and panics.
func load() {
	loadOnce.Do(func() {
		var doc yaml.Node
		if err := yaml.Unmarshal(presetsYAML, &doc); err != nil {
			panic(fmt.Sprintf("presets: parse presets.yaml: %v", err))
		}
		if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
			m := doc.Content[0]
			for i := 0; i+1 < len(m.Content); i += 2 {
				presetNames = append(presetNames, m.Content[i].Value)
			}
		}
		mustDecode("presets.yaml", presetsYAML, &presetMap)
		m, err := decodeCompute(computeYAML)
		if err != nil {
			panic(fmt.Sprintf("presets: parse compute.yaml: %v", err))
		}
		computeMap = m
		mustDecode("disks.yaml", disksYAML, &diskMap)
		var disks yaml.Node
		if err := yaml.Unmarshal(disksYAML, &disks); err != nil {
			panic(fmt.Sprintf("presets: parse disks.yaml: %v", err))
		}
		diskOrder = map[string][]string{}
		if len(disks.Content) == 1 {
			m := disks.Content[0]
			for i := 0; i+1 < len(m.Content); i += 2 {
				diskOrder[m.Content[i].Value] = mappingKeys(m.Content[i+1])
			}
		}
	})
}

// decodeCompute parses a compute.yaml document: the node sizes come only from this text.
func decodeCompute(data []byte) (map[string]Compute, error) {
	var out map[string]Compute
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding compute sizes: %w", err)
	}
	return out, nil
}

func mustDecode(name string, data []byte, out any) {
	if err := yaml.Unmarshal(data, out); err != nil {
		panic(fmt.Sprintf("presets: parse %s: %v", name, err))
	}
}

// Presets returns the cluster size presets by name (dev, small, medium, large). The result is a
// copy and may be modified.
func Presets() map[string]Preset {
	load()
	out := make(map[string]Preset, len(presetMap))
	for k, v := range presetMap {
		v.Services = append([]string(nil), v.Services...)
		out[k] = v
	}
	return out
}

// PresetNames returns the preset names in the order presets.yaml lists them.
func PresetNames() []string {
	load()
	return append([]string(nil), presetNames...)
}

// ComputeSizes returns the node sizes each cloud provider (aws, gcp, azure) offers. The result is
// a copy and may be modified.
func ComputeSizes() map[string]Compute {
	load()
	out := make(map[string]Compute, len(computeMap))
	for k, v := range computeMap {
		v.SingleNode = append([]Size(nil), v.SingleNode...)
		v.Sizes = append([]Size(nil), v.Sizes...)
		out[k] = v
	}
	return out
}

// DiskTypeNames returns the disk types of provider in the order disks.yaml lists them (the
// default type first).
func DiskTypeNames(provider string) []string {
	load()
	return append([]string(nil), diskOrder[provider]...)
}

// DiskTypes returns the disk types each cloud provider offers, keyed provider then type name.
// The result is a shallow copy and may be modified; the slices inside a DiskType are shared and
// must be treated as read-only.
func DiskTypes() map[string]map[string]DiskType {
	load()
	out := make(map[string]map[string]DiskType, len(diskMap))
	for provider, types := range diskMap {
		m := make(map[string]DiskType, len(types))
		for name, d := range types {
			m[name] = d
		}
		out[provider] = m
	}
	return out
}
