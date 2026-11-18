// Package capellaapply applies a CouchbaseCapella manifest document. It translates the document
// in memory into the configuration the Capella plan/apply engine reads from capella.yaml, checks
// the fields Capella cannot change, runs the engine without any file side effect, and then loads
// the declared imports. Nothing here reads or writes a capella.yaml.
package capellaapply

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/layout"
	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/yamlio"
)

// secretVarPrefix names the in-memory variables that carry the resolved credential passwords into
// the translated document. The document only ever holds ${CBCTL_MANIFEST_PW_n} references, so a
// password never appears in the rendered text.
const secretVarPrefix = "CBCTL_MANIFEST_PW_"

// privilegeSets maps the manifest's read, write and read+write to Capella privilege names.
var privilegeSets = map[string][]string{
	"read":       {"data_reader"},
	"write":      {"data_writer"},
	"read+write": {"data_reader", "data_writer"},
}

// Observed is what Capella already holds for the manifest's cluster. The zero value means the
// cluster does not exist yet. Translation reads it so that a field the manifest leaves out keeps
// the value Capella has, instead of being reset to a default (C18, C21).
type Observed struct {
	// Cluster is the existing cluster, nil when it does not exist.
	Cluster *capellaapi.Cluster
	// Buckets are the buckets of the existing cluster.
	Buckets []capellaapi.Bucket
}

// TranslateOptions are the inputs of Translate besides the document.
type TranslateOptions struct {
	// BaseDir is the directory a relative credentials directory resolves from (the manifest's).
	BaseDir string
	// CIDR is the cluster network, already resolved: spec.cidr, the existing cluster's, or a free
	// /24 chosen online.
	CIDR string
	// Observed is the cluster as Capella holds it now.
	Observed Observed
}

// Translation is a manifest document rendered as a capella.yaml-style configuration.
type Translation struct {
	// Config is the validated configuration the engine plans from.
	Config *capellafile.Config
	// YAML is the rendered document. It holds ${...} references, never a password.
	YAML string
	// Warnings lists manifest settings the translation drops because Capella cannot take them.
	Warnings []string
}

// Translate renders doc as an in-memory capella.yaml (the projects: form with one project and one
// cluster) and validates it with capellafile.LoadText, so every existing schema, default and
// cross-reference rule applies. The document's resolved credential passwords reach the engine
// through an injected environment. Problems come back as *manifest.ValidationError whose issues
// point at the manifest's own lines (/spec/region, not projects[0].clusters[0].cloudProvider).
func Translate(doc *manifest.Document, opt TranslateOptions) (*Translation, error) {
	if doc == nil || doc.Capella == nil {
		return nil, errors.New("capellaapply: the document is not a CouchbaseCapella")
	}
	b := &builder{doc: doc, spec: doc.Capella, opt: opt, env: map[string]string{}}
	root := b.root()
	text, err := yamlio.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("rendering the Capella configuration: %w", err)
	}
	env := func(name string) string { return b.env[name] }
	cfg, err := capellafile.LoadText(string(text), doc.Source(), opt.BaseDir, env)
	if err != nil {
		var cerr *capellafile.ConfigError
		if errors.As(err, &cerr) {
			return nil, b.validationError(cerr)
		}
		return nil, fmt.Errorf("validating the Capella configuration: %w", err)
	}
	return &Translation{Config: cfg, YAML: string(text), Warnings: b.warnings}, nil
}

// builder holds the state of one translation.
type builder struct {
	doc  *manifest.Document
	spec *manifest.CapellaSpec
	opt  TranslateOptions
	// env maps the injected password variables to their resolved values.
	env map[string]string
	// warnings collects the settings that were dropped.
	warnings []string
}

func (b *builder) root() capellafile.Obj {
	root := capellafile.Obj{"apiVersion": capellafile.APIVersion}
	if b.spec.OrganizationID != "" {
		root["organizationId"] = b.spec.OrganizationID
	}
	root["projects"] = []any{capellafile.Obj{
		"name":     b.spec.Project,
		"clusters": []any{b.cluster()},
	}}
	return root
}

func (b *builder) cluster() capellafile.Obj {
	s := b.spec
	provider := b.provider()
	cl := capellafile.Obj{"name": b.doc.Envelope.Metadata.Name}
	if s.Description != "" {
		cl["description"] = s.Description
	}
	if s.SingleNode() {
		cl["configurationType"] = "singleNode"
	}
	cp := capellafile.Obj{"type": provider, "region": b.region()}
	if b.opt.CIDR != "" {
		cp["cidr"] = b.opt.CIDR
	}
	cl["cloudProvider"] = cp
	if s.ServerVersion != "" {
		cl["couchbaseServer"] = capellafile.Obj{"version": s.ServerVersion}
	}
	cl["availability"] = capellafile.Obj{"type": b.availability()}
	cl["support"] = b.support()
	cl["serviceGroups"] = b.serviceGroups(provider)
	if s.DeletionProtection != nil {
		cl["deletionProtection"] = *s.DeletionProtection
	}
	if cidrs, anyIP := b.allowedCIDRs(); len(cidrs) > 0 {
		cl["allowedCidrs"] = cidrs
		if anyIP {
			cl["allowAnyIp"] = true
		}
	}
	if buckets := b.buckets(); len(buckets) > 0 {
		cl["buckets"] = buckets
	}
	if creds := b.credentials(); len(creds) > 0 {
		cl["databaseCredentials"] = creds
	}
	return cl
}

// provider is spec.provider. A manifest that leaves it out keeps the existing cluster's provider
// (C18): the default applies to a cluster that is created.
func (b *builder) provider() string {
	if cur := b.opt.Observed.Cluster; cur != nil && b.spec.Provider == "" && cur.CloudProvider != nil && cur.CloudProvider.Type != "" {
		return cur.CloudProvider.Type
	}
	return b.spec.EffectiveProvider()
}

// region is spec.region, or the existing cluster's region when the manifest leaves it out.
func (b *builder) region() string {
	if cur := b.opt.Observed.Cluster; cur != nil && b.spec.Region == "" && cur.CloudProvider != nil && cur.CloudProvider.Region != "" {
		return cur.CloudProvider.Region
	}
	return b.spec.EffectiveRegion()
}

// availability is spec.availability or the size preset's; without either the existing cluster's
// topology stays.
func (b *builder) availability() string {
	cur := b.opt.Observed.Cluster
	if cur != nil && b.spec.Availability == "" && b.spec.Size == "" && cur.Availability != nil && cur.Availability.Type != "" {
		return cur.Availability.Type
	}
	return b.spec.EffectiveAvailability()
}

// support is spec.supportPlan, with the existing cluster's value for what the manifest leaves
// out and the basic plan for a new cluster.
func (b *builder) support() capellafile.Obj {
	plan, tz := "", ""
	if sp := b.spec.SupportPlan; sp != nil {
		plan, tz = sp.Plan, sp.Timezone
	}
	if cur := b.opt.Observed.Cluster; cur != nil && cur.Support != nil {
		if plan == "" {
			plan = cur.Support.Plan
		}
		if tz == "" {
			tz = cur.Support.Timezone
		}
	}
	if plan == "" {
		plan = "basic"
	}
	out := capellafile.Obj{"plan": plan}
	if tz != "" {
		out["timezone"] = tz
	}
	return out
}

// serviceGroups renders the declared groups (or the one a size preset stands for). A group the
// cluster already has keeps its disk settings where the manifest is silent; a new group gets the
// provider's defaults.
func (b *builder) serviceGroups(provider string) []any {
	var existing map[string]capellaapi.ServiceGroup
	if cur := b.opt.Observed.Cluster; cur != nil {
		existing = map[string]capellaapi.ServiceGroup{}
		for _, g := range cur.ServiceGroups {
			existing[groupKey(g.Services)] = g
		}
	}
	var out []any
	for _, g := range b.spec.EffectiveGroups() {
		obs, have := existing[groupKey(g.Services)]
		out = append(out, capellafile.Obj{
			"services":   stringList(g.Services),
			"numOfNodes": g.Nodes,
			"node": capellafile.Obj{
				"compute": capellafile.Obj{"cpu": g.Compute.CPU, "ram": g.Compute.RAMGb},
				"disk":    b.disk(provider, g.Disk, obs, have),
			},
		})
	}
	return out
}

// disk is one group's disk. have says the cluster runs the group already, with disk obs.
func (b *builder) disk(provider string, d *manifest.Disk, obs capellaapi.ServiceGroup, have bool) capellafile.Obj {
	var size, iops *int
	var autoExp *bool
	diskType := ""
	if d != nil {
		diskType, size, iops, autoExp = d.Type, d.SizeGb, d.IOPS, d.AutoExpansion
	}
	// The engine (capellafile) accepts auto-expansion for Azure disks only, so for AWS and GCP the
	// setting is dropped, with a warning when the manifest asked for it (C21 deviation).
	if provider != "azure" {
		if autoExp != nil {
			b.warnings = append(b.warnings, "disk.autoExpansion is ignored: Capella accepts it for Azure disks only")
		}
		autoExp = nil
	}
	if have && obs.Node != nil && obs.Node.Disk != nil {
		cur := obs.Node.Disk
		if diskType == "" {
			diskType = cur.Type
		}
		if size == nil && !layout.FixedSizeDisk(provider, diskType) && cur.Storage != nil && cur.Type == diskType {
			size = cur.Storage
		}
		if iops == nil && cur.IOPS != nil && cur.Type == diskType {
			iops = cur.IOPS
		}
		// Auto-expansion is a create-time setting (C21): an existing group keeps Capella's value.
		if provider == "azure" {
			autoExp = cur.AutoExpansion
		}
	}
	made := layout.MakeDisk(provider, diskType, size, iops, autoExp)
	out := capellafile.Obj{"type": made.Type}
	if made.Storage != nil {
		out["storage"] = *made.Storage
	}
	if made.IOPS != nil {
		out["iops"] = *made.IOPS
	}
	if made.AutoExpansion != nil {
		out["autoExpansion"] = *made.AutoExpansion
	}
	return out
}

// groupKey identifies a service group by its sorted service set, as the engine does.
func groupKey(services []string) string {
	s := append([]string(nil), services...)
	sort.Strings(s)
	return strings.Join(s, ",")
}

func stringList(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func (b *builder) allowedCIDRs() (entries []any, anyIP bool) {
	for _, a := range b.spec.AllowedCIDRs {
		cidr := a.CIDR
		if n, err := manifest.NormalizeCIDR(a.CIDR); err == nil {
			cidr = n
		}
		e := capellafile.Obj{"cidr": cidr}
		if a.Comment != "" {
			e["comment"] = a.Comment
		}
		if a.ExpiresAt != "" {
			e["expiresAt"] = a.ExpiresAt
		}
		anyIP = anyIP || a.AllowAnyIP
		entries = append(entries, e)
	}
	return entries, anyIP
}

// buckets renders the declared buckets. A bucket Capella has keeps the settings the manifest
// leaves out; a new one gets the documented defaults.
func (b *builder) buckets() []any {
	observed := map[string]capellaapi.Bucket{}
	for _, o := range b.opt.Observed.Buckets {
		observed[o.Name] = o
	}
	single := b.spec.SingleNode()
	var out []any
	for _, bk := range b.spec.Buckets {
		obs, have := observed[bk.Name]
		bo := capellafile.Obj{"name": bk.Name}
		typ := bk.Type
		if typ == "" && have && obs.Type != "" {
			typ = obs.Type
		}
		if typ == "" {
			typ = bk.EffectiveType()
		}
		bo["type"] = typ
		if bk.StorageBackend != "" {
			bo["storageBackend"] = bk.StorageBackend
		}
		if have && obs.BucketConflictResolution != "" {
			bo["bucketConflictResolution"] = obs.BucketConflictResolution
		}
		switch {
		case bk.MemoryMB != nil:
			bo["memoryAllocationInMb"] = *bk.MemoryMB
		case have && obs.MemoryAllocationInMb > 0:
			bo["memoryAllocationInMb"] = obs.MemoryAllocationInMb
		default:
			bo["memoryAllocationInMb"] = bk.EffectiveMemoryMB()
		}
		switch {
		case bk.Replicas != nil:
			bo["replicas"] = *bk.Replicas
		case have:
			bo["replicas"] = obs.Replicas
		default:
			bo["replicas"] = bk.EffectiveReplicas(single)
		}
		switch {
		case bk.Durability != "":
			bo["durabilityLevel"] = bk.Durability
		case have && obs.DurabilityLevel != "":
			bo["durabilityLevel"] = obs.DurabilityLevel
		}
		switch {
		case bk.TTL != nil:
			bo["timeToLiveInSeconds"] = *bk.TTL
		case have:
			bo["timeToLiveInSeconds"] = obs.TimeToLiveInSeconds
		}
		switch {
		case bk.Flush != nil:
			bo["flushEnabled"] = *bk.Flush
		case have && obs.FlushEnabled != nil:
			bo["flushEnabled"] = *obs.FlushEnabled
		case have && obs.Flush != nil:
			bo["flushEnabled"] = *obs.Flush
		}
		if scopes := scopesOf(bk.Scopes); len(scopes) > 0 {
			bo["scopes"] = scopes
		}
		out = append(out, bo)
	}
	return out
}

func scopesOf(in []manifest.CapellaScope) []any {
	var out []any
	for _, s := range in {
		so := capellafile.Obj{"name": s.Name}
		var colls []any
		for _, c := range s.Collections {
			co := capellafile.Obj{"name": c.Name}
			if c.MaxTTL != nil {
				co["maxTTL"] = *c.MaxTTL
			}
			colls = append(colls, co)
		}
		if len(colls) > 0 {
			so["collections"] = colls
		}
		out = append(out, so)
	}
	return out
}

// credentials renders the declared database credentials. The privileges map as read →
// data_reader, write → data_writer; allBuckets leaves the resources out.
func (b *builder) credentials() []any {
	var out []any
	for i, c := range b.spec.Credentials {
		co := capellafile.Obj{"name": c.Name}
		if c.Password != nil {
			name := fmt.Sprintf("%s%d", secretVarPrefix, i)
			b.env[name] = c.Password.Value()
			co["password"] = "${" + name + "}"
		}
		entry := capellafile.Obj{"privileges": stringList(privilegeSets[c.Access.Privileges])}
		if c.Access.Bucket != "" && !c.Access.AllBuckets {
			bucket := capellafile.Obj{"name": c.Access.Bucket}
			if c.Access.Scope != "" {
				scope := capellafile.Obj{"name": c.Access.Scope}
				if len(c.Access.Collections) > 0 {
					scope["collections"] = stringList(c.Access.Collections)
				}
				bucket["scopes"] = []any{scope}
			}
			entry["resources"] = capellafile.Obj{"buckets": []any{bucket}}
		}
		co["access"] = []any{entry}
		out = append(out, co)
	}
	return out
}
