package capellafile

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"

	"go.yaml.in/yaml/v3"
)

// Comment blocks that "sync" writes into an otherwise empty document. The editor strips them
// when it opens a file, so that adding the first cluster or project does not leave a stale hint
// behind. The capellactl wording is recognized too.
const (
	NewClusterTemplate = "# This project has no clusters yet. To create one, run: cbctl capella cluster create NAME\n"
	NoProjectsTemplate = "# No projects left in Capella. Create one with: cbctl capella project create NAME\n" +
		"# or add an existing one with: cbctl capella sync --project NAME\n"
)

// FlowKeys are the keys whose small leaf mappings and lists of scalars are written in flow
// style ({cpu: 4, ram: 16}, [data, index]); so is any list of scalars under "collections".
var FlowKeys = map[string]bool{
	"compute":         true,
	"disk":            true,
	"services":        true,
	"privileges":      true,
	"cloudProvider":   true,
	"availability":    true,
	"support":         true,
	"couchbaseServer": true,
}

// Key orders of the mappings cbctl writes. A key that an order does not list is written after
// the listed ones, alphabetically.
var (
	// RootKeyOrder is the order of the top-level keys.
	RootKeyOrder = []string{"apiVersion", "organizationId", "credentialsOutput", "skipProjects", "projects", "project", "clusters"}
	// ProjectKeyOrder is the order of the keys of a project.
	ProjectKeyOrder = []string{"id", "name", "description", "clusters"}
	// ClusterKeyOrder is the order of the keys of a cluster; new keys are inserted in this order.
	ClusterKeyOrder = []string{"id", "name", "description", "tier", "configurationType", "cloudProvider", "couchbaseServer",
		"availability", "support", "serviceGroups", "zones", "cmekId", "enablePrivateDNSResolution", "deletionProtection",
		"allowAnyIp", "hibernation", "backupSchedule", "allowedCidrs", "buckets", "databaseCredentials"}
	// BucketKeyOrder is the order of the keys of a bucket; a backup schedule goes before the scopes.
	BucketKeyOrder = []string{"name", "type", "storageBackend", "vbuckets", "memoryAllocationInMb", "bucketConflictResolution",
		"durabilityLevel", "replicas", "flushEnabled", "timeToLiveInSeconds", "evictionPolicy", "priority",
		"enableCrossClusterVersioning", "backupSchedule", "scopes"}
)

// contextOrders gives the key order of the mapping found under (or, for lists, inside) a key.
var contextOrders = map[string][]string{
	"":                    RootKeyOrder,
	"projects":            ProjectKeyOrder,
	"project":             ProjectKeyOrder,
	"clusters":            ClusterKeyOrder,
	"buckets":             BucketKeyOrder,
	"cloudProvider":       {"type", "region", "cidr"},
	"couchbaseServer":     {"version"},
	"availability":        {"type"},
	"support":             {"plan", "timezone"},
	"serviceGroups":       {"node", "numOfNodes", "services"},
	"node":                {"compute", "disk"},
	"compute":             {"cpu", "ram"},
	"disk":                {"type", "storage", "iops", "autoExpansion"},
	"allowedCidrs":        {"cidr", "comment", "expiresAt"},
	"hibernation":         append(append([]string{"timezone"}, DayKeys...), "paused", "ensure"),
	"backupSchedule":      {"interval", "retention", "startTime", "copyToRegions", "ensure"},
	"scopes":              {"name", "collections"},
	"collections":         {"name", "maxTTL"},
	"databaseCredentials": {"name", "password", "access"},
	"access":              {"privileges", "resources"},
	"resources":           {"buckets"},
}

// orderedKeys returns the keys of m: those the context's order lists first, then the rest
// alphabetically.
func orderedKeys(m Obj, ctx string) []string {
	order := contextOrders[ctx]
	rank := make(map[string]int, len(order))
	for i, k := range order {
		rank[k] = i
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		ra, oka := rank[keys[a]]
		rb, okb := rank[keys[b]]
		switch {
		case oka && okb:
			return ra < rb
		case oka != okb:
			return oka
		}
		return keys[a] < keys[b]
	})
	return keys
}

// ToNode converts plain data into a YAML node. Mappings are written in the canonical key order
// for the context key, and small leaf values (see FlowKeys) in flow style. A *yaml.Node is
// returned as is.
func ToNode(v any, key string) *yaml.Node {
	switch t := v.(type) {
	case *yaml.Node:
		return t
	case Obj:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		scalarsOnly := true
		for _, k := range orderedKeys(t, key) {
			child := ToNode(t[k], k)
			if child.Kind == yaml.MappingNode || child.Kind == yaml.SequenceNode {
				scalarsOnly = false
			}
			n.Content = append(n.Content, scalarNode(k), child)
		}
		if FlowKeys[key] && scalarsOnly && len(t) > 0 {
			n.Style = yaml.FlowStyle
		}
		return n
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			break
		}
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		scalarsOnly := true
		for i := 0; i < rv.Len(); i++ {
			child := ToNode(rv.Index(i).Interface(), key)
			if child.Kind == yaml.MappingNode || child.Kind == yaml.SequenceNode {
				scalarsOnly = false
			}
			n.Content = append(n.Content, child)
		}
		if scalarsOnly && rv.Len() > 0 && (FlowKeys[key] || key == "collections") {
			n.Style = yaml.FlowStyle
		}
		return n
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			m := make(Obj, rv.Len())
			for _, k := range rv.MapKeys() {
				m[k.String()] = rv.MapIndex(k).Interface()
			}
			return ToNode(m, key)
		}
	}
	n := &yaml.Node{}
	if err := n.Encode(NormalizeNumbers(v)); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
	return n
}

func scalarNode(s string) *yaml.Node {
	n := &yaml.Node{}
	if err := n.Encode(s); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	}
	return n
}

// EncodeNode writes n with the canonical encoder: two-space indentation, block style except for
// the flow-styled nodes, comments and key order as in the node tree.
func EncodeNode(n *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return "", fmt.Errorf("encoding YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encoding YAML: %w", err)
	}
	return buf.String(), nil
}

// Dump is the canonical YAML writer used by sync, init and the editor. v is plain data (Obj and
// lists, written in the canonical key order) or a *yaml.Node, which keeps its own order and
// comments.
func Dump(v any) (string, error) {
	return EncodeNode(ToNode(v, ""))
}
