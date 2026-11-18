package manifest

import (
	"context"
	"fmt"
	"strings"
)

const (
	defaultBucketMemoryMB = 1024
	minBucketMemoryMB     = 100
)

var (
	bucketTypes       = []string{"couchbase", "ephemeral"}
	storageBackends   = []string{"couchstore", "magma"}
	durabilities      = []string{"none", "majority", "majorityAndPersistActive", "persistToMajority"}
	ephemeralDurables = []string{"none", "majority"}
	privilegeNames    = []string{"read", "write", "read+write"}
)

// EffectiveType returns the bucket type, default couchbase.
func (b CapellaBucket) EffectiveType() string {
	if b.Type == "" {
		return "couchbase"
	}
	return b.Type
}

// EffectiveMemoryMB returns memoryMb, default 1024.
func (b CapellaBucket) EffectiveMemoryMB() int {
	if b.MemoryMB == nil {
		return defaultBucketMemoryMB
	}
	return *b.MemoryMB
}

// EffectiveReplicas returns replicas; the default is 1, or 0 on a single-node cluster.
func (b CapellaBucket) EffectiveReplicas(singleNode bool) int {
	switch {
	case b.Replicas != nil:
		return *b.Replicas
	case singleNode:
		return 0
	}
	return 1
}

// validateCapellaData checks buckets, credentials and imports against each other.
func (c *collector) validateCapellaData(ctx context.Context, baseDir string, s *CapellaSpec) {
	single := s.SingleNode()
	total := 0
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
		total += b.EffectiveMemoryMB()
		c.capellaBucket(bp, b, single)
		c.capellaScopes(bp, b.Scopes)
		c.importRules(ctx, baseDir, bp, b.Import)
	}
	if mem := s.DataMemoryMB(); mem > 0 && total > mem && len(s.Buckets) > 0 {
		c.addf("/spec/buckets", "the buckets need %d MB but the data nodes have %d MB of RAM in total", total, mem)
	}
	c.credentials(s)
	c.importCredentials(s)
}

func (c *collector) capellaBucket(bp string, b CapellaBucket, single bool) {
	if b.Type != "" && !oneOf(b.Type, bucketTypes) {
		c.add(ptr(bp, "type"), enumIssue("type", b.Type, bucketTypes))
	}
	ephemeral := b.Type == "ephemeral"
	if b.StorageBackend != "" {
		switch {
		case !oneOf(b.StorageBackend, storageBackends):
			c.add(ptr(bp, "storageBackend"), enumIssue("storageBackend", b.StorageBackend, storageBackends))
		case ephemeral:
			c.add(ptr(bp, "storageBackend"), "not allowed for ephemeral buckets (they keep data in memory only)")
		}
	}
	if b.Durability != "" {
		switch {
		case !oneOf(b.Durability, durabilities):
			c.add(ptr(bp, "durability"), enumIssue("durability", b.Durability, durabilities))
		case ephemeral && !oneOf(b.Durability, ephemeralDurables):
			c.addf(ptr(bp, "durability"), "%s needs disk persistence; ephemeral buckets allow none or majority", b.Durability)
		}
	}
	if b.MemoryMB != nil && *b.MemoryMB < minBucketMemoryMB {
		c.addf(ptr(bp, "memoryMb"), "memoryMb must be at least %d (got %d)", minBucketMemoryMB, *b.MemoryMB)
	}
	if b.Replicas != nil {
		r := *b.Replicas
		switch {
		case r < 0 || r > maxBucketReplica:
			c.addf(ptr(bp, "replicas"), "replicas must be 0–%d (got %d)", maxBucketReplica, r)
		case single && r != 0:
			c.add(ptr(bp, "replicas"), "a single-node cluster (size: dev) only supports replicas: 0; there is no second node for a copy")
		case !single && r == 0:
			c.add(ptr(bp, "replicas"), "replicas: 0 is only for single-node clusters; use 1, 2 or 3")
		}
	}
	if b.TTL != nil && *b.TTL < 0 {
		c.add(ptr(bp, "ttl"), "ttl must not be negative")
	}
}

func (c *collector) capellaScopes(bp string, scopes []CapellaScope) {
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
			if msg := keyspaceIssue("collection", col.Name); msg != "" {
				c.add(ptr(cp, "name"), msg)
			} else if first, dup := colNames[col.Name]; dup {
				c.addf(ptr(cp, "name"), "collection %q is already defined by collections[%d]", col.Name, first)
			} else {
				colNames[col.Name] = ci
			}
			if col.MaxTTL != nil && *col.MaxTTL < 0 {
				c.add(ptr(cp, "maxTtl"), "maxTtl must not be negative")
			}
		}
	}
}

func (c *collector) credentials(s *CapellaSpec) {
	names := map[string]int{}
	for i, cr := range s.Credentials {
		cp := ptr("/spec/credentials", i)
		if strings.TrimSpace(cr.Name) == "" {
			c.add(ptr(cp, "name"), "name is required")
		} else if first, dup := names[cr.Name]; dup {
			c.addf(ptr(cp, "name"), "credential %q is already defined by credentials[%d]", cr.Name, first)
		} else {
			names[cr.Name] = i
		}
		if cr.Password != nil {
			if p := cr.Password.Problem(); p != "" {
				c.add(ptr(cp, "password"), p, secretFix)
			}
		}
		c.credentialAccess(cp, cr.Access)
	}
}

func (c *collector) credentialAccess(cp string, a Access) {
	ap := ptr(cp, "access")
	switch {
	case a.Privileges == "":
		c.add(ptr(ap, "privileges"), "privileges is required", "use read, write or read+write")
	case !oneOf(a.Privileges, privilegeNames):
		c.add(ptr(ap, "privileges"), enumIssue("privileges", a.Privileges, privilegeNames))
	}
	switch {
	case a.AllBuckets && a.Bucket != "":
		c.add(ptr(ap, "allBuckets"), "allBuckets and bucket are mutually exclusive")
	case !a.AllBuckets && a.Bucket == "":
		c.add(ap, "set bucket, or allBuckets: true")
	}
	if a.Scope != "" && a.Bucket == "" {
		c.add(ptr(ap, "scope"), "scope needs bucket")
	}
	if len(a.Collections) > 0 && a.Scope == "" {
		c.add(ptr(ap, "collections"), "collections needs scope")
	}
}

// grantsWrite reports whether the credential may write to bucket.scope.collection.
func (a Access) grantsWrite(bucket, scope, collection string) bool {
	if a.Privileges != "write" && a.Privileges != "read+write" {
		return false
	}
	if a.AllBuckets {
		return true
	}
	switch {
	case a.Bucket != bucket:
		return false
	case a.Scope != "" && a.Scope != scope:
		return false
	case len(a.Collections) > 0 && !oneOf(collection, a.Collections):
		return false
	}
	return true
}

// importCredentials checks that every import names a credential that can write to the target
// and whose password is a reference (a generated password is unknown to apply).
func (c *collector) importCredentials(s *CapellaSpec) {
	byName := map[string]Credential{}
	for _, cr := range s.Credentials {
		byName[cr.Name] = cr
	}
	for bi, b := range s.Buckets {
		for ii, imp := range b.Import {
			p := ptr("/spec/buckets", bi, "import", ii, "credential")
			if imp.Credential == "" {
				c.add(p, "credential is required for an import", "name an entry of spec.credentials")
				continue
			}
			cr, ok := byName[imp.Credential]
			if !ok {
				c.addf(p, "credential %q is not defined in spec.credentials", imp.Credential)
				continue
			}
			scope, col := imp.ImportTarget()
			switch {
			case !cr.Access.grantsWrite(b.Name, scope, col):
				c.add(p, fmt.Sprintf("credential %q does not grant write access to %s.%s.%s", cr.Name, b.Name, scope, col),
					"use privileges write or read+write on that bucket, scope and collection")
			case cr.Password == nil:
				c.addf(p, "credential %q has no password reference; an import needs a password apply can read", cr.Name)
			}
		}
	}
}
