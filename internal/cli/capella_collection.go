package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/resources"
	"github.com/spf13/cobra"
)

// collectionDoc is the JSON document of collection add, update and delete.
type collectionDoc struct {
	Cluster    string `json:"cluster"`
	Bucket     string `json:"bucket"`
	Scope      string `json:"scope"`
	Collection string `json:"collection"`
	MaxTTL     *int   `json:"maxTTL,omitempty"`
	Status     string `json:"status"`
}

const maxTTLHelp = "Maximum TTL in seconds: -1 never expires, 0 uses the bucket's TTL"

// collectionMaxTTLVersion is the first Couchbase Server version that can change a collection's maxTTL.
var collectionMaxTTLVersion = [3]int{7, 6, 0}

var versionDigits = regexp.MustCompile(`\d+`)

// parseServerVersion reads "7.6.2" or "7.2.4-build" into three numbers; ok is false when the text
// has no digits.
func parseServerVersion(text string) (v [3]int, ok bool) {
	nums := versionDigits.FindAllString(strings.SplitN(text, "-", 2)[0], 3)
	if len(nums) == 0 {
		return v, false
	}
	for i, n := range nums {
		v[i], _ = strconv.Atoi(n) // digits only: cannot fail
	}
	return v, true
}

// versionBefore reports whether a is older than b.
func versionBefore(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func newCapellaCollectionListCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list CLUSTER BUCKET SCOPE",
		Short: "List the collections of a scope",
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
		pid, cid := t.ids()
		colls, err := rt.Client.ListCollections(rt.Ctx, pid, cid, t.bucketID(), args[2])
		if err != nil {
			return notFoundOr(err, "scope", args[2], scopeAction(t), "listing collections of "+args[2])
		}
		if rt.JSON {
			rt.Out.List(colls)
			return nil
		}
		rows := make([][]string, 0, len(colls))
		for _, c := range colls {
			rows = append(rows, []string{c.Name, strconv.Itoa(c.MaxTTL)})
		}
		rt.Out.Table([]string{"NAME", "MAX_TTL"}, rows)
		return nil
	}
	return cmd
}

// scopeAction is the hint for an unknown scope.
func scopeAction(t *bucketTarget) string {
	return fmt.Sprintf("check the name with 'cbctl capella scope list %s %s'", t.Cluster.Name, t.Bucket.Name)
}

func newCapellaCollectionAddCmd() *cobra.Command {
	var (
		project string
		maxTTL  int
	)
	cmd := &cobra.Command{
		Use:   "add CLUSTER BUCKET SCOPE NAME",
		Short: "Add a collection to a scope (no-op when it exists)",
		Args:  cobra.ExactArgs(4),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.Flags().IntVar(&maxTTL, "max-ttl", 0, maxTTLHelp)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		var ttl *int
		if cmd.Flags().Changed("max-ttl") {
			if maxTTL < -1 {
				return usageError("use -1, 0 or a number of seconds", "invalid --max-ttl %d", maxTTL)
			}
			ttl = &maxTTL
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateBucket(args[0], args[1], project)
		if err != nil {
			return err
		}
		scope, name := args[2], args[3]
		pid, cid := t.ids()
		doc := collectionDoc{Cluster: t.Cluster.Name, Bucket: t.Bucket.Name, Scope: scope, Collection: name, MaxTTL: ttl}
		where := fmt.Sprintf("%s/%s/%s", t.Cluster.Name, t.Bucket.Name, scope)

		exists := func() (bool, error) {
			colls, err := rt.Client.ListCollections(rt.Ctx, pid, cid, t.bucketID(), scope)
			if err != nil {
				return false, notFoundOr(err, "scope", scope, scopeAction(t), "listing collections of "+scope)
			}
			for _, c := range colls {
				if c.Name == name {
					return true, nil
				}
			}
			return false, nil
		}
		if ok, err := exists(); err != nil {
			return err
		} else if ok {
			doc.Status = "already exists"
			rt.report(doc, "collection %s already exists in %s", name, where)
			return nil
		}

		err = rt.Client.CreateCollection(rt.Ctx, pid, cid, t.bucketID(), scope, name, ttl)
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
			return fmt.Errorf("creating collection %s: %w", name, err)
		}
		if doc.Status == "created" {
			rt.report(doc, "created collection %s in %s", name, where)
		} else {
			rt.report(doc, "collection %s already exists in %s", name, where)
		}
		return nil
	}
	return cmd
}

func newCapellaCollectionUpdateCmd() *cobra.Command {
	var (
		project string
		maxTTL  int
	)
	cmd := &cobra.Command{
		Use:   "update CLUSTER BUCKET SCOPE NAME",
		Short: "Change a collection's maxTTL (Couchbase Server 7.6.0 or later)",
		Args:  cobra.ExactArgs(4),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.Flags().IntVar(&maxTTL, "max-ttl", 0, maxTTLHelp)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("max-ttl") {
			return usageError("pass --max-ttl SECONDS", "nothing to update")
		}
		if maxTTL < -1 {
			return usageError("use -1, 0 or a number of seconds", "invalid --max-ttl %d", maxTTL)
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateBucket(args[0], args[1], project)
		if err != nil {
			return err
		}
		scope, name := args[2], args[3]
		if t.Cluster.CouchbaseServer != nil {
			if v, ok := parseServerVersion(t.Cluster.CouchbaseServer.Version); ok && versionBefore(v, collectionMaxTTLVersion) {
				return Errorf(ExitBlocked, "upgrade the cluster first", "requires Couchbase Server 7.6.0 or later (cluster %s runs %s)",
					t.Cluster.Name, t.Cluster.CouchbaseServer.Version)
			}
		} else {
			logging.Debug("server version of %s is unknown; not checking the 7.6.0 requirement", t.Cluster.Name)
		}
		pid, cid := t.ids()
		action := fmt.Sprintf("check the name with 'cbctl capella collection list %s %s %s'", t.Cluster.Name, t.Bucket.Name, scope)
		for attempt := 0; ; attempt++ {
			_, etag, err := rt.Client.GetCollection(rt.Ctx, pid, cid, t.bucketID(), scope, name)
			if err != nil {
				return notFoundOr(err, "collection", name, action, "reading collection "+name)
			}
			err = rt.Client.UpdateCollection(rt.Ctx, pid, cid, t.bucketID(), scope, name, maxTTL, etag)
			if errors.Is(err, capellaapi.ErrPreconditionFailed) && attempt == 0 {
				logging.Debug("collection %s changed while updating; retrying", name)
				continue
			}
			if err != nil {
				return notFoundOr(err, "collection", name, action, "updating collection "+name)
			}
			break
		}
		rt.report(collectionDoc{Cluster: t.Cluster.Name, Bucket: t.Bucket.Name, Scope: scope, Collection: name, MaxTTL: &maxTTL, Status: "updated"},
			"updated collection %s in %s/%s/%s", name, t.Cluster.Name, t.Bucket.Name, scope)
		return nil
	}
	return cmd
}

func newCapellaCollectionDeleteCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "delete CLUSTER BUCKET SCOPE NAME",
		Short: "Delete a collection and its documents",
		Args:  cobra.ExactArgs(4),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		scope, name := args[2], args[3]
		if isSystemCollection(scope, name) {
			return usageError("system collections are managed by Capella", "collection %s.%s cannot be deleted", scope, name)
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
		key := t.Cluster.Name + "/" + t.Bucket.Name + "/" + scope + "/" + name
		if _, _, err := rt.Client.GetCollection(rt.Ctx, pid, cid, t.bucketID(), scope, name); err != nil {
			if errors.Is(err, capellaapi.ErrNotFound) {
				rt.report(collectionDoc{Cluster: t.Cluster.Name, Bucket: t.Bucket.Name, Scope: scope, Collection: name, Status: "not found"},
					"collection %s not found, nothing to do", key)
				return nil
			}
			return fmt.Errorf("reading collection %s: %w", name, err)
		}
		details, derr := resources.KeyspaceDeleteDetails(rt.Ctx, rt.Client, pid, cid, t.Bucket.Name, t.bucketID(), scope, name)
		if derr != nil {
			details = nil // informational only
		}
		if ok, err := rt.confirmNamedDelete("collection", key, name, details); err != nil || !ok {
			return err
		}
		if err := rt.Client.DeleteCollection(rt.Ctx, pid, cid, t.bucketID(), scope, name); err != nil {
			action := fmt.Sprintf("check the name with 'cbctl capella collection list %s %s %s'", t.Cluster.Name, t.Bucket.Name, scope)
			return notFoundOr(err, "collection", name, action, "deleting collection "+name)
		}
		rt.report(collectionDoc{Cluster: t.Cluster.Name, Bucket: t.Bucket.Name, Scope: scope, Collection: name, Status: "deleted"},
			"deleted collection %s from %s/%s/%s", name, t.Cluster.Name, t.Bucket.Name, scope)
		return nil
	}
	return cmd
}
