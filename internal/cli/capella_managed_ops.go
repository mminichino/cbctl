package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/mminichino/cbctl/internal/presets"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// managedBuild turns the flags and arguments of a resource command into the file mutation it
// makes. It runs after capella.yaml is opened, so it can read the file through ed.
type managedBuild func(cmd *cobra.Command, args []string, e *envRuntime, ed *capellafile.Editor) (Mutation, error)

// runManaged opens capella.yaml, builds the mutation and runs ChangeAndApply.
func runManaged(cmd *cobra.Command, args []string, build managedBuild) error {
	e, ed, err := newManagedEnv(cmd)
	if err != nil {
		return err
	}
	m, err := build(cmd, args, e, ed)
	if err != nil {
		return err
	}
	_, err = ChangeAndApply(e.Ctx, e, ed, m, managedFlagsOf(cmd).noApply)
	return err
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func flagInt(cmd *cobra.Command, name string) int {
	v, _ := cmd.Flags().GetInt(name)
	return v
}

func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func flagStrings(cmd *cobra.Command, name string) []string {
	v, _ := cmd.Flags().GetStringArray(name)
	return v
}

// installManagedModes puts every remaining Phase 2 mutating command in file-managed mode (Phase
// 3 §10): with capella.yaml present they edit it and apply the change; --direct keeps the old
// behaviour, --no-apply only records the change.
func installManagedModes(capella *cobra.Command) {
	find := func(path ...string) *cobra.Command {
		c, rest, err := capella.Find(path)
		if err != nil || c == nil || len(rest) != 0 || c == capella {
			panic(fmt.Sprintf("managed mode: no command %q", strings.Join(path, " ")))
		}
		return c
	}
	managed := func(b managedBuild) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error { return runManaged(cmd, args, b) }
	}
	wrap := func(b managedBuild, path ...string) {
		cmd := find(path...)
		wrapManaged(cmd, addManagedFlags(cmd), managed(b))
	}
	del := func(spec deleteSpec, path ...string) {
		cmd := find(path...)
		direct := cmd.RunE
		wrapManaged(cmd, addManagedFlags(cmd), managedDelete(direct, spec))
	}

	addClusterCreateFlags(find("cluster", "create"))
	{
		// The legacy flags always take the legacy direct path, even with a capella.yaml.
		cmd := find("cluster", "create")
		direct := cmd.RunE
		managedCreate := managed(managedClusterCreate)
		wrapManaged(cmd, addManagedFlags(cmd), func(c *cobra.Command, args []string) error {
			if usesLegacyFlags(c) {
				return direct(c, args)
			}
			return managedCreate(c, args)
		})
	}
	wrap(managedClusterUpdate, "cluster", "update")
	wrap(managedGroupAdd, "cluster", "group", "add")
	wrap(managedGroupRemove, "cluster", "group", "remove")
	del(clusterDeleteSpec, "cluster", "delete")
	wrap(managedClusterClone, "cluster", "clone")

	wrap(managedScheduleSet, "cluster", "schedule", "set")
	wrap(managedScheduleDelete, "cluster", "schedule", "delete")
	wrap(managedSchedulePause(true), "cluster", "schedule", "pause")
	wrap(managedSchedulePause(false), "cluster", "schedule", "resume")
	wrap(managedClusterBackupSet, "cluster", "backup", "schedule", "set")
	wrap(managedBackupDelete("backup-schedule", false), "cluster", "backup", "schedule", "delete")
	wrap(managedBucketBackupSet, "bucket", "backup", "schedule", "set")
	wrap(managedBackupDelete("bucket-backup-schedule", true), "bucket", "backup", "schedule", "delete")

	wrap(managedCIDRCreate, "cidr", "create")
	del(cidrDeleteSpec, "cidr", "delete")

	wrap(managedBucketAdd, "bucket", "add")
	wrap(managedBucketUpdate, "bucket", "update")
	del(bucketDeleteSpec, "bucket", "delete")
	wrap(managedScopeAdd, "scope", "add")
	del(scopeDeleteSpec, "scope", "delete")
	wrap(managedCollectionSet(true), "collection", "add")
	wrap(managedCollectionSet(false), "collection", "update")
	del(collectionDeleteSpec, "collection", "delete")

	wrap(managedCredential(true), "credential", "create")
	wrap(managedCredential(false), "credential", "update")
	del(credentialDeleteSpec, "credential", "delete")
}

// ---- helpers -----------------------------------------------------------------------------------

// projectFlag is the --project of a command, or "".
func projectFlag(cmd *cobra.Command) string { return flagString(cmd, "project") }

func fileName(ed *capellafile.Editor) string { return filepath.Base(ed.Path) }

// clusterEntry finds the cluster the first argument names.
func clusterEntry(cmd *cobra.Command, args []string, ed *capellafile.Editor) (*yaml.Node, error) {
	return findClusterNode(ed, args[0], projectFlag(cmd))
}

// childMap returns the mapping node[key] of the file, or nil.
func childMap(ed *capellafile.Editor, node *yaml.Node, key string) *yaml.Node {
	c := ed.Child(node, key)
	if c == nil || c.Kind != yaml.MappingNode {
		return nil
	}
	return c
}

// bucketEntry finds a bucket of a cluster entry.
func bucketEntry(ed *capellafile.Editor, cluster *yaml.Node, name string) (*yaml.Node, error) {
	b := ed.FindBy(cluster, "buckets", "name", name)
	if b == nil {
		return nil, notInFile("bucket", name, fileName(ed))
	}
	return b, nil
}

// scopeEntry finds a scope of a bucket entry.
func scopeEntry(ed *capellafile.Editor, bucket *yaml.Node, name string) (*yaml.Node, error) {
	s := ed.FindBy(bucket, "scopes", "name", name)
	if s == nil {
		return nil, notInFile("scope", name, fileName(ed))
	}
	return s, nil
}

// markCancelled records that the user declined the command's confirmation, so a managed wrapper
// does not go on to change the file.
func (r *runtime) markCancelled() {
	if r == nil || r.cmd == nil {
		return
	}
	if r.cmd.Annotations == nil {
		r.cmd.Annotations = map[string]string{}
	}
	r.cmd.Annotations[cancelledAnnotation] = "1"
}

const cancelledAnnotation = "cbctl.cancelled"

// ---- delete commands: act in Capella, then drop the entry from the file -------------------------

// deleteSpec describes how a delete command finds and removes its entry in capella.yaml.
type deleteSpec struct {
	kind string
	// label names the resource in messages, for example "bucket orders".
	label func(args []string) string
	// remove deletes the entry; it returns notInFile when capella.yaml does not declare it.
	remove func(cmd *cobra.Command, args []string, ed *capellafile.Editor) error
}

// managedDelete wraps a Phase 2 delete command. The Capella-side delete keeps its confirmations,
// protection checks and cascade rules; capella.yaml loses the entry afterwards, and only when
// the user did not decline. A resource Capella does not know is still removed from the file.
func managedDelete(direct func(*cobra.Command, []string) error, spec deleteSpec) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		mf := managedFlagsOf(cmd)
		disc, err := discoverConfig(cmd)
		if err != nil {
			return err
		}
		if !disc.Exists {
			return notFoundError(displayPath(disc.Path))
		}
		ed, err := capellafile.OpenEditor(disc.Path)
		if err != nil {
			return err
		}
		file := filepath.Base(ed.Path)
		probe, err := capellafile.OpenEditor(disc.Path)
		if err != nil {
			return err
		}
		if err := spec.remove(cmd, args, probe); err != nil {
			// Not in the file: still act on Capella, so a rerun of a finished delete is a no-op
			// ("not found, nothing to do") and a resource only Capella has can be deleted.
			var ce *CLIError
			if spec.kind == "cluster" || mf.noApply || !errorAsCLI(err, &ce) || ce.Code != ExitConfigInvalid || !strings.Contains(ce.Message, "is not in "+file) {
				return err
			}
			return direct(cmd, args)
		}
		label := spec.label(args)
		if mf.noApply {
			g, err := readGlobals(cmd)
			if err != nil {
				return err
			}
			rt := &runtime{cmd: cmd}
			rt.Yes, rt.Quiet = g.Yes, g.Quiet
			rt.Out = newRenderer(cmd.OutOrStdout(), cmd.ErrOrStderr(), g.Output == "json", g.Quiet)
			ok, err := confirm(rt, fmt.Sprintf("Remove %s from %s (Capella keeps it until you delete it)?", label, file))
			if err != nil || !ok {
				return err
			}
			if err := removeFromFile(cmd, ed, spec, args); err != nil {
				return err
			}
			if g.Output != "json" {
				fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s. Capella still has it.\n", label, file)
			}
			return nil
		}
		if err := direct(cmd, args); err != nil {
			var ce *CLIError
			if !errorAsCLI(err, &ce) || ce.Code != ExitConfigInvalid || !strings.Contains(ce.Message, "not found") {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s is not in Capella; removing it from %s\n", label, file)
		} else if cmd.Annotations[cancelledAnnotation] != "" {
			return nil
		}
		return removeFromFile(cmd, ed, spec, args)
	}
}

func errorAsCLI(err error, target **CLIError) bool { return errors.As(err, target) }

func removeFromFile(cmd *cobra.Command, ed *capellafile.Editor, spec deleteSpec, args []string) error {
	if err := spec.remove(cmd, args, ed); err != nil {
		return err
	}
	text, err := editedText(ed)
	if err != nil {
		return err
	}
	backup, err := ed.Save(text)
	if err != nil {
		return err
	}
	g, _ := readGlobals(cmd)
	if !g.Quiet && backup != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s updated: removed %s (previous version kept as %s)\n",
			filepath.Base(ed.Path), spec.label(args), filepath.Base(backup))
	}
	return nil
}

var clusterDeleteSpec = deleteSpec{
	kind:  "cluster",
	label: func(a []string) string { return "cluster " + a[0] },
	remove: func(cmd *cobra.Command, args []string, ed *capellafile.Editor) error {
		proj, node, err := ed.FindCluster(args[0], projectFlag(cmd))
		var amb *capellafile.AmbiguousError
		if asAmbiguous(err, &amb) {
			return usageError("add --project NAME", "%v", err)
		}
		if err != nil {
			return err
		}
		if node == nil {
			return notInFile("cluster", args[0], fileName(ed))
		}
		ed.RemoveCluster(proj, args[0])
		return nil
	},
}

func asAmbiguous(err error, target **capellafile.AmbiguousError) bool { return errors.As(err, target) }

var cidrDeleteSpec = deleteSpec{
	kind:  "allowed CIDR",
	label: func(a []string) string { return "allowed CIDR " + canonicalCIDR(a[1]) + " of " + a[0] },
	remove: func(cmd *cobra.Command, args []string, ed *capellafile.Editor) error {
		cl, err := clusterEntry(cmd, args, ed)
		if err != nil {
			return err
		}
		want := canonicalCIDR(args[1])
		if !removeMatching(ed, cl, "allowedCidrs", "cidr", func(v string) bool { return canonicalCIDR(v) == want }) {
			return notInFile("allowed CIDR", want, fileName(ed))
		}
		return nil
	},
}

// removeMatching removes the first item of node[list] whose field satisfies match.
func removeMatching(ed *capellafile.Editor, node *yaml.Node, list, field string, match func(string) bool) bool {
	seq := ed.Child(node, list)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return false
	}
	for _, item := range seq.Content {
		if v := ed.Child(item, field); v != nil && match(v.Value) {
			return ed.RemoveBy(node, list, field, v.Value)
		}
	}
	return false
}

var bucketDeleteSpec = deleteSpec{
	kind:  "bucket",
	label: func(a []string) string { return "bucket " + a[1] },
	remove: func(cmd *cobra.Command, args []string, ed *capellafile.Editor) error {
		cl, err := clusterEntry(cmd, args, ed)
		if err != nil {
			return err
		}
		if !ed.RemoveBy(cl, "buckets", "name", args[1]) {
			return notInFile("bucket", args[1], fileName(ed))
		}
		return nil
	},
}

var scopeDeleteSpec = deleteSpec{
	kind:  "scope",
	label: func(a []string) string { return "scope " + a[1] + "." + a[2] },
	remove: func(cmd *cobra.Command, args []string, ed *capellafile.Editor) error {
		cl, err := clusterEntry(cmd, args, ed)
		if err != nil {
			return err
		}
		b, err := bucketEntry(ed, cl, args[1])
		if err != nil {
			return err
		}
		if !ed.RemoveBy(b, "scopes", "name", args[2]) {
			return notInFile("scope", args[2], fileName(ed))
		}
		return nil
	},
}

var collectionDeleteSpec = deleteSpec{
	kind:  "collection",
	label: func(a []string) string { return "collection " + a[1] + "." + a[2] + "." + a[3] },
	remove: func(cmd *cobra.Command, args []string, ed *capellafile.Editor) error {
		cl, err := clusterEntry(cmd, args, ed)
		if err != nil {
			return err
		}
		b, err := bucketEntry(ed, cl, args[1])
		if err != nil {
			return err
		}
		s, err := scopeEntry(ed, b, args[2])
		if err != nil {
			return err
		}
		if !ed.RemoveBy(s, "collections", "name", args[3]) {
			return notInFile("collection", args[3], fileName(ed))
		}
		return nil
	},
}

var credentialDeleteSpec = deleteSpec{
	kind:  "credential",
	label: func(a []string) string { return "credential " + a[1] },
	remove: func(cmd *cobra.Command, args []string, ed *capellafile.Editor) error {
		cl, err := clusterEntry(cmd, args, ed)
		if err != nil {
			return err
		}
		if !ed.RemoveBy(cl, "databaseCredentials", "name", args[1]) {
			return notInFile("credential", args[1], fileName(ed))
		}
		return nil
	},
}

// ---- allowed CIDRs -----------------------------------------------------------------------------

func managedCIDRCreate(cmd *cobra.Command, args []string, e *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	cidr, err := parseCIDR(args[1])
	if err != nil {
		return Mutation{}, err
	}
	any4 := cidr == "0.0.0.0/0" || cidr == "::/0"
	if any4 && !flagBool(cmd, "allow-any-ip") {
		return Mutation{}, usageError("pass --allow-any-ip if you really mean it", "%s would allow every IP address", cidr)
	}
	expires := flagString(cmd, "expires-at")
	if expires != "" {
		t, err := time.Parse(time.RFC3339, expires)
		if err != nil {
			return Mutation{}, usageError("use RFC 3339, for example 2027-12-31T23:59:59Z", "invalid --expires-at %q", expires)
		}
		if !t.After(e.Clock.Now()) {
			return Mutation{}, usageError("pass a time in the future", "--expires-at %s is not in the future", expires)
		}
	}
	comment := flagString(cmd, "comment")
	return Mutation{
		Target:      "allowed-cidr/" + args[0] + "/" + cidr,
		Question:    fmt.Sprintf("Allow %s on %s in %s and Capella?", cidr, args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("%s is already allowed on %s; nothing to do", cidr, args[0]),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			if any4 {
				ed.Set(cl, []string{"allowAnyIp"}, true)
			}
			seq := ed.Child(cl, "allowedCidrs")
			if seq != nil && seq.Kind == yaml.SequenceNode {
				for _, item := range seq.Content {
					if v := ed.Child(item, "cidr"); v != nil && canonicalCIDR(v.Value) == cidr {
						if cmd.Flags().Changed("comment") {
							ed.Set(item, []string{"comment"}, comment)
						}
						if cmd.Flags().Changed("expires-at") {
							ed.Set(item, []string{"expiresAt"}, expires)
						}
						return nil
					}
				}
			}
			entry := capellafile.Obj{"cidr": cidr}
			if comment != "" {
				entry["comment"] = comment
			}
			if expires != "" {
				entry["expiresAt"] = expires
			}
			ed.Append(cl, "allowedCidrs", entry)
			return nil
		},
	}, nil
}

// ---- buckets, scopes, collections --------------------------------------------------------------

func managedBucketAdd(cmd *cobra.Command, args []string, env *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	name := args[1]
	ch := cmd.Flags().Changed
	settings := func(b capellafile.Obj) {
		for flag, key := range map[string]string{
			"type": "type", "storage-backend": "storageBackend", "durability": "durabilityLevel",
			"conflict-resolution": "bucketConflictResolution", "eviction-policy": "evictionPolicy"} {
			if ch(flag) {
				b[key] = flagString(cmd, flag)
			}
		}
		for flag, key := range map[string]string{
			"vbuckets": "vbuckets", "replicas": "replicas", "ttl": "timeToLiveInSeconds", "priority": "priority"} {
			if ch(flag) {
				b[key] = int64(flagInt(cmd, flag))
			}
		}
		if ch("flush") {
			b["flushEnabled"] = flagBool(cmd, "flush")
		}
	}
	// A bucket in the file but not in Capella is an earlier create that did not finish. Like
	// capellactl, the given settings go into its entry and the bucket is created, because a
	// later update could not set the storage backend or type.
	finish := recordedButMissingBucket(cmd, args, env, ed)
	if finish {
		fmt.Fprintf(cmd.ErrOrStderr(), "bucket %s is in %s but not in Capella yet (an earlier run did not finish); creating it now\n",
			name, fileName(ed))
	}
	return Mutation{
		Target:      "bucket/" + args[0] + "/" + name,
		Question:    fmt.Sprintf("Add bucket %s to %s in %s and create it in Capella?", name, args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("bucket %s already exists in %s; nothing to do", name, args[0]),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			if b := ed.FindBy(cl, "buckets", "name", name); b != nil {
				if finish {
					given := capellafile.Obj{}
					settings(given)
					if ch("memory") {
						given["memoryAllocationInMb"] = int64(flagInt(cmd, "memory"))
					}
					for k, v := range given {
						ed.Set(b, []string{k}, v)
					}
				}
				return nil
			}
			b := capellafile.Obj{"name": name, "memoryAllocationInMb": int64(flagInt(cmd, "memory"))}
			settings(b)
			ed.Append(cl, "buckets", b)
			return nil
		},
	}, nil
}

// recordedButMissingBucket reports whether bucket args[1] of cluster args[0] is in the file
// while its cluster exists in Capella without it. Any lookup failure reports false, so the
// usual plan decides.
func recordedButMissingBucket(cmd *cobra.Command, args []string, env *envRuntime, ed *capellafile.Editor) bool {
	if env == nil || env.runtime == nil {
		return false
	}
	cl, err := clusterEntry(cmd, args, ed)
	if err != nil || ed.FindBy(cl, "buckets", "name", args[1]) == nil {
		return false
	}
	t, err := env.locateCluster(args[0], projectFlag(cmd))
	if err != nil {
		return false
	}
	pid, cid := t.ids()
	if t.freeTier() {
		_, _, err = env.Client.GetFreeBucket(env.Ctx, pid, cid, capellaapi.BucketID(args[1]))
	} else {
		_, _, err = env.Client.GetBucket(env.Ctx, pid, cid, capellaapi.BucketID(args[1]))
	}
	return errors.Is(err, capellaapi.ErrNotFound)
}

func managedBucketUpdate(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	ch := cmd.Flags().Changed
	if ch("flush") && ch("no-flush") {
		return Mutation{}, usageError("use either --flush or --no-flush", "--flush and --no-flush are mutually exclusive")
	}
	if ch("cross-cluster-versioning") && ch("no-cross-cluster-versioning") {
		return Mutation{}, usageError("use one of them", "--cross-cluster-versioning and --no-cross-cluster-versioning are mutually exclusive")
	}
	changes := capellafile.Obj{}
	for flag, key := range map[string]string{"memory": "memoryAllocationInMb", "replicas": "replicas", "ttl": "timeToLiveInSeconds", "priority": "priority"} {
		if ch(flag) {
			changes[key] = int64(flagInt(cmd, flag))
		}
	}
	if ch("durability") {
		changes["durabilityLevel"] = flagString(cmd, "durability")
	}
	if ch("flush") {
		changes["flushEnabled"] = flagBool(cmd, "flush")
	}
	if ch("no-flush") {
		changes["flushEnabled"] = false
	}
	if ch("cross-cluster-versioning") {
		changes["enableCrossClusterVersioning"] = flagBool(cmd, "cross-cluster-versioning")
	}
	if ch("no-cross-cluster-versioning") {
		changes["enableCrossClusterVersioning"] = false
	}
	if len(changes) == 0 {
		return Mutation{}, usageError("add at least one option to change, for example --memory 2048", "nothing to update: no option was given")
	}
	name := args[1]
	return Mutation{
		Target:        "bucket/" + args[0] + "/" + name,
		Question:      fmt.Sprintf("Record the new settings of bucket %s in %s and apply them?", name, fileName(ed)),
		NoopMessage:   fmt.Sprintf("bucket %s already has these settings; nothing to do", name),
		NoDescendants: true,
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			b, err := bucketEntry(ed, cl, name)
			if err != nil {
				return err
			}
			for _, k := range sortedObjKeys(changes) {
				ed.SetOrdered(b, []string{k}, changes[k], capellafile.BucketKeyOrder)
			}
			return nil
		},
	}, nil
}

func sortedObjKeys(m capellafile.Obj) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func managedScopeAdd(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	bucket, name := args[1], args[2]
	return Mutation{
		Target:        "scope/" + args[0] + "/" + bucket + "/" + name,
		Question:      fmt.Sprintf("Add scope %s.%s to %s and create it in Capella?", bucket, name, fileName(ed)),
		NoopMessage:   fmt.Sprintf("scope %s.%s already exists; nothing to do", bucket, name),
		NoDescendants: true,
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			b, err := bucketEntry(ed, cl, bucket)
			if err != nil {
				return err
			}
			if ed.FindBy(b, "scopes", "name", name) == nil {
				ed.Append(b, "scopes", capellafile.Obj{"name": name})
			}
			return nil
		},
	}, nil
}

func managedCollectionSet(add bool) managedBuild {
	return func(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
		bucket, scope, name := args[1], args[2], args[3]
		if !add && !cmd.Flags().Changed("max-ttl") {
			return Mutation{}, usageError("pass --max-ttl SECONDS", "nothing to update: no option was given")
		}
		verb := "Add"
		if !add {
			verb = "Update"
		}
		return Mutation{
			Target:        "collection/" + args[0] + "/" + bucket + "/" + scope + "/" + name,
			Question:      fmt.Sprintf("%s collection %s.%s.%s in %s and apply it?", verb, bucket, scope, name, fileName(ed)),
			NoopMessage:   fmt.Sprintf("collection %s.%s.%s already matches; nothing to do", bucket, scope, name),
			NoDescendants: true,
			Edit: func(ed *capellafile.Editor) error {
				cl, err := clusterEntry(cmd, args, ed)
				if err != nil {
					return err
				}
				b, err := bucketEntry(ed, cl, bucket)
				if err != nil {
					return err
				}
				s := ed.FindBy(b, "scopes", "name", scope)
				if s == nil && add && scope != resources.DefaultScope {
					// As in capellactl, adding a collection to a new scope declares the scope too.
					ed.Append(b, "scopes", capellafile.Obj{"name": scope})
					s = ed.FindBy(b, "scopes", "name", scope)
				}
				if s == nil {
					return notInFile("scope", scope, fileName(ed))
				}
				c := ed.FindBy(s, "collections", "name", name)
				switch {
				case c == nil && !add:
					return notInFile("collection", name, fileName(ed))
				case c == nil:
					entry := capellafile.Obj{"name": name}
					if cmd.Flags().Changed("max-ttl") {
						entry["maxTTL"] = int64(flagInt(cmd, "max-ttl"))
					}
					ed.Append(s, "collections", entry)
				case cmd.Flags().Changed("max-ttl"):
					ed.Set(c, []string{"maxTTL"}, int64(flagInt(cmd, "max-ttl")))
				}
				return nil
			},
		}, nil
	}
}

// ---- credentials -------------------------------------------------------------------------------

// credentialPassword is the file value for --password-env / --password-file: a reference, never a
// literal. Without either flag it is "" and the password is left out.
func credentialPassword(cmd *cobra.Command) (string, error) {
	env, file := flagString(cmd, "password-env"), flagString(cmd, "password-file")
	switch {
	case env != "" && file != "":
		return "", usageError("keep one of them", "both --password-env and --password-file were given")
	case env != "":
		return "${" + env + "}", nil
	case file != "":
		return "${file:" + file + "}", nil
	}
	return "", nil
}

func managedCredential(create bool) managedBuild {
	return func(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
		name := args[1]
		pw, err := credentialPassword(cmd)
		if err != nil {
			return Mutation{}, err
		}
		af := &accessFlags{
			privileges: flagString(cmd, "privileges"), bucket: flagString(cmd, "bucket"), scope: flagString(cmd, "scope"),
			collections: flagStrings(cmd, "collection"), allBuckets: flagBool(cmd, "all-buckets"),
		}
		var access []any
		if !create && af.given() && af.privileges == "" {
			// As in capellactl, an update that names only the resources keeps the privileges of
			// the credential's one grant in the file.
			privs, err := filePrivileges(cmd, args, ed)
			if err != nil {
				return Mutation{}, err
			}
			af.privileges = "read" // validated placeholder; replaced by the file's privileges below
			entry, err := af.grant()
			if err != nil {
				return Mutation{}, err
			}
			entry.Privileges = privs
			if access, err = accessToFile(entry); err != nil {
				return Mutation{}, err
			}
		} else if create || af.given() {
			entry, err := af.grant()
			if err != nil {
				return Mutation{}, err
			}
			access, err = accessToFile(entry)
			if err != nil {
				return Mutation{}, err
			}
		}
		rotate := !create && flagBool(cmd, "rotate-password")
		if !create && access == nil && pw == "" && !rotate {
			return Mutation{}, usageError("add --privileges with a bucket, --password-env/--password-file or --rotate-password",
				"nothing to update: no option was given")
		}
		verb := "Add"
		if !create {
			verb = "Update"
		}
		return Mutation{
			Target:        "credential/" + args[0] + "/" + name,
			Question:      fmt.Sprintf("%s credential %s in %s and apply it?", verb, name, fileName(ed)),
			NoopMessage:   fmt.Sprintf("credential %s already matches; nothing to do", name),
			NoDescendants: true,
			Options:       func(rc *plan.RunContext) { rc.RotatePasswords = rotate },
			Edit: func(ed *capellafile.Editor) error {
				cl, err := clusterEntry(cmd, args, ed)
				if err != nil {
					return err
				}
				c := ed.FindBy(cl, "databaseCredentials", "name", name)
				switch {
				case c == nil && !create:
					return notInFile("credential", name, fileName(ed))
				case c == nil:
					entry := capellafile.Obj{"name": name, "access": access}
					if pw != "" {
						entry["password"] = pw
					}
					ed.Append(cl, "databaseCredentials", entry)
				case create && !sameFileAccess(ed.Child(c, "access"), access):
					// create never rewrites a declared credential: changing access is an update.
					return Errorf(ExitBlocked, fmt.Sprintf("change it with 'cbctl capella credential update %s %s'", args[0], name),
						"credential %s is already in %s with different access", name, fileName(ed))
				default:
					if pw != "" {
						ed.Set(c, []string{"password"}, pw)
					}
					if access != nil {
						ed.Set(c, []string{"access"}, access)
					}
				}
				return nil
			},
		}, nil
	}
}

// filePrivileges returns the privileges of the one access grant of credential args[1] in the
// file. A credential that is not in the file, or has several grants, is a usage error.
func filePrivileges(cmd *cobra.Command, args []string, ed *capellafile.Editor) ([]string, error) {
	cl, err := clusterEntry(cmd, args, ed)
	if err != nil {
		return nil, err
	}
	c := ed.FindBy(cl, "databaseCredentials", "name", args[1])
	if c == nil {
		return nil, notInFile("credential", args[1], fileName(ed))
	}
	var grants []struct {
		Privileges []string `yaml:"privileges"`
	}
	if n := ed.Child(c, "access"); n != nil {
		if err := n.Decode(&grants); err != nil {
			return nil, Errorf(ExitConfigInvalid, "fix the access of the credential in "+fileName(ed), "reading the access of credential %s: %v", args[1], err)
		}
	}
	switch len(grants) {
	case 1:
		return grants[0].Privileges, nil
	case 0:
		return nil, usageError("pass --privileges read, write or read+write", "credential %s has no access grant in %s", args[1], fileName(ed))
	default:
		return nil, usageError("pass --privileges with the resources to replace them with one grant, or edit "+fileName(ed),
			"credential %s has several access grants in %s", args[1], fileName(ed))
	}
}

// sameFileAccess reports whether the access node of a credential in the file grants exactly what
// access does (privilege aliases and grouping do not matter).
func sameFileAccess(node *yaml.Node, access []any) bool {
	var declared any
	if node != nil {
		if err := node.Decode(&declared); err != nil {
			return false
		}
	}
	return len(resources.AccessChanges(access, declared)) == 0
}

// accessToFile converts one API access grant into its capella.yaml form.
func accessToFile(e capellaapi.AccessEntry) ([]any, error) {
	obj := capellafile.Obj{"privileges": toAnyList(e.Privileges)}
	if len(e.Resources.Buckets) > 0 {
		var buckets []any
		for _, b := range e.Resources.Buckets {
			bo := capellafile.Obj{"name": b.Name}
			if len(b.Scopes) > 0 {
				var scopes []any
				for _, s := range b.Scopes {
					so := capellafile.Obj{"name": s.Name}
					if len(s.Collections) > 0 {
						so["collections"] = toAnyList(s.Collections)
					}
					scopes = append(scopes, so)
				}
				bo["scopes"] = scopes
			}
			buckets = append(buckets, bo)
		}
		obj["resources"] = capellafile.Obj{"buckets": buckets}
	}
	return []any{obj}, nil
}

func toAnyList(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// ---- schedules and backups ---------------------------------------------------------------------

func managedScheduleSet(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	set := map[string]string{}
	if cmd.Flags().Changed("timezone") {
		set["timezone"] = flagString(cmd, "timezone")
	}
	for _, key := range append([]string{"weekdays", "weekend"}, scheduleDays...) {
		if cmd.Flags().Changed(key) {
			v := strings.ToLower(strings.TrimSpace(flagString(cmd, key)))
			if _, err := parseWindow(key, v); err != nil {
				return Mutation{}, err
			}
			set[key] = v
		}
	}
	if len(set) == 0 {
		return Mutation{}, usageError("add at least one of --timezone, --weekdays, --weekend or a day option (for example --friday 08:00-14:30)",
			"nothing to set: no schedule option was given")
	}
	if tz, ok := set["timezone"]; ok {
		if _, err := time.LoadLocation(tz); err != nil {
			return Mutation{}, usageError("use an IANA name such as US/Pacific, Europe/London or Asia/Calcutta", "unknown timezone %q", tz)
		}
	}
	return Mutation{
		Target:      "schedule/" + args[0],
		Question:    fmt.Sprintf("Record the on/off schedule of %s in %s and apply it?", args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("the schedule of %s already matches; nothing to do", args[0]),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			h := childMap(ed, cl, "hibernation")
			if h == nil && set["timezone"] == "" {
				return usageError("add --timezone, for example --timezone US/Pacific", "a new schedule needs a timezone")
			}
			if h != nil {
				ed.Delete(h, "ensure")
			}
			for _, k := range append([]string{"timezone", "weekdays", "weekend"}, scheduleDays...) {
				if v, ok := set[k]; ok {
					ed.Set(cl, []string{"hibernation", k}, v)
				}
			}
			return nil
		},
	}, nil
}

func managedScheduleDelete(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	return Mutation{
		Target:      "schedule/" + args[0],
		Question:    fmt.Sprintf("Mark the on/off schedule of %s absent in %s and delete it in Capella?", args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("%s has no on/off schedule; nothing to do", args[0]),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			if childMap(ed, cl, "hibernation") == nil {
				return nil
			}
			ed.Set(cl, []string{"hibernation", "ensure"}, "absent")
			return nil
		},
	}, nil
}

func managedSchedulePause(pause bool) managedBuild {
	return func(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
		word, question := "resumed", fmt.Sprintf("Resume the on/off schedule of %s (record it in %s and apply)?", args[0], fileName(ed))
		if pause {
			word, question = "paused", fmt.Sprintf("Pause the on/off schedule of %s (record it in %s and apply)?", args[0], fileName(ed))
		}
		powerOn := pause && flagBool(cmd, "power-on")
		return Mutation{
			Target:      "schedule/" + args[0],
			Question:    question,
			NoopMessage: fmt.Sprintf("the schedule of %s is already %s; nothing to do", args[0], word),
			Options:     func(rc *plan.RunContext) { rc.PowerOn = powerOn },
			Edit: func(ed *capellafile.Editor) error {
				cl, err := clusterEntry(cmd, args, ed)
				if err != nil {
					return err
				}
				h := childMap(ed, cl, "hibernation")
				if h == nil {
					return blockedError(fmt.Sprintf("create one with 'cbctl capella cluster schedule set %s --timezone TZ --weekdays HH:MM-HH:MM'", args[0]),
						"cluster %s has no on/off schedule in %s", args[0], fileName(ed))
				}
				if pause {
					ed.Set(cl, []string{"hibernation", "paused"}, true)
				} else {
					ed.Delete(h, "paused")
				}
				return nil
			},
		}, nil
	}
}

var hmValue = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func managedClusterBackupSet(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	ch := cmd.Flags().Changed
	if !ch("every") && !ch("retain") && !ch("start") && !ch("copy-to") && !flagBool(cmd, "no-copies") {
		return Mutation{}, usageError("add at least one of --every, --retain, --start, --copy-to or --no-copies", "nothing to set: no schedule option was given")
	}
	if ch("copy-to") && flagBool(cmd, "no-copies") {
		return Mutation{}, usageError("use either --copy-to or --no-copies", "--copy-to and --no-copies are mutually exclusive")
	}
	if ch("start") && !hmValue.MatchString(flagString(cmd, "start")) {
		return Mutation{}, usageError("give a UTC time of day as HH:MM, for example --start 02:00", "--start takes a UTC time of day HH:MM (got %q)", flagString(cmd, "start"))
	}
	if ch("every") && flagInt(cmd, "every") <= 0 {
		return Mutation{}, usageError("give a number of hours", "--every must be a positive number of hours")
	}
	retention := 0
	if ch("retain") {
		h, err := parseHours(flagString(cmd, "retain"), "--retain")
		if err != nil {
			return Mutation{}, err
		}
		retention = h
	}
	return Mutation{
		Target:      "backup-schedule/" + args[0],
		Question:    fmt.Sprintf("Record the backup schedule of %s in %s and apply it?", args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("the backup schedule of %s already matches; nothing to do", args[0]),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			if err := refuseFreeEntry(ed, cl); err != nil {
				return err
			}
			b := childMap(ed, cl, "backupSchedule")
			if (b == nil || ed.Child(b, "interval") == nil) && !ch("every") || (b == nil || ed.Child(b, "retention") == nil) && !ch("retain") {
				return usageError("a new backup schedule needs --every HOURS and --retain DURATION",
					"a new backup schedule needs --every HOURS and --retain DURATION")
			}
			if b != nil {
				ed.Delete(b, "ensure")
			}
			if ch("every") {
				ed.Set(cl, []string{"backupSchedule", "interval"}, int64(flagInt(cmd, "every")))
			}
			if ch("retain") {
				ed.Set(cl, []string{"backupSchedule", "retention"}, int64(retention))
			}
			if ch("start") {
				ed.Set(cl, []string{"backupSchedule", "startTime"}, flagString(cmd, "start"))
			}
			switch {
			case ch("copy-to"):
				ed.Set(cl, []string{"backupSchedule", "copyToRegions"}, toAnyList(flagStrings(cmd, "copy-to")))
			case flagBool(cmd, "no-copies"):
				if b != nil {
					ed.Delete(b, "copyToRegions")
				}
			}
			return nil
		},
	}, nil
}

var backupDayValues = backupDays

// refuseFreeEntry fails with ExitBlocked when the cluster entry is a free-tier cluster, which has
// no backups (the same refusal as the direct commands).
func refuseFreeEntry(ed *capellafile.Editor, cl *yaml.Node) error {
	if t := ed.Child(cl, "tier"); t != nil && t.Kind == yaml.ScalarNode && t.Value == "free" {
		return blockedError("create a provisioned cluster for data that needs backups ('cbctl capella cluster create')", "%s", freeNoBackups)
	}
	return nil
}

func managedBucketBackupSet(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	ch := cmd.Flags().Changed
	if ch("day") && !slices.Contains(backupDayValues, flagString(cmd, "day")) {
		return Mutation{}, usageError("use one of "+strings.Join(backupDayValues, ", "), "invalid --day %q", flagString(cmd, "day"))
	}
	if ch("start-at") && (flagInt(cmd, "start-at") < 0 || flagInt(cmd, "start-at") > 23) {
		return Mutation{}, usageError("use an hour from 0 to 23", "--start-at must be 0-23")
	}
	if ch("incremental-every") && !slices.Contains(backupIncrements, flagInt(cmd, "incremental-every")) {
		return Mutation{}, usageError("use one of 1, 2, 4, 6, 8, 12, 24", "invalid --incremental-every %d", flagInt(cmd, "incremental-every"))
	}
	if ch("retention") && !slices.Contains(retentionValues(), flagString(cmd, "retention")) {
		return Mutation{}, usageError("use one of "+strings.Join(retentionValues(), ", "), "invalid --retention %q", flagString(cmd, "retention"))
	}
	bucket := args[1]
	return Mutation{
		Target:      "bucket-backup-schedule/" + args[0] + "/" + bucket,
		Question:    fmt.Sprintf("Record the backup schedule of bucket %s in %s and apply it?", bucket, fileName(ed)),
		NoopMessage: fmt.Sprintf("the backup schedule of bucket %s already matches; nothing to do", bucket),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			if err := refuseFreeEntry(ed, cl); err != nil {
				return err
			}
			b, err := bucketEntry(ed, cl, bucket)
			if err != nil {
				return err
			}
			s := childMap(ed, b, "backupSchedule")
			isNew := s == nil || ed.Child(s, "ensure") != nil
			if s != nil {
				ed.Delete(s, "ensure")
			}
			put := func(k string, v any) { ed.SetOrdered(b, []string{"backupSchedule", k}, v, capellafile.BucketKeyOrder) }
			if isNew {
				// A new schedule records every value, so the file shows what Capella runs.
				def := bucketScheduleDefaults()
				for _, k := range []string{"dayOfWeek", "startAt", "incrementalEvery", "retentionTime", "costOptimizedRetention"} {
					v := def[k]
					if n, ok := v.(int); ok {
						v = int64(n)
					}
					put(k, v)
				}
			}
			if ch("day") {
				put("dayOfWeek", flagString(cmd, "day"))
			}
			if ch("start-at") {
				put("startAt", int64(flagInt(cmd, "start-at")))
			}
			if ch("incremental-every") {
				put("incrementalEvery", int64(flagInt(cmd, "incremental-every")))
			}
			if ch("retention") {
				put("retentionTime", flagString(cmd, "retention"))
			}
			if ch("cost-optimized") {
				put("costOptimizedRetention", flagBool(cmd, "cost-optimized"))
			}
			return nil
		},
	}, nil
}

// managedBackupDelete marks a cluster or bucket backup schedule absent.
func managedBackupDelete(kind string, bucketLevel bool) managedBuild {
	return func(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
		target := kind + "/" + args[0]
		what := "the backup schedule of " + args[0]
		owner := "cluster " + args[0]
		if bucketLevel {
			target += "/" + args[1]
			what = "the backup schedule of bucket " + args[1]
			owner = "bucket " + args[1] + " of " + args[0]
		}
		return Mutation{
			Target:      target,
			Question:    fmt.Sprintf("Mark %s absent in %s and delete it in Capella?", what, fileName(ed)),
			NoopMessage: owner + " has no backup schedule; nothing to do",
			Edit: func(ed *capellafile.Editor) error {
				cl, err := clusterEntry(cmd, args, ed)
				if err != nil {
					return err
				}
				holder := cl
				if bucketLevel {
					if holder, err = bucketEntry(ed, cl, args[1]); err != nil {
						return err
					}
				}
				if childMap(ed, holder, "backupSchedule") == nil {
					return nil
				}
				if bucketLevel {
					ed.SetOrdered(holder, []string{"backupSchedule", "ensure"}, "absent", capellafile.BucketKeyOrder)
				} else {
					ed.Set(holder, []string{"backupSchedule", "ensure"}, "absent")
				}
				return nil
			},
		}, nil
	}
}

// ---- cluster update, service groups ------------------------------------------------------------

// diskChange is the disk part of cluster update / group add.
type diskChange struct {
	diskType      string
	size, iops    *int
	autoExpansion *bool
}

func diskChangeOf(cmd *cobra.Command) diskChange {
	var d diskChange
	ch := cmd.Flags().Changed
	if ch("disk-type") {
		d.diskType = flagString(cmd, "disk-type")
	}
	if ch("disk-size") {
		v := flagInt(cmd, "disk-size")
		d.size = &v
	}
	if ch("disk-iops") {
		v := flagInt(cmd, "disk-iops")
		d.iops = &v
	}
	if ch("disk-auto-expansion") {
		v := flagBool(cmd, "disk-auto-expansion")
		d.autoExpansion = &v
	}
	return d
}

func (d diskChange) any() bool {
	return d.diskType != "" || d.size != nil || d.iops != nil || d.autoExpansion != nil
}

// apply returns the group's new disk mapping. A new type starts from that type's defaults, but
// keeps the current size where the type allows it (IOPS become the recommendation for the
// size); otherwise the given values change the current disk.
func (d diskChange) apply(provider string, current capellafile.Obj) capellafile.Obj {
	if curType, _ := current["type"].(string); d.diskType != "" && d.diskType != curType {
		size := d.size
		if cur, ok := current["storage"].(int64); ok && size == nil && !fixedSizeDisk(provider, d.diskType) {
			keep := int(cur)
			if t, ok := presetDisk(provider, d.diskType); !ok || len(t.Sizes) == 0 || slices.Contains(t.Sizes, keep) {
				size = &keep
			}
		}
		return diskToFile(makeDisk(provider, d.diskType, size, d.iops, d.autoExpansion))
	}
	out := capellafile.Obj{}
	for k, v := range current {
		out[k] = v
	}
	if d.size != nil {
		out["storage"] = int64(*d.size)
	}
	if d.iops != nil {
		out["iops"] = int64(*d.iops)
	}
	if d.autoExpansion != nil {
		out["autoExpansion"] = *d.autoExpansion
	}
	return out
}

func diskToFile(d *capellaapi.Disk) capellafile.Obj {
	out := capellafile.Obj{"type": d.Type}
	if d.Storage != nil {
		out["storage"] = int64(*d.Storage)
	}
	if d.IOPS != nil {
		out["iops"] = int64(*d.IOPS)
	}
	if d.AutoExpansion != nil {
		out["autoExpansion"] = *d.AutoExpansion
	}
	return out
}

// clusterProviderOf reads cloudProvider.type of a cluster entry.
func clusterProviderOf(ed *capellafile.Editor, cl *yaml.Node) string {
	if cp := childMap(ed, cl, "cloudProvider"); cp != nil {
		if t := ed.Child(cp, "type"); t != nil {
			return t.Value
		}
	}
	return ""
}

func managedClusterUpdate(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	ch := cmd.Flags().Changed
	disk := diskChangeOf(cmd)
	group := ch("cpu") || ch("ram") || disk.any()
	if !ch("description") && !ch("support-plan") && !ch("support-timezone") && !group {
		return Mutation{}, usageError("add at least one option, for example --support-plan enterprise or --cpu 8 --ram 32", "nothing to update: no option was given")
	}
	if ch("support-plan") && !slices.Contains(supportPlans, strings.ToLower(flagString(cmd, "support-plan"))) {
		return Mutation{}, usageError("use one of "+strings.Join(supportPlans, ", "), "unknown support plan %q", flagString(cmd, "support-plan"))
	}
	if ch("support-timezone") && !slices.Contains(supportTimezones, flagString(cmd, "support-timezone")) {
		return Mutation{}, usageError("use one of "+strings.Join(supportTimezones, ", "), "unknown support timezone %q", flagString(cmd, "support-timezone"))
	}
	return Mutation{
		Target:      "cluster/" + args[0],
		Question:    fmt.Sprintf("Record the new settings of %s in %s and apply them?", args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("%s already has these settings; nothing to do", args[0]),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			if ch("description") {
				ed.Set(cl, []string{"description"}, flagString(cmd, "description"))
			}
			if ch("support-plan") {
				ed.Set(cl, []string{"support", "plan"}, strings.ToLower(flagString(cmd, "support-plan")))
			}
			if ch("support-timezone") {
				ed.Set(cl, []string{"support", "timezone"}, flagString(cmd, "support-timezone"))
			}
			if !group {
				return nil
			}
			gn, err := ed.Group(cl, flagString(cmd, "group"))
			if err != nil {
				return usageError("name the group with --group SERVICES", "%v", err)
			}
			if ch("cpu") {
				ed.Set(gn, []string{"node", "compute", "cpu"}, int64(flagInt(cmd, "cpu")))
			}
			if ch("ram") {
				ed.Set(gn, []string{"node", "compute", "ram"}, int64(flagInt(cmd, "ram")))
			}
			if disk.any() {
				var current capellafile.Obj
				if n := childMap(ed, gn, "node"); n != nil {
					if dn := childMap(ed, n, "disk"); dn != nil {
						current, _ = ed.Decode(dn)
					}
				}
				provider := clusterProviderOf(ed, cl)
				newType := disk.diskType
				if newType == "" {
					newType, _ = current["type"].(string)
				}
				if fixedSizeDisk(provider, newType) && (disk.size != nil || disk.iops != nil) {
					return usageError("choose a larger type with --disk-type (for example P20), or Ultra to set size and IOPS yourself",
						"azure disk type %s has a size and IOPS set by the type", newType)
				}
				ed.Set(gn, []string{"node", "disk"}, disk.apply(provider, current))
			}
			return nil
		},
	}, nil
}

func managedGroupAdd(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	ch := cmd.Flags().Changed
	services := flagString(cmd, "services")
	if services == "" {
		return Mutation{}, usageError("pass --services, for example --services search", "the services of the new group are required")
	}
	names, err := parseServices(services, "--services")
	if err != nil {
		return Mutation{}, err
	}
	nodes := 2
	if ch("nodes") {
		nodes = flagInt(cmd, "nodes")
		if nodes < 2 || nodes > 24 {
			return Mutation{}, usageError("choose 2-24 nodes", "--nodes %d is outside the limits of an extra group (2-24)", nodes)
		}
	}
	key := strings.Join(names, ",")
	disk := diskChangeOf(cmd)
	var picked capellafile.Obj
	if canPrompt(cmd) {
		if cl, err := clusterEntry(cmd, args, ed); err == nil {
			if _, err := ed.Group(cl, key); err != nil { // a new group: ask what capellactl asks
				w := promptOf(cmd)
				if !ch("nodes") {
					if nodes, err = w.askInt("Nodes", 2, func(n int) error {
						if n < otherGroupMin || n > otherGroupMax {
							return fmt.Errorf("an extra group has %d–%d nodes", otherGroupMin, otherGroupMax)
						}
						return nil
					}); err != nil {
						return Mutation{}, err
					}
				}
				if disk.diskType == "" && disk.size == nil && disk.iops == nil {
					provider := clusterProviderOf(ed, cl)
					d, err := w.askDisk(provider, dataGroupDisk(ed, cl, provider))
					if err != nil {
						return Mutation{}, err
					}
					size, iops := intOrNil(d.size), intOrNil(d.iops)
					picked = diskToFile(makeDisk(provider, d.diskType, size, iops, disk.autoExpansion))
				}
			}
		}
	}
	return Mutation{
		Target:      "cluster/" + args[0],
		Question:    fmt.Sprintf("Add service group %s to %s in %s and apply it?", key, args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("group %s already exists on %s; nothing to do", key, args[0]),
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			if v := ed.Child(cl, "tier"); v != nil && v.Value == "free" {
				return blockedError("create a provisioned cluster for that ('cbctl capella cluster create')", "%s", freeTierBlocked)
			}
			if _, single, _ := fileGroups(cl); single {
				return usageError("create a multiNode cluster to add groups", "singleNode clusters have exactly one service group")
			}
			if existing, err := ed.Group(cl, key); err == nil {
				if v := ed.Child(existing, "numOfNodes"); ch("nodes") && v != nil && v.Value != fmt.Sprint(nodes) {
					return usageError(fmt.Sprintf("change its node count with 'cbctl capella cluster scale %s --group %s --nodes N'", args[0], key),
						"group %s already exists with %s nodes", key, v.Value)
				}
				return nil
			}
			// Options not given are copied from the group that runs data.
			grp := capellafile.Obj{"node": map[string]any{"compute": map[string]any{"cpu": int64(4), "ram": int64(16)}}}
			if dg, err := ed.Group(cl, ""); err == nil {
				if src, err := ed.Decode(dg); err == nil && src != nil {
					grp = src
				}
			}
			grp["services"] = toAnyList(names)
			grp["numOfNodes"] = int64(nodes)
			node, _ := grp["node"].(map[string]any)
			if node == nil {
				node = map[string]any{}
			}
			compute, _ := node["compute"].(map[string]any)
			if compute == nil {
				compute = map[string]any{}
			}
			if ch("cpu") {
				compute["cpu"] = int64(flagInt(cmd, "cpu"))
			}
			if ch("ram") {
				compute["ram"] = int64(flagInt(cmd, "ram"))
			}
			node["compute"] = compute
			switch {
			case picked != nil:
				node["disk"] = picked
			case disk.any():
				cur, _ := node["disk"].(map[string]any)
				node["disk"] = disk.apply(clusterProviderOf(ed, cl), cur)
			}
			grp["node"] = node
			ed.Append(cl, "serviceGroups", grp)
			return nil
		},
	}, nil
}

func managedGroupRemove(cmd *cobra.Command, args []string, _ *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	names, err := parseServices(args[1], "SERVICES")
	if err != nil {
		return Mutation{}, err
	}
	key := strings.Join(names, ",")
	return Mutation{
		Target:      "cluster/" + args[0],
		Question:    fmt.Sprintf("Remove service group %s from %s in %s and apply it?", key, args[0], fileName(ed)),
		NoopMessage: fmt.Sprintf("%s has no group %s; nothing to do", args[0], key),
		Options: func(rc *plan.RunContext) {
			rc.AllowServiceGroupRemoval = true
			rc.AllowServiceRemoval = flagBool(cmd, "allow-service-removal")
			rc.SkipCapacityCheck = flagBool(cmd, "skip-capacity-check")
		},
		Edit: func(ed *capellafile.Editor) error {
			cl, err := clusterEntry(cmd, args, ed)
			if err != nil {
				return err
			}
			gn, err := ed.Group(cl, key)
			if err != nil {
				return usageError("check the groups with 'cbctl capella cluster group list "+args[0]+"'", "%v", err)
			}
			seq := ed.Child(cl, "serviceGroups")
			for i, g := range seq.Content {
				if g == gn {
					seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
					break
				}
			}
			return nil
		},
	}, nil
}

// ---- cluster create and clone ------------------------------------------------------------------

// addClusterCreateFlags adds the options of the file-managed cluster create. The legacy flags of
// the direct command stay.
func addClusterCreateFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("name", "", "Name of the new cluster (or give NAME as the argument)")
	f.String("provider", "", "Cloud provider: aws, gcp or azure (default aws)")
	f.String("region", "", "Region (default per provider)")
	f.String("cidr", "", "Cluster network CIDR (default: the first free 10.x.y.0/24)")
	f.String("size", "", "Size preset: dev, small, medium, large or custom (default small)")
	f.Int("nodes", 0, "Nodes (overrides the preset)")
	f.Int("cpu", 0, "vCPUs per node (overrides the preset)")
	f.Int("ram", 0, "RAM GB per node (overrides the preset)")
	f.StringArray("group", nil, "Service group SERVICES:NODESxCPU/RAM[:DISK] (repeatable), for example data:3x8/32 or index,query:2x8/32:type=io2")
	f.String("disk-type", "", "Disk type (default per provider; see 'cbctl capella cluster disk-types')")
	f.Int("disk-size", 0, "Disk GB per node")
	f.Int("disk-iops", 0, "Disk IOPS")
	f.Bool("disk-auto-expansion", false, "Azure: let disk storage expand automatically")
	f.String("services", "", "Comma-separated services (default data,index,query)")
	f.String("availability", "", "Availability zones: single or multi")
	f.String("support-plan", "", "Support plan: basic, developer pro or enterprise (default developer pro)")
	f.String("support-timezone", "", "Support timezone: ET, GMT, IST or PT (default PT)")
	f.String("description", "", "Cluster description")
	f.Bool("free-tier", false, "Deploy the organization's free single-node cluster")
	f.Bool("single-node", false, "One node (development)")
	f.String("server-version", "", "Couchbase Server version, for example 8.0 (default: Capella's)")
	f.String("zone", "", "Availability zone (single-AZ clusters only)")
	f.String("cmek-id", "", "ID of an existing customer-managed encryption key")
	f.Bool("private-dns", false, "Enable private DNS resolution")
}

var (
	groupSpecPattern = regexp.MustCompile(`^\s*(\d+)\s*x\s*(\d+)\s*/\s*(\d+)\s*$`)
	versionShape     = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)
)

// parseGroupSpec reads --group SERVICES:NODESxCPU/RAM[:DISK] into a service group mapping.
func parseGroupSpec(text, provider string, base diskChange) (capellafile.Obj, error) {
	where := "--group " + text
	parts := strings.Split(text, ":")
	help := "write each --group as SERVICES:NODESxCPU/RAM[:DISK], for example data:3x8/32 or index,query:2x8/32:type=io2"
	if len(parts) != 2 && len(parts) != 3 {
		return nil, usageError(help, "%s: expected SERVICES:NODESxCPU/RAM[:DISK], e.g. data:3x8/32", where)
	}
	services, err := parseServices(parts[0], where)
	if err != nil {
		return nil, err
	}
	m := groupSpecPattern.FindStringSubmatch(parts[1])
	if m == nil {
		return nil, usageError(help, "%s: expected NODESxCPU/RAM, e.g. 3x8/32 (got '%s')", where, parts[1])
	}
	disk := base
	if len(parts) == 3 {
		for _, item := range strings.Split(parts[2], ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(item), "=")
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if !ok || !slices.Contains([]string{"type", "size", "iops", "auto-expansion"}, k) {
				return nil, usageError("use type=, size=, iops= or auto-expansion=, for example data:3x8/32:type=io2,size=200", "%s: unknown disk option '%s'", where, k)
			}
			switch k {
			case "type":
				if v == "" {
					return nil, usageError("give a disk type, for example type=io2 ('cbctl capella cluster disk-types' lists them)", "%s: type= has no value", where)
				}
				disk.diskType = v
			case "size", "iops":
				var n int
				if _, err := fmt.Sscanf(v, "%d", &n); err != nil || fmt.Sprint(n) != v || n < 0 {
					return nil, usageError("give a whole number, for example size=200", "%s: %s must be a whole number (got '%s')", where, k, v)
				}
				if k == "size" {
					disk.size = &n
				} else {
					disk.iops = &n
				}
			default: // auto-expansion
				v = strings.ToLower(v)
				if v != "true" && v != "false" {
					return nil, usageError("give auto-expansion=true or auto-expansion=false", "%s: auto-expansion is true or false (got '%s')", where, v)
				}
				b := v == "true"
				disk.autoExpansion = &b
			}
		}
	}
	n, errN := strconv.Atoi(m[1])
	cpu, errC := strconv.Atoi(m[2])
	ram, errR := strconv.Atoi(m[3])
	if err := errors.Join(errN, errC, errR); err != nil {
		return nil, usageError(help, "%s: node count, CPU and RAM must be whole numbers (got %q): %v", where, parts[1], err)
	}
	return capellafile.Obj{
		"node": capellafile.Obj{
			"compute": capellafile.Obj{"cpu": int64(cpu), "ram": int64(ram)},
			"disk":    diskToFile(makeDisk(provider, disk.diskType, disk.size, disk.iops, disk.autoExpansion)),
		},
		"numOfNodes": int64(n),
		"services":   toAnyList(services),
	}, nil
}

// pickFileProject chooses the project of a new cluster: --project, else the file's only project.
// With several projects and a terminal it asks (as capellactl did) and records the answer as
// --project, so later calls for the same invocation pick the same project.
func pickFileProject(cmd *cobra.Command, ed *capellafile.Editor) (string, *yaml.Node, error) {
	name := projectFlag(cmd)
	if !cmd.Flags().Changed("project") {
		names := ed.ProjectNames()
		switch len(names) {
		case 0:
			return "", nil, usageError("create one with 'cbctl capella project create NAME'", "%s declares no project", fileName(ed))
		case 1:
			name = names[0]
		default:
			if !canPrompt(cmd) {
				return "", nil, usageError("add --project NAME ("+strings.Join(names, ", ")+")", "%s declares several projects", fileName(ed))
			}
			picked, err := askProject(promptOf(cmd), names)
			if err != nil {
				return "", nil, err
			}
			if err := cmd.Flags().Set("project", picked); err != nil {
				return "", nil, fmt.Errorf("recording the project: %w", err)
			}
			name = picked
		}
	}
	node, ok := ed.FindProject(name)
	if !ok {
		return "", nil, projectNotInFile(ed, name, func(p string) string { return "--project " + p })
	}
	return name, node, nil
}

// fileCIDRs lists the network CIDRs of every cluster in the file.
func fileCIDRs(ed *capellafile.Editor) []string {
	var out []string
	for _, p := range ed.ProjectNames() {
		pn, _ := ed.FindProject(p)
		for _, c := range ed.ClusterNodes(pn) {
			if cp := childMap(ed, c, "cloudProvider"); cp != nil {
				if v := ed.Child(cp, "cidr"); v != nil {
					out = append(out, v.Value)
				}
			}
		}
	}
	return out
}

// wizardAnswerFlags are the cluster create flags the wizard fills in; on a retry the ones the
// user did not type are cleared and asked again.
var wizardAnswerFlags = []string{"provider", "region", "size", "group", "cidr", "support-plan", "nodes", "cpu",
	"ram", "disk-type", "disk-size", "disk-iops"}

// wizardRunning marks cmd while wizardClusterCreate builds the mutation, so the nested
// managedClusterCreate uses the answers instead of starting the wizard again.
const wizardRunning = "cbctl.cluster-wizard"

// wizardClusterCreate runs the cluster create wizard w and builds the mutation from its answers.
// When the answers together make capella.yaml invalid (for example a single-node size with
// --availability multi), it shows the problems and asks the provider, size and the rest again,
// keeping the name and type, as capellactl did, at most wizardTries times; the last invalid
// answer is reported by the usual validation.
func wizardClusterCreate(cmd *cobra.Command, args []string, e *envRuntime, ed *capellafile.Editor, w *clusterWizard,
	name, cidr string) (Mutation, error) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[wizardRunning] = "1"
	defer delete(cmd.Annotations, wizardRunning)
	given := map[string]bool{}
	for _, f := range wizardAnswerFlags {
		given[f] = cmd.Flags().Changed(f)
	}
	spec, err := w.run(cmd, name, cidr)
	if err != nil {
		return Mutation{}, err
	}
	for attempt := 1; ; attempt++ {
		if err := spec.apply(cmd); err != nil {
			return Mutation{}, err
		}
		m, err := managedClusterCreate(cmd, args, e, ed)
		if err != nil || attempt >= wizardTries {
			return m, err
		}
		var invalid *CLIError
		if verr := trialEdit(cmd, ed, m); !errors.As(verr, &invalid) || invalid.Code != ExitConfigInvalid {
			return m, nil
		}
		printError(invalid.Message)
		printAction("Please try again: choose the cloud provider and size again.")
		if err := clearFlags(cmd, wizardAnswerFlags, given); err != nil {
			return Mutation{}, err
		}
		if spec, err = w.details(cmd, spec, cidr); err != nil {
			return Mutation{}, err
		}
	}
}

// trialEdit applies m to a fresh copy of the file and validates the result, writing nothing.
func trialEdit(cmd *cobra.Command, ed *capellafile.Editor, m Mutation) error {
	trial, err := capellafile.OpenEditor(ed.Path)
	if err != nil {
		return nil // the real edit reports it
	}
	if err := m.Edit(trial); err != nil {
		return err
	}
	text, err := trial.Text()
	if err != nil {
		return nil // the real edit reports it
	}
	_, err = validateCandidateEnv(trial, text, envFrom(cmd))
	return err
}

// clearFlags resets the named flags the user did not give to their defaults and unset state.
func clearFlags(cmd *cobra.Command, names []string, given map[string]bool) error {
	for _, n := range names {
		f := cmd.Flags().Lookup(n)
		if f == nil || given[n] {
			continue
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			if err := sv.Replace(nil); err != nil {
				return fmt.Errorf("resetting --%s: %w", n, err)
			}
		} else if err := f.Value.Set(f.DefValue); err != nil {
			return fmt.Errorf("resetting --%s: %w", n, err)
		}
		f.Changed = false
	}
	return nil
}

func managedClusterCreate(cmd *cobra.Command, args []string, e *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	ch := cmd.Flags().Changed
	name, err := resolveClusterName(cmd, args, "name")
	if err != nil {
		return Mutation{}, err
	}
	// Capella allows one free-tier cluster per organization: the wizard does not offer it once it
	// is used, and --free-tier is refused before anything is written when it is used.
	freeUsedBy, freeKnown := "", false
	lookupFree := func() {
		if freeKnown || e == nil || e.runtime == nil || e.Client == nil {
			return
		}
		if u, err := e.freeTierUsedBy(); err == nil {
			freeUsedBy, freeKnown = u, true
		}
	}
	// With a real capella.yaml, check it and what Capella runs before asking anything, as
	// capellactl did: a file that is already invalid stops here, and the name and network are
	// checked against the clusters only Capella knows.
	var networks *capellaNetworks
	fileProject := ""
	if e != nil && !e.Ephemeral {
		if _, err := validateCandidateEnv(ed, ed.Original, envFrom(e.cmd)); err != nil {
			return Mutation{}, err
		}
		if p, _, err := pickFileProject(cmd, ed); err == nil {
			fileProject = p
			if networks, err = readCapellaNetworks(e, ed, p); err != nil {
				return Mutation{}, err
			}
		}
	}
	// On a terminal, capellactl asks for what the flags leave open: everything when NAME is
	// missing, and the type, size and the rest when NAME is given but nothing sizes the cluster.
	if e != nil && !e.Ephemeral && cmd.Annotations[wizardRunning] == "" &&
		(name == "" || !sizedByFlags(cmd) && !flagBool(cmd, "free-tier")) {
		if _, tty := e.terminalIn(); tty {
			lookupFree()
			w := newClusterWizard(shareStdin(cmd), stderrFrom(cmd))
			w.noFree = freeUsedBy != ""
			w.cidrCheck = wizardCIDRCheck(fileCIDRs(ed), networks)
			return wizardClusterCreate(cmd, args, e, ed, w, name, suggestCIDR(networks.used(fileCIDRs(ed))))
		}
	}
	if name == "" {
		return Mutation{}, usageError("pass NAME or --name NAME", "the cluster name is required")
	}
	groups := flagStrings(cmd, "group")
	single := flagBool(cmd, "single-node")
	free := flagBool(cmd, "free-tier")
	if free {
		lookupFree()
		if freeUsedBy != "" {
			return Mutation{}, freeTierInUse(freeUsedBy)
		}
	}
	size := flagString(cmd, "size")
	if len(groups) > 0 {
		var given []string
		for _, f := range []string{"nodes", "cpu", "ram", "services", "size"} {
			if ch(f) {
				given = append(given, "--"+f)
			}
		}
		if len(given) > 0 {
			return Mutation{}, usageError("remove "+strings.Join(given, ", ")+", or use them without --group",
				"--group sets nodes, size and services per group, so %s cannot be given with it", strings.Join(given, ", "))
		}
	}
	if single {
		switch {
		case size != "":
			return Mutation{}, usageError("remove --size", "--single-node sets the size")
		case ch("nodes") && flagInt(cmd, "nodes") != 1:
			return Mutation{}, usageError("remove --nodes", "singleNode clusters have exactly 1 node")
		case flagString(cmd, "availability") == "multi":
			return Mutation{}, usageError("remove --availability multi, or leave out --single-node", "singleNode clusters must use availability: single")
		case len(groups) > 1:
			return Mutation{}, usageError("give one --group, or leave out --single-node", "singleNode clusters have exactly one service group")
		}
	}
	if v := flagString(cmd, "server-version"); ch("server-version") && !versionShape.MatchString(v) {
		return Mutation{}, usageError("give a version like 8.0 or 7.6.4, or leave out --server-version", "--server-version takes a version like 8.0 or 7.6.4 (got %q)", v)
	}
	provider := flagString(cmd, "provider")
	if provider == "" {
		provider = "aws"
	}
	if !slices.Contains(cloudProviders, provider) {
		return Mutation{}, usageError("use one of "+strings.Join(cloudProviders, ", "), "unknown provider %q", provider)
	}
	if free && (len(groups) > 0 || single || size != "" || ch("nodes") || ch("cpu") || ch("ram") || ch("services") ||
		ch("availability") || ch("support-plan") || ch("disk-type") || ch("disk-size") || ch("disk-iops")) {
		return Mutation{}, usageError("remove the size, disk, services, availability and support options, or create a provisioned cluster without --free-tier",
			"free-tier clusters have a fixed size and plan")
	}
	supportPlan := strings.ToLower(flagString(cmd, "support-plan"))
	if supportPlan == "" {
		supportPlan = "developer pro"
	}
	if !slices.Contains(supportPlans, supportPlan) {
		return Mutation{}, usageError("use one of "+strings.Join(supportPlans, ", "), "unknown support plan %q", supportPlan)
	}
	disk := diskChangeOf(cmd)
	var layout []any
	if !free {
		for _, g := range groups {
			obj, err := parseGroupSpec(g, provider, disk)
			if err != nil {
				return Mutation{}, err
			}
			layout = append(layout, obj)
		}
		layout = dataFirst(layout)
	}
	preset, err := sizePreset(size, single, len(groups) > 0)
	if err != nil {
		return Mutation{}, err
	}

	project, projNode, err := pickFileProject(cmd, ed)
	if err != nil {
		return Mutation{}, err
	}
	replace := false
	if dup := hasClusterNamed(ed, projNode, name); dup {
		if !undeployedEntry(ed, project, name, networks) {
			return Mutation{}, blockedError("change it with 'cbctl capella cluster update' or 'cbctl capella cluster scale'",
				"cluster %s already exists in %s", name, fileName(ed))
		}
		replace = true
	} else if networks != nil && networks.names[name] && project == fileProject {
		return Mutation{}, blockedError("run 'cbctl capella sync' to bring it into "+fileName(ed)+", then change it with 'cbctl capella cluster update'",
			"cluster %s already exists in project %s in Capella", name, project)
	}
	fileNets := fileCIDRs(ed)
	if replace {
		// The entry being replaced gives its range back.
		if _, old, _ := ed.FindCluster(name, project); old != nil {
			if cp := childMap(ed, old, "cloudProvider"); cp != nil {
				if v := ed.Child(cp, "cidr"); v != nil {
					fileNets = slices.DeleteFunc(fileNets, func(c string) bool { return c == v.Value })
				}
			}
		}
	}
	used := networks.used(fileNets)
	cidr := flagString(cmd, "cidr")
	if cidr == "" {
		cidr = suggestCIDR(used)
	} else if networks != nil {
		if err := networks.overlapError(cidr, suggestCIDR(used)); err != nil {
			return Mutation{}, err
		}
	}
	if other, ok := networks.elsewhereOverlap(cidr); ok && cmd.Flags().Changed("cidr") {
		printNotice(stderrFrom(cmd), fmt.Sprintf("%s overlaps the network of cluster %s", cidr, other),
			"none needed unless you will connect the two networks")
	}
	if replace {
		printNotice(stderrFrom(cmd), fmt.Sprintf("%s is in %s but was never deployed to Capella; replacing it", name, fileName(ed)), "")
	}
	region := flagString(cmd, "region")
	if region == "" {
		region = regionDefaults[provider]
		if free {
			region = freeTierRegions[provider][0]
		}
	}

	cluster := capellafile.Obj{"name": name}
	if d := flagString(cmd, "description"); d != "" {
		cluster["description"] = d
	}
	cluster["cloudProvider"] = capellafile.Obj{"type": provider, "region": region, "cidr": cidr}
	var summary string
	var after []string
	if free {
		cluster["tier"] = "free"
		summary = "free tier, network " + cidr
	} else {
		if layout == nil {
			nodes, cpu, ram := preset.NumOfNodes, preset.CPU, preset.RAM
			if ch("nodes") {
				nodes = flagInt(cmd, "nodes")
			}
			if ch("cpu") {
				cpu = flagInt(cmd, "cpu")
			}
			if ch("ram") {
				ram = flagInt(cmd, "ram")
			}
			svc := preset.Services
			if s := flagString(cmd, "services"); s != "" {
				if svc, err = parseServices(s, "--services"); err != nil {
					return Mutation{}, err
				}
			}
			svc = append([]string(nil), svc...)
			sort.Strings(svc)
			layout = []any{capellafile.Obj{
				"node": capellafile.Obj{
					"compute": capellafile.Obj{"cpu": int64(cpu), "ram": int64(ram)},
					"disk":    diskToFile(makeDisk(provider, disk.diskType, disk.size, disk.iops, disk.autoExpansion)),
				},
				"numOfNodes": int64(nodes),
				"services":   toAnyList(svc),
			}}
		}
		if preset.ConfigurationType == "singleNode" {
			cluster["configurationType"] = "singleNode"
		}
		if v := flagString(cmd, "server-version"); v != "" {
			cluster["couchbaseServer"] = capellafile.Obj{"version": v}
		}
		avail := flagString(cmd, "availability")
		if avail == "" {
			avail = preset.Availability
		}
		cluster["availability"] = capellafile.Obj{"type": avail}
		tz := flagString(cmd, "support-timezone")
		if tz == "" {
			tz = "PT"
		}
		cluster["support"] = capellafile.Obj{"plan": supportPlan, "timezone": tz}
		cluster["serviceGroups"] = layout
		if z := flagString(cmd, "zone"); z != "" {
			cluster["zones"] = []any{z}
		}
		if id := flagString(cmd, "cmek-id"); id != "" {
			cluster["cmekId"] = id
		}
		if flagBool(cmd, "private-dns") {
			cluster["enablePrivateDNSResolution"] = true
		}
		total := int64(0)
		var parts []string
		var warnings []string
		offered, known := presets.ComputeSizes()[provider]
		for _, g := range layout {
			obj := g.(capellafile.Obj)
			n := obj["numOfNodes"].(int64)
			total += n
			comp := obj["node"].(capellafile.Obj)["compute"].(capellafile.Obj)
			cpu, ram := comp["cpu"].(int64), comp["ram"].(int64)
			key := joinAny(obj["services"])
			parts = append(parts, fmt.Sprintf("%s %d × %d/%d", key, n, cpu, ram))
			if known && !offered.Offered(presets.Size{CPU: int(cpu), RAM: int(ram)}, preset.ConfigurationType == "singleNode") {
				warnings = append(warnings, fmt.Sprintf("! serviceGroups[%s]: %d vCPU/%d GB is not a known %s size; Capella may reject it (see 'cbctl capella cluster options')",
					key, cpu, ram, provider))
			}
		}
		if len(layout) > 1 {
			summary = fmt.Sprintf("%d groups · %d nodes (%s)", len(layout), total, strings.Join(parts, " · "))
		} else {
			comp := layout[0].(capellafile.Obj)["node"].(capellafile.Obj)["compute"].(capellafile.Obj)
			summary = fmt.Sprintf("%d × %d vCPU/%d GB", total, comp["cpu"], comp["ram"])
		}
		summary += fmt.Sprintf(", network %s, support %s", cidr, supportPlan)
		after = append(after, warnings...)
	}
	after = append([]string{fmt.Sprintf("New cluster %s in project %s: %s %s, %s", name, project, provider, region, summary)}, after...)
	return Mutation{
		Target:     "cluster/" + name,
		Question:   fmt.Sprintf("Add %s to %s and deploy it now?", name, fileName(ed)),
		AfterPlan:  after,
		AfterApply: func(p *plan.Plan) { recordServerVersion(e, p, project, name) },
		Edit: func(ed *capellafile.Editor) error {
			_, pn, err := pickFileProject(cmd, ed)
			if err != nil {
				return err
			}
			if replace {
				return replaceUndeployed(ed, project, cluster)
			}
			ed.AddCluster(pn, cluster)
			return nil
		},
	}, nil
}

// dataGroupDisk is the disk of cl's data group (the default of a new group's disk), or nil.
func dataGroupDisk(ed *capellafile.Editor, cl *yaml.Node, provider string) *capellaapi.Disk {
	dg, err := ed.Group(cl, "")
	if err != nil {
		return nil
	}
	src, err := ed.Decode(dg)
	if err != nil {
		return nil
	}
	node, _ := src["node"].(map[string]any)
	d, _ := node["disk"].(map[string]any)
	if d == nil {
		return nil
	}
	num := func(k string) *int {
		if v, ok := d[k].(int64); ok {
			return intPtr(int(v))
		}
		return nil
	}
	t, _ := d["type"].(string)
	if t == "" {
		return makeDisk(provider, "", num("storage"), num("iops"), nil)
	}
	return &capellaapi.Disk{Type: t, Storage: num("storage"), IOPS: num("iops")}
}

// intOrNil is &n, or nil when n is 0 (not set).
func intOrNil(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

// joinAny joins a YAML list of services with commas.
func joinAny(v any) string {
	list, _ := v.([]any)
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = fmt.Sprint(s)
	}
	return strings.Join(out, ",")
}

// dataFirst puts the first group that runs data first, so the 3-27 node rule of the first group
// applies to it.
func dataFirst(groups []any) []any {
	for i, g := range groups {
		svc, _ := g.(capellafile.Obj)["services"].([]any)
		if slices.Contains(svc, any("data")) {
			if i == 0 {
				return groups
			}
			out := []any{g}
			out = append(out, groups[:i]...)
			return append(out, groups[i+1:]...)
		}
	}
	return groups
}

// sizePreset resolves --size (default small, dev for --single-node).
func sizePreset(size string, single, grouped bool) (presets.Preset, error) {
	all := presets.Presets()
	switch {
	case single:
		return presets.Preset{ConfigurationType: "singleNode", Availability: "single", NumOfNodes: 1, CPU: 4, RAM: 16,
			Services: []string{"data", "index", "query"}}, nil
	case size == "custom":
		return presets.Preset{ConfigurationType: "multiNode", Availability: "multi", NumOfNodes: 3, CPU: 4, RAM: 16,
			Services: []string{"data", "index", "query"}}, nil
	case size == "" && grouped:
		return presets.Preset{ConfigurationType: "multiNode", Availability: "multi"}, nil
	case size == "":
		size = "small"
	}
	p, ok := all[size]
	if !ok {
		return presets.Preset{}, usageError("use one of "+strings.Join(append(presets.PresetNames(), "custom"), ", "), "unknown size %q", size)
	}
	return p, nil
}

func managedClusterClone(cmd *cobra.Command, args []string, e *envRuntime, ed *capellafile.Editor) (Mutation, error) {
	source, name := args[0], args[1]
	region := strings.TrimSpace(flagString(cmd, "region"))
	if region == "" {
		return Mutation{}, usageError("pass --region REGION", "the region of the clone is required")
	}
	ch := cmd.Flags().Changed
	supportPlan := ""
	if ch("support-plan") {
		supportPlan = strings.ToLower(strings.TrimSpace(flagString(cmd, "support-plan")))
		if !slices.Contains(supportPlans, supportPlan) {
			return Mutation{}, usageError("use one of "+strings.Join(supportPlans, ", "), "unknown support plan %q", supportPlan)
		}
	}
	return Mutation{
		Target:    "cluster/" + name,
		Question:  fmt.Sprintf("Add %s (a copy of %s) to %s and deploy it now?", name, source, fileName(ed)),
		AfterPlan: []string{"not copied: " + strings.Join(managedNotCopied(cmd), ", ")},
		Edit: func(ed *capellafile.Editor) error {
			srcProj, src, err := ed.FindCluster(source, projectFlag(cmd))
			var amb *capellafile.AmbiguousError
			if asAmbiguous(err, &amb) {
				return usageError("add --project NAME", "%v", err)
			}
			if err != nil {
				return err
			}
			if src == nil {
				return notInFile("cluster", source, fileName(ed))
			}
			obj, err := ed.Decode(src)
			if err != nil {
				return fmt.Errorf("reading cluster %s: %w", source, err)
			}
			if tier, _ := obj["tier"].(string); tier == "free" {
				return blockedError("create a provisioned cluster with 'cbctl capella cluster create' instead",
					"free-tier clusters cannot be cloned (one per organization)")
			}
			target := srcProj
			if to := flagString(cmd, "to-project"); to != "" {
				node, ok := ed.FindProject(to)
				if !ok {
					return usageError(fmt.Sprintf("create it first with 'cbctl capella project create %s'", to), "project %s does not exist", to)
				}
				target = node
			}
			if hasClusterNamed(ed, target, name) {
				return blockedError("choose another name for the copy", "cluster %s already exists in %s", name, fileName(ed))
			}
			cidr := strings.TrimSpace(flagString(cmd, "cidr"))
			if cidr == "" {
				cidr = suggestCIDR(fileCIDRs(ed))
			}
			clone := capellafile.Obj{"name": name}
			srcRegion := ""
			if cp, ok := obj["cloudProvider"].(map[string]any); ok {
				srcRegion, _ = cp["region"].(string)
				clone["cloudProvider"] = capellafile.Obj{"type": cp["type"], "region": region, "cidr": cidr}
			}
			desc := flagString(cmd, "description")
			if desc == "" {
				desc = fmt.Sprintf("Clone of %s (%s)", source, srcRegion)
			}
			clone["description"] = desc
			for _, k := range []string{"configurationType", "availability", "support", "serviceGroups", "deletionProtection"} {
				if v, ok := obj[k]; ok {
					clone[k] = v
				}
			}
			if supportPlan != "" {
				sup, _ := clone["support"].(map[string]any)
				if sup == nil {
					sup = map[string]any{}
				}
				sup["plan"] = supportPlan
				clone["support"] = sup
			}
			switch v := flagString(cmd, "server-version"); {
			case v == "latest":
			case v != "":
				clone["couchbaseServer"] = capellafile.Obj{"version": v}
			default:
				if cs, ok := obj["couchbaseServer"]; ok {
					clone["couchbaseServer"] = cs
				}
			}
			if !flagBool(cmd, "no-data-model") {
				if b, ok := obj["buckets"]; ok {
					clone["buckets"] = b
				}
			}
			if flagBool(cmd, "with-cidrs") {
				if c, ok := obj["allowedCidrs"]; ok {
					clone["allowedCidrs"] = c
				}
				if v, ok := obj["allowAnyIp"]; ok {
					clone["allowAnyIp"] = v
				}
			}
			if flagBool(cmd, "with-credentials") {
				if c, ok := obj["databaseCredentials"]; ok {
					clone["databaseCredentials"] = c
				}
			}
			ed.AddCluster(target, clone)
			return nil
		},
	}, nil
}

// managedNotCopied is what a file-managed clone leaves behind, in the order the direct clone
// reports it (notCopied). Region-bound settings (zones, CMEK, private DNS) are never written.
func managedNotCopied(cmd *cobra.Command) []string {
	out := []string{"documents", "on/off schedule"}
	if flagBool(cmd, "no-data-model") {
		out = append([]string{"data model"}, out...)
	}
	if !flagBool(cmd, "with-cidrs") {
		out = append(out, "allowed CIDRs")
	}
	if !flagBool(cmd, "with-credentials") {
		out = append(out, "database credentials")
	}
	return out
}

// hasClusterNamed reports whether project declares a cluster with this name.
func hasClusterNamed(ed *capellafile.Editor, project *yaml.Node, name string) bool {
	for _, c := range ed.ClusterNodes(project) {
		if v := ed.Child(c, "name"); v != nil && v.Value == name {
			return true
		}
	}
	return false
}
