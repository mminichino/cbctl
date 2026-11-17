package cli

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/mminichino/cbctl/internal/secrets"
	"github.com/spf13/cobra"
)

// cloneableStates are the source states a clone can be taken from.
var cloneableStates = []string{"healthy", "turnedOff"}

type cloneOptions struct {
	project, toProject, region, cidr, plan, version, description string
	withCIDRs, withCredentials, noDataModel                      bool
}

// cloneResult is the JSON document of cluster clone.
type cloneResult struct {
	Source      string   `json:"source"`
	Cluster     string   `json:"cluster"`
	ClusterID   string   `json:"clusterId"`
	Project     string   `json:"project"`
	Region      string   `json:"region"`
	CIDR        string   `json:"cidr"`
	Status      string   `json:"status"` // created | accepted
	Buckets     int      `json:"buckets"`
	Scopes      int      `json:"scopes"`
	Collections int      `json:"collections"`
	AllowedCIDR int      `json:"allowedCidrs"`
	Credentials int      `json:"credentials"`
	NotCopied   []string `json:"notCopied"`
}

const cloneLong = `Deploy a copy of an existing cluster, usually in another region: the same size, services and
availability, plus its buckets, scopes and collections (--no-data-model skips them). Documents, the
on/off schedule, CMEK and private DNS settings are never copied. Allowed CIDRs (--with-cidrs) and
database credentials with new generated passwords (--with-credentials) are opt-in; the passwords are
printed once.

The data model, CIDRs and credentials are copied after the new cluster is healthy, so --no-wait only
creates the cluster.

Examples:
  cbctl capella cluster clone demo-01 demo-weu --region eu-west-1
  cbctl capella cluster clone demo-01 dr-01 --region us-west-2 --to-project dr --support-plan 'developer pro'`

func newCapellaClusterCloneCmd() *cobra.Command {
	var o cloneOptions
	var cmd *cobra.Command
	cmd = capellaLeaf("clone SOURCE NEW_NAME", "Deploy a copy of a cluster (size, services, data model) in another region", cobra.ExactArgs(2),
		func([]string) error {
			o.region = strings.TrimSpace(o.region)
			if o.region == "" {
				return usageError("pass --region REGION", "the region of the clone is required")
			}
			if o.cidr != "" {
				if _, err := netip.ParsePrefix(strings.TrimSpace(o.cidr)); err != nil {
					return usageError("use CIDR notation such as 10.7.0.0/24", "invalid --cidr %q", o.cidr)
				}
			}
			if cmd.Flags().Changed("support-plan") {
				o.plan = strings.ToLower(strings.TrimSpace(o.plan))
				if !slices.Contains(supportPlans, o.plan) {
					return usageError("use one of "+strings.Join(supportPlans, ", "), "unknown support plan %q", o.plan)
				}
			}
			return nil
		},
		func(rt *runtime, args []string) error { return rt.runClusterClone(args[0], args[1], o) })
	cmd.Long = cloneLong
	cmd.Flags().StringVar(&o.region, "region", "", "Region of the clone (required)")
	cmd.Flags().StringVar(&o.project, "project", "", "Project of SOURCE (only needed when the name repeats)")
	cmd.Flags().StringVar(&o.toProject, "to-project", "", "Project of the clone (default: the source's project)")
	cmd.Flags().StringVar(&o.cidr, "cidr", "", "Network CIDR of the clone (default: the first free 10.x.y.0/24)")
	cmd.Flags().StringVar(&o.plan, "support-plan", "", "Support plan: basic, developer pro, enterprise (default: the source's)")
	cmd.Flags().StringVar(&o.version, "server-version", "", "Couchbase Server version (default: the source's; 'latest' lets Capella choose)")
	cmd.Flags().StringVar(&o.description, "description", "", "Description (default: 'Clone of SOURCE (PROJECT, REGION)')")
	cmd.Flags().BoolVar(&o.withCIDRs, "with-cidrs", false, "Also copy the allowed CIDRs")
	cmd.Flags().BoolVar(&o.withCredentials, "with-credentials", false, "Also copy the database credentials (with new generated passwords)")
	cmd.Flags().BoolVar(&o.noDataModel, "no-data-model", false, "Do not copy buckets, scopes and collections")
	return cmd
}

func (rt *runtime) runClusterClone(sourceName, newName string, o cloneOptions) error {
	src, err := rt.locateCluster(sourceName, o.project)
	if err != nil {
		return err
	}
	if src.freeTier() {
		return blockedError("create a provisioned cluster with 'cbctl capella cluster create' instead",
			"free-tier clusters cannot be cloned (one per organization)")
	}
	if !slices.Contains(cloneableStates, src.Cluster.CurrentState) {
		return blockedError(fmt.Sprintf("wait until it is healthy (see 'cbctl capella cluster get %s'), then run the command again", src.Cluster.Name),
			"cluster %s is %s", src.Cluster.Name, src.Cluster.CurrentState)
	}
	// The list entry may be partial; the full cluster carries the groups and settings.
	full, err := rt.readCluster(src.Project.ID, src.Cluster.ID, false)
	if err != nil {
		return err
	}
	srcRegion := clusterRegion(full)

	target := src.Project
	if o.toProject != "" {
		if target, err = rt.resolveProject(o.toProject); err != nil {
			return usageError(fmt.Sprintf("create it first with 'cbctl capella project create %s'", o.toProject), "project %s does not exist", o.toProject)
		}
	}

	targetClusters, err := rt.Client.ListClusters(rt.Ctx, target.ID)
	if err != nil {
		return fmt.Errorf("listing clusters in project %s: %w", target.Name, err)
	}
	if slices.ContainsFunc(targetClusters, func(c capellaapi.Cluster) bool { return c.Name == newName }) {
		return blockedError("choose another name for the copy", "cluster %s already exists in project %s", newName, target.Name)
	}
	used := []usedCIDR{}
	collect := func(clusters []capellaapi.Cluster) {
		for _, c := range clusters {
			if c.CloudProvider != nil && c.CloudProvider.CIDR != "" {
				used = append(used, usedCIDR{cluster: c.Name, cidr: c.CloudProvider.CIDR})
			}
		}
	}
	collect(targetClusters)
	if target.ID != src.Project.ID {
		sourceClusters, err := rt.Client.ListClusters(rt.Ctx, src.Project.ID)
		if err != nil {
			return fmt.Errorf("listing clusters in project %s: %w", src.Project.Name, err)
		}
		collect(sourceClusters)
	}
	cidr := strings.TrimSpace(o.cidr)
	if cidr == "" {
		list := make([]string, len(used))
		for i, u := range used {
			list[i] = u.cidr
		}
		cidr = suggestCIDR(list)
	}
	if clash, ok := overlapOf(cidr, used); ok {
		return usageError("pass --cidr with a free range, or leave it out to get one that does not overlap",
			"%s overlaps the network of cluster %s (%s)", cidr, clash.cluster, clash.cidr)
	}

	if o.region == srcRegion && target.ID == src.Project.ID {
		ok, err := confirm(rt, fmt.Sprintf("%s would be in the same region and project as %s. Clone into the same region?", newName, src.Cluster.Name))
		if err != nil {
			return err
		}
		if !ok {
			rt.report(statusDoc{Status: "cancelled"}, "cancelled; nothing was changed")
			return nil
		}
	}

	desc := o.description
	if desc == "" {
		desc = fmt.Sprintf("Clone of %s (%s, %s)", src.Cluster.Name, src.Project.Name, srcRegion)
	}
	req := buildCloneRequest(full, newName, o.region, cidr, desc, o.plan, o.version)
	rt.Out.Line("Cloning %s (%s · %s %s) → %s (%s · %s %s)", src.Cluster.Name, src.Project.Name, clusterProvider(full), srcRegion,
		newName, target.Name, clusterProvider(full), o.region)

	id, err := rt.Client.CreateCluster(rt.Ctx, target.ID, req)
	if err != nil {
		if o.version != "latest" && req.CouchbaseServer != nil && errors.Is(err, capellaapi.ErrValidation) {
			err = fmt.Errorf("%w\nif Couchbase Server %s is not offered in %s, run the command again with --server-version latest",
				err, req.CouchbaseServer.Version, o.region)
		}
		return fmt.Errorf("creating cluster %s: %w", newName, err)
	}
	logging.Info("create accepted for cluster %s", newName)

	res := cloneResult{Source: src.Cluster.Name, Cluster: newName, ClusterID: id, Project: target.Name, Region: o.region,
		CIDR: cidr, Status: "created", NotCopied: notCopied(full, o)}
	if rt.NoWait {
		res.Status = "accepted"
		if !o.noDataModel || o.withCIDRs || o.withCredentials {
			logging.Warning("--no-wait: only the cluster is created; copy the data model, CIDRs and credentials after it is healthy")
		}
		res.NotCopied = append([]string{"data model", "allowed CIDRs", "database credentials"}, res.NotCopied...)
		rt.report(res, "cluster %s is being created (--no-wait)", newName)
		return nil
	}
	if _, err := rt.waitCluster(target.ID, id, newName, engine.OpCreate, false); err != nil {
		return err
	}

	if !o.noDataModel {
		if err := rt.copyDataModel(src, target.ID, id, &res); err != nil {
			return err
		}
	}
	if o.withCIDRs {
		if err := rt.copyCIDRs(src, target.ID, id, &res); err != nil {
			return err
		}
	}
	var issued []namedSecret
	if o.withCredentials {
		if issued, err = rt.copyCredentials(src, target.ID, id, &res); err != nil {
			return err
		}
	}
	rt.printClone(res, issued)
	return nil
}

// buildCloneRequest is the create body for the clone: the source's size, services, availability
// and support, in the new region and network (port of cluster_clone.build_clone).
func buildCloneRequest(src *capellaapi.Cluster, name, region, cidr, description, plan, version string) capellaapi.CreateClusterRequest {
	provider := clusterProvider(src)
	req := capellaapi.CreateClusterRequest{
		Name:              name,
		Description:       description,
		ConfigurationType: src.ConfigurationType,
		CloudProvider:     capellaapi.CloudProvider{Type: provider, Region: region, CIDR: cidr},
	}
	if version != "latest" {
		v := version
		if v == "" && src.CouchbaseServer != nil {
			v = resources.ReleaseLine(src.CouchbaseServer.Version)
		}
		if v != "" {
			req.CouchbaseServer = &capellaapi.CouchbaseServer{Version: v}
		}
	}
	if src.Availability != nil {
		req.Availability = *src.Availability
	}
	if src.Support != nil {
		req.Support = *src.Support
	}
	if plan != "" {
		req.Support.Plan = plan
	}
	for _, g := range cloneGroups(src.ServiceGroups) {
		sg := capellaapi.ServiceGroupRequest{NumOfNodes: g.NumOfNodes, Services: g.Services}
		if g.Node != nil {
			sg.Node = *g.Node
			if sg.Node.Disk != nil && fixedSizeDisk(provider, sg.Node.Disk.Type) {
				sg.Node.Disk.Storage, sg.Node.Disk.IOPS = nil, nil
			}
		}
		req.ServiceGroups = append(req.ServiceGroups, sg)
	}
	return req
}

// notCopied lists what a clone leaves behind, for the result.
func notCopied(src *capellaapi.Cluster, o cloneOptions) []string {
	out := []string{"documents", "on/off schedule"}
	if src.CMEKID != "" {
		out = append(out, "customer-managed encryption key")
	}
	if src.EnablePrivateDNSResolution != nil && *src.EnablePrivateDNSResolution {
		out = append(out, "private DNS resolution")
	}
	if !o.withCIDRs {
		out = append(out, "allowed CIDRs")
	}
	if !o.withCredentials {
		out = append(out, "database credentials")
	}
	return out
}

// copyDataModel creates the source's buckets with their scopes and collections on the clone.
func (rt *runtime) copyDataModel(src *clusterTarget, projectID, clusterID string, res *cloneResult) error {
	buckets, err := rt.Client.ListBuckets(rt.Ctx, src.Project.ID, src.Cluster.ID)
	if err != nil {
		return fmt.Errorf("listing buckets of %s: %w", src.Cluster.Name, err)
	}
	for _, b := range buckets {
		req := capellaapi.CreateBucketRequest{
			Name: b.Name, Type: orDefault(b.Type, "couchbase"), StorageBackend: b.StorageBackend,
			MemoryAllocationInMb:     b.MemoryAllocationInMb,
			BucketConflictResolution: orDefault(b.BucketConflictResolution, "seqno"),
			DurabilityLevel:          orDefault(b.DurabilityLevel, "none"),
			Replicas:                 b.Replicas, TimeToLiveInSeconds: b.TimeToLiveInSeconds, EvictionPolicy: b.EvictionPolicy,
			Flush: b.Flush != nil && *b.Flush,
		}
		if b.Priority > 0 {
			req.Priority = intPtr(b.Priority)
		}
		if _, err := rt.Client.CreateBucket(rt.Ctx, projectID, clusterID, req); err != nil && !errors.Is(err, capellaapi.ErrConflict) {
			return fmt.Errorf("creating bucket %s on the clone: %w", b.Name, err)
		}
		res.Buckets++
		bid := capellaapi.BucketID(b.Name)
		scopes, err := rt.Client.ListScopes(rt.Ctx, src.Project.ID, src.Cluster.ID, bid)
		if err != nil {
			return fmt.Errorf("listing scopes of bucket %s: %w", b.Name, err)
		}
		for _, s := range scopes {
			if s.Name == "_system" {
				continue
			}
			if s.Name != "_default" {
				if err := rt.Client.CreateScope(rt.Ctx, projectID, clusterID, bid, s.Name); err != nil && !errors.Is(err, capellaapi.ErrConflict) {
					return fmt.Errorf("creating scope %s.%s on the clone: %w", b.Name, s.Name, err)
				}
				res.Scopes++
			}
			cols := s.Collections
			if cols == nil {
				if cols, err = rt.Client.ListCollections(rt.Ctx, src.Project.ID, src.Cluster.ID, bid, s.Name); err != nil {
					return fmt.Errorf("listing collections of %s.%s: %w", b.Name, s.Name, err)
				}
			}
			for _, c := range cols {
				if isSystemCollection(s.Name, c.Name) {
					continue
				}
				var ttl *int
				if c.MaxTTL != 0 {
					ttl = intPtr(c.MaxTTL)
				}
				if err := rt.Client.CreateCollection(rt.Ctx, projectID, clusterID, bid, s.Name, c.Name, ttl); err != nil && !errors.Is(err, capellaapi.ErrConflict) {
					return fmt.Errorf("creating collection %s.%s.%s on the clone: %w", b.Name, s.Name, c.Name, err)
				}
				res.Collections++
			}
		}
	}
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func (rt *runtime) copyCIDRs(src *clusterTarget, projectID, clusterID string, res *cloneResult) error {
	items, err := rt.Client.ListAllowedCIDRs(rt.Ctx, src.Project.ID, src.Cluster.ID)
	if err != nil {
		return fmt.Errorf("listing allowed CIDRs of %s: %w", src.Cluster.Name, err)
	}
	for _, c := range items {
		comment := "(cloned from " + src.Cluster.Name + ")"
		if c.Comment != "" {
			comment = c.Comment + " " + comment
		}
		if _, err := rt.Client.CreateAllowedCIDR(rt.Ctx, projectID, clusterID, c.CIDR, comment, c.ExpiresAt); err != nil && !errors.Is(err, capellaapi.ErrConflict) {
			return fmt.Errorf("creating allowed CIDR %s on the clone: %w", c.CIDR, err)
		}
		res.AllowedCIDR++
	}
	return nil
}

// copyCredentials recreates the source's database credentials with new generated passwords and
// returns the passwords to show once.
func (rt *runtime) copyCredentials(src *clusterTarget, projectID, clusterID string, res *cloneResult) ([]namedSecret, error) {
	items, err := rt.Client.ListCredentials(rt.Ctx, src.Project.ID, src.Cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("listing credentials of %s: %w", src.Cluster.Name, err)
	}
	var issued []namedSecret
	for _, c := range items {
		pw, err := secrets.Generate(generatedPasswordLength)
		if err != nil {
			return issued, err
		}
		if _, _, err := rt.Client.CreateCredential(rt.Ctx, projectID, clusterID,
			capellaapi.CreateCredentialRequest{Name: c.Name, Password: pw, Access: c.Access}); err != nil {
			if errors.Is(err, capellaapi.ErrConflict) {
				continue
			}
			return issued, fmt.Errorf("creating credential %s on the clone: %w", c.Name, err)
		}
		issued = append(issued, namedSecret{Name: c.Name, Password: pw})
		res.Credentials++
	}
	return issued, nil
}

func (rt *runtime) printClone(res cloneResult, issued []namedSecret) {
	if rt.JSON {
		if len(issued) > 0 {
			printSecretsOnce(rt, res, "credentialPasswords", issued)
		} else {
			rt.Out.Object(res)
		}
		return
	}
	rt.Out.Line("cloned %s to %s in %s (%s): %d buckets, %d scopes, %d collections", res.Source, res.Cluster, res.Region, res.CIDR,
		res.Buckets, res.Scopes, res.Collections)
	if res.AllowedCIDR > 0 {
		rt.Out.Line("copied %d allowed CIDRs", res.AllowedCIDR)
	}
	rt.Out.Line("not copied: %s", strings.Join(res.NotCopied, ", "))
	rt.Out.Line("add a schedule later: cbctl capella cluster schedule set %s --timezone TZ --weekdays HH:MM-HH:MM", res.Cluster)
	printSecretsOnce(rt, nil, "credentialPasswords", issued)
}
