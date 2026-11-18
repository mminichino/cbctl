package manifest

import (
	"context"
	"strings"
)

const (
	defaultQuotaMiB  = 128
	minQuotaMiB      = 100
	maxBucketReplica = 3
	// DefaultServerGroup is the server group of a node that names none. Defaulting leaves
	// ServerGroup empty so apply does not move a node that is already in another group.
	DefaultServerGroup = "Group 1"
	// DefaultAdminUser is the admin user when spec.credentials.username is absent.
	DefaultAdminUser = "Administrator"
)

// validateServer checks a CouchbaseServer spec offline. It does not change the spec.
func (c *collector) validateServer(ctx context.Context, baseDir string, s *ServerSpec) {
	c.serverCredentials(s)
	if s.TLS != nil && s.TLS.CAFile != "" && !s.TLS.Enabled {
		c.add("/spec/tls/caFile", "caFile is only used with tls.enabled: true")
	}
	c.checkServices("/spec/nodeDefaults/services", s.NodeDefaults.Services, true)
	if s.NodeDefaults.RAMGiB != nil && *s.NodeDefaults.RAMGiB <= 0 {
		c.add("/spec/nodeDefaults/ramGiB", "ramGiB must be greater than 0")
	}
	if s.NodeDefaults.Services != nil && len(s.NodeDefaults.Services) == 0 {
		c.add("/spec/nodeDefaults/services", "services must not be empty", "omit it for the default: data, index, query, search")
	}
	c.serverNodes(s)
	c.serverBuckets(ctx, baseDir, s)
}

func (c *collector) serverCredentials(s *ServerSpec) {
	if u := s.Credentials.Username; u != "" && strings.TrimSpace(u) != u {
		c.add("/spec/credentials/username", "username must not start or end with white space")
	}
	pw := s.Credentials.Password
	switch p := pw.Problem(); {
	case pw.Raw == "" && p == "":
		c.add("/spec/credentials/password", "password is required", secretFix)
	case p != "":
		c.add("/spec/credentials/password", p, secretFix)
	}
}

const secretFix = "keep the password out of the file: use ${VAR}, ${file:PATH} or ${base64:VALUE}"

// EffectiveServices returns the services a node runs after defaults: none for an arbiter, the
// node's own set, else the node defaults, else DefaultNodeServices. Names are canonical.
func (s *ServerSpec) EffectiveServices(n ServerNode) ServiceSet {
	switch {
	case n.Arbiter:
		return nil
	case n.Services != nil:
		return n.Services.Canonical()
	case s.NodeDefaults.Services != nil:
		return s.NodeDefaults.Services.Canonical()
	}
	return DefaultNodeServices
}

// PrimaryIndex returns the index of the primary node: the one marked primary, else the first.
// It returns -1 when there are no nodes or several are marked.
func (s *ServerSpec) PrimaryIndex() int {
	found := -1
	for i, n := range s.Nodes {
		if n.Primary {
			if found >= 0 {
				return -1
			}
			found = i
		}
	}
	if found < 0 && len(s.Nodes) > 0 {
		return 0
	}
	return found
}

func (c *collector) serverNodes(s *ServerSpec) {
	if len(s.Nodes) == 0 {
		c.add("/spec/nodes", "at least one node is required", "add a node with its ipAddress")
		return
	}
	seen := map[string]int{}
	var primaries []int
	hasData := false
	for i, n := range s.Nodes {
		np := ptr("/spec/nodes", i)
		switch n.IPAddress {
		case "":
			c.add(ptr(np, "ipAddress"), "ipAddress is required")
		default:
			if msg := hostIssue(n.IPAddress); msg != "" {
				c.add(ptr(np, "ipAddress"), msg)
			} else if first, dup := seen[hostKey(n.IPAddress)]; dup {
				c.addf(ptr(np, "ipAddress"), "%s is already used by nodes[%d]; ipAddress values must be unique", n.IPAddress, first)
			} else {
				seen[hostKey(n.IPAddress)] = i
			}
		}
		if n.ExternalIPAddress != "" {
			if msg := hostIssue(n.ExternalIPAddress); msg != "" {
				c.add(ptr(np, "externalIpAddress"), msg)
			}
		}
		if n.RAMGiB != nil && *n.RAMGiB <= 0 {
			c.add(ptr(np, "ramGiB"), "ramGiB must be greater than 0")
		}
		if n.Arbiter {
			if n.Services != nil {
				c.add(ptr(np, "services"), "arbiter cannot be combined with services", "remove services, or use arbiter: false")
			}
			if n.Primary {
				c.add(ptr(np, "primary"), "the primary node cannot be an arbiter", "mark a node that runs services with primary: true")
			}
		} else {
			c.checkServices(ptr(np, "services"), n.Services, true)
			if n.Services != nil && len(n.Services) == 0 {
				c.add(ptr(np, "services"), "services must not be empty", "omit it to use nodeDefaults.services")
			}
			if s.EffectiveServices(n).Has(ServiceData) {
				hasData = true
			}
		}
		if n.Primary {
			primaries = append(primaries, i)
		}
	}
	if len(primaries) > 1 {
		c.addf(ptr("/spec/nodes", primaries[1], "primary"), "only one node can be primary (nodes[%d] is also primary)", primaries[0])
	}
	if len(primaries) == 0 && s.Nodes[0].Arbiter {
		c.add("/spec/nodes/0", "no node is marked primary, so the first node would be the primary, but it is an arbiter",
			"mark a node that runs services with primary: true")
	}
	if !hasData {
		c.add("/spec/nodes", "at least one node must run the data service")
	}
}

func (c *collector) serverBuckets(ctx context.Context, baseDir string, s *ServerSpec) {
	names := map[string]int{}
	for i, b := range s.Buckets {
		bp := ptr("/spec/buckets", i)
		if msg := bucketNameIssue(b.Name); msg != "" {
			c.add(ptr(bp, "name"), msg)
		} else if first, dup := names[b.Name]; dup {
			c.addf(ptr(bp, "name"), "bucket %q is already defined by buckets[%d]", b.Name, first)
		} else {
			names[b.Name] = i
		}
		if b.QuotaMiB != nil && *b.QuotaMiB < minQuotaMiB {
			c.addf(ptr(bp, "quotaMiB"), "quotaMiB must be at least %d (got %d)", minQuotaMiB, *b.QuotaMiB)
		}
		if b.Replicas != nil && (*b.Replicas < 0 || *b.Replicas > maxBucketReplica) {
			c.addf(ptr(bp, "replicas"), "replicas must be 0–%d (got %d)", maxBucketReplica, *b.Replicas)
		}
		c.serverScopes(bp, b.Scopes)
		c.importRules(ctx, baseDir, bp, b.Import)
		for k, imp := range b.Import {
			if imp.Credential != "" {
				c.add(ptr(bp, "import", k, "credential"), "credential applies to CouchbaseCapella only; imports use the admin credentials")
			}
		}
	}
}

func (c *collector) serverScopes(bp string, scopes []ServerScope) {
	scopeNames := map[string]int{}
	for si, sc := range scopes {
		sp := ptr(bp, "scopes", si)
		if msg := keyspaceIssue("scope", sc.Name); msg != "" {
			c.add(ptr(sp, "name"), msg)
		} else if first, dup := scopeNames[sc.Name]; dup {
			c.addf(ptr(sp, "name"), "scope %q is already defined by scopes[%d]", sc.Name, first)
		} else {
			scopeNames[sc.Name] = si
		}
		colNames := map[string]int{}
		for ci, col := range sc.Collections {
			cp := ptr(sp, "collections", ci)
			if msg := keyspaceIssue("collection", col); msg != "" {
				c.add(cp, msg)
			} else if first, dup := colNames[col]; dup {
				c.addf(cp, "collection %q is already listed at collections[%d]", col, first)
			} else {
				colNames[col] = ci
			}
		}
	}
}

// applyServerDefaults fills the documented defaults in place, after validation: the admin user,
// the services, data path and RAM of every node, the primary, bucket quota and replicas,
// Service sets become canonical.
func applyServerDefaults(s *ServerSpec) {
	if s.Credentials.Username == "" {
		s.Credentials.Username = DefaultAdminUser
	}
	s.NodeDefaults.Services = s.NodeDefaults.Services.Canonical()
	p := s.PrimaryIndex()
	for i := range s.Nodes {
		n := &s.Nodes[i]
		n.Services = s.EffectiveServices(*n)
		if n.DataPath == "" {
			n.DataPath = s.NodeDefaults.DataPath
		}
		if n.RAMGiB == nil && s.NodeDefaults.RAMGiB != nil {
			v := *s.NodeDefaults.RAMGiB
			n.RAMGiB = &v
		}
		n.Primary = i == p
	}
	for i := range s.Buckets {
		b := &s.Buckets[i]
		if b.QuotaMiB == nil {
			v := defaultQuotaMiB
			b.QuotaMiB = &v
		}
		if b.Replicas == nil {
			v := 0
			b.Replicas = &v
		}
	}
}

// DoRebalance reports whether nodes are rebalanced in; absent means true.
func (s *ServerSpec) DoRebalance() bool { return s.Rebalance == nil || *s.Rebalance }
