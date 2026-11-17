package cli

import (
	"errors"
	"fmt"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/spf13/cobra"
)

// isSystemScope reports whether name is a scope Capella manages.
func isSystemScope(name string) bool { return name == "_default" || name == "_system" }

// scopeDoc is the JSON document of scope add and delete.
type scopeDoc struct {
	Cluster string `json:"cluster"`
	Bucket  string `json:"bucket"`
	Scope   string `json:"scope"`
	Status  string `json:"status"`
}

// bucketTarget is a cluster together with one of its buckets.
type bucketTarget struct {
	*clusterTarget
	Bucket *capellaapi.Bucket
}

// locateBucket resolves the cluster and then the bucket with this name.
func (r *runtime) locateBucket(cluster, bucket, project string) (*bucketTarget, error) {
	t, err := r.locateCluster(cluster, project)
	if err != nil {
		return nil, err
	}
	pid, cid := t.ids()
	if t.freeTier() {
		// Free-tier buckets are only listed by their own endpoint.
		buckets, err := r.listBuckets(t)
		if err != nil {
			return nil, err
		}
		for i := range buckets {
			if buckets[i].Name == bucket {
				return &bucketTarget{clusterTarget: t, Bucket: &buckets[i]}, nil
			}
		}
		return nil, usageError(fmt.Sprintf("check the name with 'cbctl capella bucket list %s'", t.Cluster.Name), "bucket %s not found", bucket)
	}
	b, err := r.resolveBucket(pid, cid, bucket)
	if err != nil {
		return nil, err
	}
	return &bucketTarget{clusterTarget: t, Bucket: b}, nil
}

// bucketID returns the Capella ID of the bucket (the base64url of its name).
func (b *bucketTarget) bucketID() string { return capellaapi.BucketID(b.Bucket.Name) }

func newCapellaScopeListCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list CLUSTER BUCKET",
		Short: "List the scopes of a bucket",
		Args:  cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateBucket(args[0], args[1], project)
		if err != nil {
			return err
		}
		pid, cid := t.ids()
		scopes, err := rt.Client.ListScopes(rt.Ctx, pid, cid, t.bucketID())
		if err != nil {
			return fmt.Errorf("listing scopes of %s: %w", t.Bucket.Name, err)
		}
		if rt.JSON {
			rt.Out.List(scopes)
			return nil
		}
		rows := make([][]string, 0, len(scopes))
		for _, s := range scopes {
			n := 0
			for _, c := range s.Collections {
				if !isSystemCollection(s.Name, c.Name) {
					n++
				}
			}
			rows = append(rows, []string{s.Name, fmt.Sprint(n)})
		}
		rt.Out.Table([]string{"NAME", "COLLECTIONS"}, rows)
		return nil
	}
	return cmd
}

func newCapellaScopeAddCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "add CLUSTER BUCKET NAME",
		Short: "Add a scope to a bucket (no-op when it exists)",
		Args:  cobra.ExactArgs(3),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateBucket(args[0], args[1], project)
		if err != nil {
			return err
		}
		name := args[2]
		pid, cid := t.ids()
		doc := scopeDoc{Cluster: t.Cluster.Name, Bucket: t.Bucket.Name, Scope: name}
		exists := func() (bool, error) {
			scopes, err := rt.Client.ListScopes(rt.Ctx, pid, cid, t.bucketID())
			if err != nil {
				return false, fmt.Errorf("listing scopes of %s: %w", t.Bucket.Name, err)
			}
			for _, s := range scopes {
				if s.Name == name {
					return true, nil
				}
			}
			return false, nil
		}
		if ok, err := exists(); err != nil {
			return err
		} else if ok {
			doc.Status = "already exists"
			rt.report(doc, "scope %s already exists in %s/%s", name, t.Cluster.Name, t.Bucket.Name)
			return nil
		}

		err = rt.Client.CreateScope(rt.Ctx, pid, cid, t.bucketID(), name)
		switch {
		case err == nil:
			doc.Status = "created"
		case errors.Is(err, capellaapi.ErrConflict):
			doc.Status = "already exists"
		case isAmbiguous(err):
			ok, lerr := exists()
			if lerr != nil || !ok {
				return err
			}
			doc.Status = "created"
		default:
			return fmt.Errorf("creating scope %s: %w", name, err)
		}
		if doc.Status == "created" {
			rt.report(doc, "created scope %s in %s/%s", name, t.Cluster.Name, t.Bucket.Name)
		} else {
			rt.report(doc, "scope %s already exists in %s/%s", name, t.Cluster.Name, t.Bucket.Name)
		}
		return nil
	}
	return cmd
}

func newCapellaScopeDeleteCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "delete CLUSTER BUCKET NAME",
		Short: "Delete a scope with its collections and documents",
		Args:  cobra.ExactArgs(3),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name := args[2]
		if isSystemScope(name) {
			return usageError("system scopes are managed by Capella", "scope %s cannot be deleted", name)
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateBucket(args[0], args[1], project)
		if err != nil {
			return err
		}
		if err := t.refuseIfProtected(); err != nil {
			return err
		}
		pid, cid := t.ids()
		key := t.Cluster.Name + "/" + t.Bucket.Name + "/" + name
		if _, _, err := rt.Client.GetScope(rt.Ctx, pid, cid, t.bucketID(), name); err != nil {
			if errors.Is(err, capellaapi.ErrNotFound) {
				rt.report(scopeDoc{Cluster: t.Cluster.Name, Bucket: t.Bucket.Name, Scope: name, Status: "not found"},
					"scope %s not found, nothing to do", key)
				return nil
			}
			return fmt.Errorf("reading scope %s: %w", name, err)
		}
		details, derr := resources.KeyspaceDeleteDetails(rt.Ctx, rt.Client, pid, cid, t.Bucket.Name, t.bucketID(), name, "")
		if derr != nil {
			details = nil // informational only
		}
		if ok, err := rt.confirmNamedDelete("scope", key, name, details); err != nil || !ok {
			return err
		}
		if err := rt.Client.DeleteScope(rt.Ctx, pid, cid, t.bucketID(), name); err != nil {
			return notFoundOr(err, "scope", name,
				fmt.Sprintf("check the name with 'cbctl capella scope list %s %s'", t.Cluster.Name, t.Bucket.Name),
				"deleting scope "+name)
		}
		rt.report(scopeDoc{Cluster: t.Cluster.Name, Bucket: t.Bucket.Name, Scope: name, Status: "deleted"},
			"deleted scope %s from %s/%s", name, t.Cluster.Name, t.Bucket.Name)
		return nil
	}
	return cmd
}
