package capellaapply

import (
	"strconv"

	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/manifest"
)

// node is one step of a path table: ptr is the manifest pointer of the value at this step and
// kids are the steps below it. A path that runs past the table resolves to the deepest ptr found.
type node struct {
	ptr  string
	kids map[string]*node
}

func leaf(ptr string) *node { return &node{ptr: ptr} }

func branch(ptr string, kids map[string]*node) *node { return &node{ptr: ptr, kids: kids} }

// clusterTable maps the keys of the translated cluster to manifest pointers. Lists (serviceGroups,
// allowedCidrs, buckets, databaseCredentials) are handled by resolve, which knows the indexes.
var clusterTable = map[string]*node{
	"name":               leaf("/metadata/name"),
	"description":        leaf("/spec/description"),
	"tier":               leaf("/spec"),
	"configurationType":  leaf("/spec/size"),
	"deletionProtection": leaf("/spec/deletionProtection"),
	"allowAnyIp":         leaf("/spec/allowedCidrs"),
	"cloudProvider": branch("/spec/provider", map[string]*node{
		"type": leaf("/spec/provider"), "region": leaf("/spec/region"), "cidr": leaf("/spec/cidr"),
	}),
	"couchbaseServer": branch("/spec/serverVersion", map[string]*node{"version": leaf("/spec/serverVersion")}),
	"availability":    branch("/spec/availability", map[string]*node{"type": leaf("/spec/availability")}),
	"support": branch("/spec/supportPlan", map[string]*node{
		"plan": leaf("/spec/supportPlan/plan"), "timezone": leaf("/spec/supportPlan/timezone"),
	}),
}

// groupTable is the table of one service group, relative to /spec/serviceGroups/N.
var groupTable = map[string]*node{
	"services":   leaf("/services"),
	"numOfNodes": leaf("/nodes"),
	"node": branch("/compute", map[string]*node{
		"compute": branch("/compute", map[string]*node{"cpu": leaf("/compute/cpu"), "ram": leaf("/compute/ramGb")}),
		"disk": branch("/disk", map[string]*node{
			"type": leaf("/disk/type"), "storage": leaf("/disk/sizeGb"), "iops": leaf("/disk/iops"),
			"autoExpansion": leaf("/disk/autoExpansion"),
		}),
	}),
}

var cidrTable = map[string]*node{
	"cidr": leaf("/cidr"), "comment": leaf("/comment"), "expiresAt": leaf("/expiresAt"),
}

var bucketTable = map[string]*node{
	"name": leaf("/name"), "type": leaf("/type"), "storageBackend": leaf("/storageBackend"),
	"memoryAllocationInMb": leaf("/memoryMb"), "replicas": leaf("/replicas"), "durabilityLevel": leaf("/durability"),
	"timeToLiveInSeconds": leaf("/ttl"), "flushEnabled": leaf("/flush"),
}

var credentialTable = map[string]*node{"name": leaf("/name"), "password": leaf("/password")}

// walk follows segs through table and returns the deepest pointer it reaches, or fallback.
func walk(table map[string]*node, segs capellafile.Path, fallback string) string {
	out := fallback
	for _, seg := range segs {
		key, ok := seg.(string)
		if !ok {
			break
		}
		n, ok := table[key]
		if !ok {
			break
		}
		out = n.ptr
		table = n.kids
		if table == nil {
			break
		}
	}
	return out
}

// pointer re-points a path of the translated document (projects[0].clusters[0]...) to the
// manifest value it came from, so the user sees /spec/region and the line of region, never
// cloudProvider.region of a file they did not write.
func pointer(spec *manifest.CapellaSpec, p capellafile.Path) string {
	// projects[0].clusters[0] is the fixed prefix; "projects[0].name" is spec.project.
	if len(p) < 2 || p[0] != "projects" {
		return walk(map[string]*node{"organizationId": leaf("/spec/organizationId"), "apiVersion": leaf("/apiVersion")}, p, "/spec")
	}
	rest := p[2:]
	if len(rest) == 0 {
		return "/spec"
	}
	if key, _ := rest[0].(string); key == "name" {
		return "/spec/project"
	} else if key != "clusters" {
		return "/spec"
	}
	if len(rest) < 3 {
		return "/metadata/name"
	}
	rest = rest[2:]
	key, _ := rest[0].(string)
	switch key {
	case "serviceGroups":
		return groupPointer(spec, rest[1:])
	case "allowedCidrs":
		return listPointer("/spec/allowedCidrs", cidrTable, rest[1:])
	case "buckets":
		return bucketPointer(rest[1:])
	case "databaseCredentials":
		return credentialPointer(rest[1:])
	}
	return walk(clusterTable, rest, "/spec")
}

// index returns the list index at the front of segs and the rest.
func index(segs capellafile.Path) (int, capellafile.Path, bool) {
	if len(segs) == 0 {
		return 0, nil, false
	}
	i, ok := segs[0].(int)
	return i, segs[1:], ok
}

func groupPointer(spec *manifest.CapellaSpec, segs capellafile.Path) string {
	if spec.Size != "" { // the group came from the preset, so the value to change is size
		return "/spec/size"
	}
	i, rest, ok := index(segs)
	if !ok {
		return "/spec/serviceGroups"
	}
	return "/spec/serviceGroups/" + strconv.Itoa(i) + walk(groupTable, rest, "")
}

func listPointer(base string, table map[string]*node, segs capellafile.Path) string {
	i, rest, ok := index(segs)
	if !ok {
		return base
	}
	return base + "/" + strconv.Itoa(i) + walk(table, rest, "")
}

func bucketPointer(segs capellafile.Path) string {
	i, rest, ok := index(segs)
	if !ok {
		return "/spec/buckets"
	}
	base := "/spec/buckets/" + strconv.Itoa(i)
	if len(rest) > 0 && rest[0] == "scopes" {
		j, rest, ok := index(rest[1:])
		if !ok {
			return base + "/scopes"
		}
		base += "/scopes/" + strconv.Itoa(j)
		if len(rest) > 0 && rest[0] == "collections" {
			k, rest, ok := index(rest[1:])
			if !ok {
				return base + "/collections"
			}
			base += "/collections/" + strconv.Itoa(k)
			return base + walk(map[string]*node{"name": leaf("/name"), "maxTTL": leaf("/maxTtl")}, rest, "")
		}
		return base + walk(map[string]*node{"name": leaf("/name")}, rest, "")
	}
	return base + walk(bucketTable, rest, "")
}

func credentialPointer(segs capellafile.Path) string {
	i, rest, ok := index(segs)
	if !ok {
		return "/spec/credentials"
	}
	base := "/spec/credentials/" + strconv.Itoa(i)
	if len(rest) > 0 && rest[0] == "access" {
		return base + "/access"
	}
	return base + walk(credentialTable, rest, "")
}

// validationError re-points the issues of the translated document to the manifest. The message
// and fix stay as the capella.yaml loader wrote them; the location is the manifest's.
func (b *builder) validationError(cerr *capellafile.ConfigError) error {
	out := &manifest.ValidationError{Source: b.doc.Source()}
	for _, i := range cerr.Issues {
		ptr := pointer(b.spec, capellafile.ParsePath(i.Path))
		out.Issues = append(out.Issues, b.doc.NewIssue(ptr, i.Message, i.Fix))
	}
	return out
}
