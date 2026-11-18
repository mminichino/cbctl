package capellaapply

import (
	"context"
	"fmt"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/layout"
	"github.com/mminichino/cbctl/internal/manifest"
)

// remote is what Capella holds for the manifest's project and cluster, read before planning.
type remote struct {
	// Project is the project spec.project names, nil when it does not exist yet.
	Project *capellaapi.Project
	// Cluster is the full cluster record, nil when the cluster does not exist yet.
	Cluster *capellaapi.Cluster
	// Buckets are the cluster's buckets, empty until the cluster is healthy.
	Buckets []capellaapi.Bucket
	// UsedCIDRs are the networks of every cluster in the organization: Capella rejects a cluster
	// whose network is not unique across the organization, not just across the project.
	UsedCIDRs []string
}

// observe reads the project, the cluster and its buckets. Nothing is written. A cluster that is
// not healthy yet (still deploying) has no readable buckets, so they are left out.
func observe(ctx context.Context, c *capellaapi.Client, project, cluster string) (*remote, error) {
	out := &remote{}
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	for i := range projects {
		clusters, err := c.ListClusters(ctx, projects[i].ID)
		if err != nil {
			return nil, fmt.Errorf("listing clusters in project %s: %w", projects[i].Name, err)
		}
		for _, cl := range clusters {
			if cl.CloudProvider != nil && cl.CloudProvider.CIDR != "" {
				out.UsedCIDRs = append(out.UsedCIDRs, cl.CloudProvider.CIDR)
			}
			if projects[i].Name == project && cl.Name == cluster && out.Cluster == nil {
				cl := cl
				out.Cluster = &cl
			}
		}
		if projects[i].Name == project && out.Project == nil {
			out.Project = &projects[i]
		}
	}
	if out.Project == nil {
		return out, nil
	}
	if out.Cluster == nil {
		return out, nil
	}
	full, err := c.GetCluster(ctx, out.Project.ID, out.Cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("reading cluster %s: %w", cluster, err)
	}
	out.Cluster = full
	if full.CurrentState == "healthy" {
		out.Buckets, err = c.ListBuckets(ctx, out.Project.ID, full.ID)
		if err != nil {
			return nil, fmt.Errorf("listing buckets of cluster %s: %w", cluster, err)
		}
	}
	return out, nil
}

// observed is the translator's view of the remote state.
func (r *remote) observed() Observed { return Observed{Cluster: r.Cluster, Buckets: r.Buckets} }

// resolveCIDR is the network of the cluster: spec.cidr, else the existing cluster's, else the
// first free 10.x.y.0/24 not used by any cluster of the organization.
func resolveCIDR(doc *manifest.Document, r *remote) string {
	if doc.Capella.CIDR != "" {
		return doc.Capella.CIDR
	}
	if r.Cluster != nil && r.Cluster.CloudProvider != nil && r.Cluster.CloudProvider.CIDR != "" {
		return r.Cluster.CloudProvider.CIDR
	}
	return layout.SuggestCIDR(r.UsedCIDRs)
}
