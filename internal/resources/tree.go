// Package resources holds one plan.Reconciler per Capella resource kind and the tree that wires
// them together. It is a port of capellactl's resources package (Apache-2.0, Couchbase, Inc.).
//
// Reconcilers compare generic mappings (the file's desired state against what Capella reports)
// and never import the cli package.
package resources

import "github.com/mminichino/cbctl/internal/plan"

// Options tune the reconcilers that need command-line switches the plan.RunContext does not carry.
type Options struct {
	// ForceOverwriteSecret lets a generated credential replace an existing secret file
	// (apply --force-overwrite-secret).
	ForceOverwriteSecret bool
	// NoRenameHint drops the "did you mean to rename" warning on new clusters. Direct cluster
	// create declares only the new cluster, so every other cluster would look undeclared.
	NoRenameHint bool
}

// rootRec is the synthetic root of the tree. BuildPlan walks its children with the loaded
// configuration as their parent desired value; the root itself is never planned.
type rootRec struct{ plan.Base }

// BuildTree returns root, project, cluster and, below each cluster, deletion-protection,
// schedule, backup-schedule, allowed-cidr, bucket (with bucket-backup-schedule and scope, then
// collection) and credential.
func BuildTree() plan.Reconciler { return BuildTreeWith(Options{}) }

// BuildTreeWith is BuildTree with options.
func BuildTreeWith(opts Options) plan.Reconciler {
	collection := newCollection()
	scope := newScope(collection)
	bucket := newBucket(newBucketBackupSchedule(), scope)
	cluster := newCluster(opts,
		newDeletionProtection(),
		newSchedule(),
		newBackupSchedule(),
		newAllowedCIDR(),
		bucket,
		newCredential(opts),
	)
	project := newProject(cluster)
	root := &rootRec{Base: plan.NewBase(plan.RootKind, plan.Flags{}, project)}
	root.Bind(root)
	return root
}
