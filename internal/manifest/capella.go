package manifest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/presets"
	"github.com/mminichino/cbctl/internal/yamlio"
)

// Limits of a provisioned cluster.
const (
	firstGroupMinNodes = 3
	otherGroupMinNodes = 2
	firstGroupMaxNodes = 27
	otherGroupMaxNodes = 24
	clusterMaxNodes    = 27
)

var (
	providers      = []string{"aws", "gcp", "azure"}
	availabilities = []string{"multi", "single"}
	supportPlans   = []string{"basic", "developer pro", "enterprise"}
	timezones      = []string{"ET", "GMT", "IST", "PT"}
	defaultRegion  = map[string]string{"aws": "us-east-1", "gcp": "us-east1", "azure": "eastus"}
)

// EffectiveProvider returns spec.provider or the default, aws.
func (s *CapellaSpec) EffectiveProvider() string {
	if s.Provider == "" {
		return "aws"
	}
	return s.Provider
}

// EffectiveRegion returns spec.region or the provider's default region.
func (s *CapellaSpec) EffectiveRegion() string {
	if s.Region != "" {
		return s.Region
	}
	return defaultRegion[s.EffectiveProvider()]
}

// SingleNode reports whether the cluster is a one-node cluster: size dev.
func (s *CapellaSpec) SingleNode() bool {
	p, ok := presets.Presets()[s.Size]
	return ok && p.ConfigurationType == "singleNode"
}

// EffectiveAvailability returns spec.availability, else the preset's, else multi.
func (s *CapellaSpec) EffectiveAvailability() string {
	if s.Availability != "" {
		return s.Availability
	}
	if p, ok := presets.Presets()[s.Size]; ok && p.Availability != "" {
		return p.Availability
	}
	return "multi"
}

// EffectiveGroups returns the service groups: the declared ones, or the single group a size
// preset stands for (its services, node count and compute). Services are canonical.
func (s *CapellaSpec) EffectiveGroups() []ServiceGroup {
	if p, ok := presets.Presets()[s.Size]; ok && len(s.ServiceGroups) == 0 {
		return []ServiceGroup{{Services: ServiceSet(p.Services).Canonical(), Nodes: p.NumOfNodes, Compute: Compute{CPU: p.CPU, RAMGb: p.RAM}}}
	}
	out := make([]ServiceGroup, len(s.ServiceGroups))
	for i, g := range s.ServiceGroups {
		g.Services = g.Services.Canonical()
		out[i] = g
	}
	return out
}

// DataMemoryMB is the RAM, in MB, of all nodes that run the data service.
func (s *CapellaSpec) DataMemoryMB() int {
	total := 0
	for _, g := range s.EffectiveGroups() {
		if g.Services.Has(ServiceData) {
			total += g.Nodes * g.Compute.RAMGb * 1024
		}
	}
	return total
}

// WaitTimeout returns spec.wait.timeout, default 60 minutes. Load rejects a malformed value.
func (s *CapellaSpec) WaitTimeout() time.Duration {
	if s.Wait == nil || s.Wait.Timeout == "" {
		return DefaultWait
	}
	d, err := s.Wait.Timeout.Parse()
	if err != nil {
		return DefaultWait
	}
	return d
}

// WaitEnabled reports whether apply waits for Capella operations; absent means true.
func (s *CapellaSpec) WaitEnabled() bool {
	return s.Wait == nil || s.Wait.Enabled == nil || *s.Wait.Enabled
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if a == v {
			return true
		}
	}
	return false
}

// enumIssue returns "x is not valid ... (did you mean y?)" for a value outside allowed.
func enumIssue(field, v string, allowed []string) string {
	msg := fmt.Sprintf("%s %q is not valid; use one of: %s", field, clip(v), strings.Join(allowed, ", "))
	if g := yamlio.ClosestKey(v, allowed); g != "" {
		msg = fmt.Sprintf("%s %q is not valid (did you mean %s?); use one of: %s", field, clip(v), g, strings.Join(allowed, ", "))
	}
	return msg
}

// validateCapellaCluster checks everything about the cluster itself: project, placement, size
// or service groups, allow list, wait. Buckets and credentials are in validateCapellaData.
func (c *collector) validateCapellaCluster(s *CapellaSpec) {
	if strings.TrimSpace(s.Project) == "" {
		c.add("/spec/project", "project is required")
	}
	if s.Provider != "" && !oneOf(s.Provider, providers) {
		c.add("/spec/provider", enumIssue("provider", s.Provider, providers))
	}
	if s.Region != "" && strings.TrimSpace(s.Region) != s.Region {
		c.add("/spec/region", "region must not start or end with white space")
	}
	if s.CIDR != "" {
		if n, err := NormalizeCIDR(s.CIDR); err != nil {
			c.add("/spec/cidr", err.Error())
		} else if anyIPCIDRs[n] || strings.HasSuffix(n, "/0") {
			c.add("/spec/cidr", "the cluster network cannot be "+n)
		}
	}
	if s.Availability != "" && !oneOf(s.Availability, availabilities) {
		c.add("/spec/availability", enumIssue("availability", s.Availability, availabilities))
	}
	if sp := s.SupportPlan; sp != nil {
		if sp.Plan != "" && !oneOf(sp.Plan, supportPlans) {
			c.add("/spec/supportPlan/plan", enumIssue("plan", sp.Plan, supportPlans))
		}
		if sp.Timezone != "" && !oneOf(sp.Timezone, timezones) {
			c.add("/spec/supportPlan/timezone", enumIssue("timezone", sp.Timezone, timezones))
		}
	}
	c.capellaSize(s)
	c.allowedCIDRs(s)
	if s.Wait != nil && s.Wait.Timeout != "" {
		if _, err := s.Wait.Timeout.Parse(); err != nil {
			c.add("/spec/wait/timeout", err.Error())
		}
	}
}

func (c *collector) capellaSize(s *CapellaSpec) {
	names := presets.PresetNames()
	sort.Strings(names)
	switch {
	case s.Size != "" && len(s.ServiceGroups) > 0:
		c.add("/spec/size", "size and serviceGroups are mutually exclusive", "keep size, or keep serviceGroups")
		return
	case s.Size == "" && len(s.ServiceGroups) == 0:
		c.add("/spec", "set size or serviceGroups", "size: "+strings.Join(names, " | ")+", or list serviceGroups")
		return
	case s.Size != "":
		if _, ok := presets.Presets()[s.Size]; !ok {
			c.add("/spec/size", enumIssue("size", s.Size, names))
			return
		}
		if s.SingleNode() && s.Availability == "multi" {
			c.add("/spec/availability", "size: dev is a single-node cluster; availability must be single (or omitted)")
		}
		return
	}
	c.serviceGroups(s)
}

func (c *collector) serviceGroups(s *CapellaSpec) {
	provider := s.EffectiveProvider()
	if !oneOf(provider, providers) {
		provider = "aws" // reported at /spec/provider; keep checking the rest
	}
	seen := map[string]int{}
	total, dataFirst, dataAt := 0, false, -1
	for i, g := range s.ServiceGroups {
		gp := ptr("/spec/serviceGroups", i)
		c.checkServices(ptr(gp, "services"), g.Services, false)
		canon := g.Services.Canonical()
		switch {
		case len(canon) == 0:
			c.add(ptr(gp, "services"), "services is required", "list at least one service, for example [data]")
		default:
			key := canon.String()
			if first, dup := seen[key]; dup {
				c.addf(ptr(gp, "services"), "serviceGroups[%d] already runs %s; each group needs a different set of services", first, key)
			}
			seen[key] = i
		}
		if canon.Has(ServiceData) && dataAt < 0 {
			dataAt, dataFirst = i, i == 0
		}
		c.groupNodes(gp, i, g.Nodes)
		total += g.Nodes
		c.groupCompute(gp, provider, g.Compute)
		c.groupDisk(gp, provider, g.Disk)
	}
	switch {
	case dataAt < 0:
		c.add("/spec/serviceGroups", "at least one service group must run the data service")
	case !dataFirst:
		c.addf("/spec/serviceGroups", "the group that runs data must come first (it is serviceGroups[%d])", dataAt)
	}
	if total > clusterMaxNodes {
		c.addf("/spec/serviceGroups", "a cluster can have at most %d nodes (declared %d)", clusterMaxNodes, total)
	}
}

func (c *collector) groupNodes(gp string, i, n int) {
	lo, hi := otherGroupMinNodes, otherGroupMaxNodes
	if i == 0 {
		lo, hi = firstGroupMinNodes, firstGroupMaxNodes
	}
	if n < lo || n > hi {
		c.addf(ptr(gp, "nodes"), "the %s group needs %d–%d nodes (got %d)", map[bool]string{true: "first", false: "other"}[i == 0], lo, hi, n)
	}
}

func (c *collector) groupCompute(gp, provider string, cp Compute) {
	if cp.CPU <= 0 || cp.RAMGb <= 0 {
		c.add(ptr(gp, "compute"), "compute needs cpu and ramGb", "see 'cbctl capella cluster options'")
		return
	}
	want := presets.Size{CPU: cp.CPU, RAM: cp.RAMGb}
	offered := presets.ComputeSizes()[provider]
	if offered.Offered(want, false) {
		return
	}
	var list []string
	for _, sz := range offered.Sizes {
		list = append(list, sz.String())
	}
	c.addf(ptr(gp, "compute"), "%s is not a node size %s offers (cpu/ramGb: %s)", want, provider, strings.Join(list, ", "))
}

func (c *collector) groupDisk(gp, provider string, d *Disk) {
	if d == nil {
		return
	}
	dp := ptr(gp, "disk")
	typeName := d.Type
	types := presets.DiskTypeNames(provider)
	if typeName == "" && len(types) > 0 {
		typeName = types[0]
	} else if !oneOf(typeName, types) {
		c.add(ptr(dp, "type"), fmt.Sprintf("disk type %q is not valid for %s (use: %s)", typeName, provider, strings.Join(types, ", ")))
		return
	}
	facts := presets.DiskTypes()[provider][typeName]
	if d.SizeGb != nil {
		c.diskSize(dp, typeName, facts, *d.SizeGb)
	}
	if d.IOPS != nil {
		c.diskIOPS(dp, provider, typeName, facts, d)
	}
}

func (c *collector) diskSize(dp, typeName string, f presets.DiskType, size int) {
	switch {
	case f.FixedSize != 0:
		c.addf(ptr(dp, "sizeGb"), "sizeGb is not allowed for %s disks (the type fixes the size at %d GB)", typeName, f.FixedSize)
	case len(f.Sizes) > 0 && !intIn(f.Sizes, size):
		c.addf(ptr(dp, "sizeGb"), "%s disks come in %v GB (got %d)", typeName, f.Sizes, size)
	case len(f.Size) > 0 && size < f.Size[0]:
		c.addf(ptr(dp, "sizeGb"), "%s disks are at least %d GB (got %d)", typeName, f.Size[0], size)
	case len(f.Size) > 1 && size > f.Size[1]:
		c.addf(ptr(dp, "sizeGb"), "%s disks are at most %d GB (got %d)", typeName, f.Size[1], size)
	}
}

func (c *collector) diskIOPS(dp, provider, typeName string, f presets.DiskType, d *Disk) {
	iops := *d.IOPS
	switch {
	case provider == "gcp" || f.PerGB != 0:
		c.addf(ptr(dp, "iops"), "iops is not allowed for %s disks (IOPS follow the disk size)", typeName)
	case f.FixedIOPS != 0:
		c.addf(ptr(dp, "iops"), "iops is not allowed for %s disks (the type fixes it at %d)", typeName, f.FixedIOPS)
	case len(f.IOPS) == 2:
		lo, hi := f.IOPS[0], f.IOPS[1]
		if d.SizeGb != nil {
			for _, step := range f.IOPSCap {
				if len(step) == 2 && *d.SizeGb >= step[0] && step[1] < hi {
					hi = step[1]
				}
			}
		}
		if iops < lo || iops > hi {
			c.addf(ptr(dp, "iops"), "%s disks take %d–%d IOPS (got %d)", typeName, lo, hi, iops)
		}
	}
}

func intIn(list []int, n int) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

func (c *collector) allowedCIDRs(s *CapellaSpec) {
	seen := map[string]int{}
	for i, a := range s.AllowedCIDRs {
		ap := ptr("/spec/allowedCidrs", i)
		if a.CIDR == "" {
			c.add(ptr(ap, "cidr"), "cidr is required")
			continue
		}
		n, err := NormalizeCIDR(a.CIDR)
		if err != nil {
			c.add(ptr(ap, "cidr"), err.Error())
			continue
		}
		if anyIPCIDRs[n] && !a.AllowAnyIP {
			c.addf(ptr(ap, "cidr"), "%s allows every address; confirm with allowAnyIp: true", n)
		}
		if first, dup := seen[n]; dup {
			c.addf(ptr(ap, "cidr"), "%s is already listed at allowedCidrs[%d]", n, first)
		}
		seen[n] = i
		if a.ExpiresAt != "" {
			if _, ok := a.ExpiryTime(); !ok {
				c.addf(ptr(ap, "expiresAt"), "expiresAt %q is not an RFC 3339 time, for example 2027-01-31T00:00:00Z", clip(a.ExpiresAt))
			}
		}
	}
}
