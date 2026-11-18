package capellaapply

import (
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/resources"
)

// precheck compares the fields Capella cannot change in place with the existing cluster (C7).
// A difference is a manifest problem, not a blocked plan: it returns a *manifest.ValidationError
// whose issues point at the manifest lines, and nothing is planned. A field the manifest leaves
// out keeps the cluster's value, so it never differs. A cluster that does not exist yet has
// nothing to differ from.
func precheck(doc *manifest.Document, r *remote) error {
	cur := r.Cluster
	if cur == nil {
		return nil
	}
	s := doc.Capella
	name := doc.Envelope.Metadata.Name
	var issues []manifest.Issue
	add := func(ptr, field, have, want, fix string) {
		issues = append(issues, doc.NewIssue(ptr,
			fmt.Sprintf("%s cannot change on the existing cluster %s: Capella has %s, the manifest says %s", field, name, have, want), fix))
	}
	newCluster := "create a new cluster under another metadata.name, or set the field to what the cluster has"

	if cp := cur.CloudProvider; cp != nil {
		if s.Provider != "" && cp.Type != "" && s.Provider != cp.Type {
			add("/spec/provider", "provider", cp.Type, s.Provider, newCluster)
		}
		if s.Region != "" && cp.Region != "" && s.Region != cp.Region {
			add("/spec/region", "region", cp.Region, s.Region, newCluster)
		}
		if s.CIDR != "" && cp.CIDR != "" {
			want, err := manifest.NormalizeCIDR(s.CIDR)
			if err != nil {
				want = s.CIDR
			}
			have, err := manifest.NormalizeCIDR(cp.CIDR)
			if err != nil {
				have = cp.CIDR
			}
			if want != have {
				add("/spec/cidr", "cidr", have, want, newCluster)
			}
		}
	}
	if cur.Availability != nil && cur.Availability.Type != "" && (s.Availability != "" || s.Size != "") {
		if want := s.EffectiveAvailability(); want != cur.Availability.Type {
			ptr := "/spec/availability"
			if s.Availability == "" {
				ptr = "/spec/size"
			}
			add(ptr, "availability", cur.Availability.Type, want, newCluster)
		}
	}
	if cur.CouchbaseServer != nil && cur.CouchbaseServer.Version != "" && s.ServerVersion != "" {
		have, want := resources.ReleaseLine(cur.CouchbaseServer.Version), resources.ReleaseLine(s.ServerVersion)
		if have != want {
			add("/spec/serverVersion", "serverVersion", have, want,
				"a server upgrade is not part of apply; set serverVersion to "+have+" or remove it")
		}
	}
	issues = append(issues, bucketBackends(doc, r)...)
	if len(issues) == 0 {
		return nil
	}
	return &manifest.ValidationError{Source: doc.Source(), Issues: issues}
}

// bucketBackends flags a bucket whose storage backend differs from the existing bucket's.
func bucketBackends(doc *manifest.Document, r *remote) []manifest.Issue {
	have := map[string]string{}
	for _, b := range r.Buckets {
		have[b.Name] = b.StorageBackend
	}
	var issues []manifest.Issue
	for i, b := range doc.Capella.Buckets {
		cur, ok := have[b.Name]
		if !ok || cur == "" || b.StorageBackend == "" || strings.EqualFold(cur, b.StorageBackend) {
			continue
		}
		issues = append(issues, doc.NewIssue(fmt.Sprintf("/spec/buckets/%d/storageBackend", i),
			fmt.Sprintf("storageBackend cannot change on the existing bucket %s: Capella has %s, the manifest says %s", b.Name, cur, b.StorageBackend),
			"create a bucket with another name, or set storageBackend to "+cur))
	}
	return issues
}
