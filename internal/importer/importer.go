// Package importer builds a capella.yaml document from what exists in Capella. It is read-only
// towards Capella (GET requests only). Each exporter is the inverse of a reconciler's
// comparison, so a plan straight after an import reports no changes. Passwords are never
// exported (Capella never returns them) and the API key is never written.
package importer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"gopkg.in/yaml.v3"
)

// Notice is a warning with its next step.
type Notice = capellafile.Notice

// APIVersion is what files written by cbctl say.
const APIVersion = "cbctl/v1"

// RetrySync is the next step after a read that failed.
const RetrySync = "Run 'cbctl capella sync' again later; capella.yaml keeps what it had until then."

// BackupRoleWarning is shown when backup schedules cannot be read for lack of a role.
var BackupRoleWarning = Notice{
	What: "backup schedules not read: the API key does not have the Organization Owner or Project Owner role, " +
		"so capella.yaml keeps the ones it has",
	Action: "Give the API key one of those roles in the Capella UI, then run 'cbctl capella sync'.",
}

// CommentPrefix starts the keys that carry a comment instead of data: a key `__comment_N__` in a
// mapping puts its text (see Result.Comments) above the next key; a list item that is only such a
// key becomes a commented-out entry. Render turns them into YAML comments.
const (
	commentPrefix = "__comment_"
	blockPrefix   = "__block_"
)

// IsMarker reports whether key is a comment marker.
func IsMarker(key string) bool {
	return strings.HasPrefix(key, commentPrefix) || strings.HasPrefix(key, blockPrefix)
}

// UnreadInfo records the parts of one cluster that Capella could not return now (powered off, or
// an error), so sync keeps them from the previous file instead of dropping them. "scopes:B" is the
// scopes of bucket B, "bucketBackup:B" the backup schedule of bucket B.
type UnreadInfo struct {
	Name   string
	Reason string
	Fields []string
	Kept   bool
}

// Result is the document built from Capella.
type Result struct {
	Data     Obj
	Comments map[string]string
	Warnings []Notice
	Counts   map[string]int
	Unread   map[string]*UnreadInfo
}

// Options tune BuildDocument.
type Options struct {
	// Full writes every field, including values equal to the defaults.
	Full bool
	// PinIDs writes the id of every provisioned cluster (free clusters are always pinned).
	PinIDs bool
	// ClusterNames and ClusterIDs limit the export to these clusters.
	ClusterNames, ClusterIDs []string
	// KnownAccess returns the access (and password reference) the current file records for a
	// credential that has no access in Capella, so apply can restore it.
	KnownAccess func(cluster, credential string) (access any, password string, ok bool)
}

type idGen struct{ n int }

func (g *idGen) next(kind string) string {
	g.n++
	return fmt.Sprintf("__%s_%d__", kind, g.n)
}

// markerID is shared across projects so keys never collide when documents are combined.
var markerSeq idGen

func normObj(o Obj) Obj {
	if n, ok := capellafile.NormalizeNumbers(o).(Obj); ok {
		return n
	}
	return o
}

func apiErr(err error) (*capellaapi.APIError, bool) {
	var api *capellaapi.APIError
	if errors.As(err, &api) {
		return api, true
	}
	return nil, false
}

func rawList(ctx context.Context, c *capellaapi.Client, projectID, clusterID, suffix, key string) ([]Obj, error) {
	org, err := c.OrgPath()
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("%s/projects/%s/clusters/%s%s", org, url.PathEscape(projectID), url.PathEscape(clusterID), suffix)
	items, err := capellaapi.Paginate[Obj](ctx, c, path, key, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Obj, len(items))
	for i, it := range items {
		out[i] = normObj(it)
	}
	return out, nil
}

func clusterIsFree(c capellaapi.Cluster) bool {
	return c.Support != nil && strings.EqualFold(c.Support.Plan, "free")
}

// BuildProject reads everything below project for the given clusters and returns the document
// (single-project form). allClusters (every cluster in the project) decides which names are
// ambiguous and need an id: pin, even when only some clusters are exported.
func BuildProject(ctx context.Context, client *capellaapi.Client, project capellaapi.Project,
	clusters, allClusters []capellaapi.Cluster, opts Options) (*Result, error) {
	res := &Result{
		Comments: map[string]string{},
		Counts:   map[string]int{},
		Unread:   map[string]*UnreadInfo{},
	}
	counts := res.Counts
	for _, k := range []string{"cluster", "bucket", "scope", "collection", "credential", "allowed CIDR", "hibernation schedule"} {
		counts[k] = 0
	}
	pid := project.ID
	nameCounts := map[string]int{}
	if allClusters == nil {
		allClusters = clusters
	}
	for _, c := range allClusters {
		nameCounts[c.Name]++
	}

	var exported []any
	for _, c := range clusters {
		cid := c.ID
		free := clusterIsFree(c)
		var body Obj
		if free {
			o, _, err := client.GetFreeCluster(ctx, pid, cid)
			if err != nil {
				return nil, fmt.Errorf("reading cluster %s: %w", c.Name, err)
			}
			body = normObj(o)
		} else {
			cl, err := client.GetCluster(ctx, pid, cid)
			if err != nil {
				return nil, fmt.Errorf("reading cluster %s: %w", c.Name, err)
			}
			body = toObj(cl)
		}
		name := str(body, "name")
		if n := nameCounts[name]; n > 1 {
			msg := Notice{
				What: fmt.Sprintf("%d clusters are named %s; capella.yaml tells them apart by id", n, name),
				Action: "None needed. To avoid confusion, give one of them another name in the Capella UI, " +
					"then run 'cbctl capella sync'.",
			}
			if !hasNotice(res.Warnings, msg) {
				res.Warnings = append(res.Warnings, msg)
			}
		}
		cl := ExportCluster(body, opts.Full, true)
		counts["cluster"]++
		state := str(body, "currentState")

		notRead := func(key, reason string) {
			info := res.Unread[cid]
			if info == nil {
				info = &UnreadInfo{Name: name, Reason: reason}
				res.Unread[cid] = info
			}
			info.Fields = append(info.Fields, key)
		}
		comment := func(text string) {
			key := markerSeq.next("comment")
			cl[key] = ""
			res.Comments[key] = text
		}
		// guarded runs a read and turns an API error into a warning plus a note in the file.
		guarded := func(what, key string, fn func() error) (bool, error) {
			err := fn()
			if err == nil {
				return true, nil
			}
			api, ok := apiErr(err)
			if !ok {
				return false, err
			}
			notRead(key, fmt.Sprintf("Capella returned %d", api.Status))
			note := state
			if note == "" {
				note = "unavailable"
			}
			comment(fmt.Sprintf("not imported: %s (Capella returned %d while the cluster is %s;\nrun cbctl capella sync again when it is on)",
				what, api.Status, note))
			res.Warnings = append(res.Warnings, Notice{
				What:   fmt.Sprintf("cluster %s: %s not imported (Capella returned %d)", name, what, api.Status),
				Action: RetrySync,
			})
			return false, nil
		}

		// Read before the schedules: allowAnyIp goes ahead of them in the file.
		var cidrs []Obj
		if _, err := guarded("allowed CIDRs", "allowedCidrs", func() error {
			list, err := client.ListAllowedCIDRs(ctx, pid, cid)
			for _, x := range list {
				cidrs = append(cidrs, toObj(x))
			}
			return err
		}); err != nil {
			return nil, err
		}
		var activeCIDRs []any
		for _, x := range cidrs {
			if s := str(x, "status"); s != "" && s == "expired" {
				continue
			}
			activeCIDRs = append(activeCIDRs, ExportCIDR(x))
		}
		for _, a := range activeCIDRs {
			if anyIP[strings.TrimSpace(str(a.(Obj), "cidr"))] {
				cl["allowAnyIp"] = true // already open in Capella; without it the imported file would not validate
				break
			}
		}

		if !free { // no on/off schedule on the free tier
			sched, _, err := client.GetSchedule(ctx, pid, cid)
			switch {
			case err == nil && len(sched) > 0:
				cl["hibernation"] = ExportSchedule(normObj(sched))
				counts["hibernation schedule"]++
			case err == nil, errors.Is(err, capellaapi.ErrNotFound):
			default:
				api, ok := apiErr(err)
				if !ok {
					return nil, err
				}
				notRead("hibernation", fmt.Sprintf("Capella returned %d", api.Status))
				res.Warnings = append(res.Warnings, Notice{
					What:   fmt.Sprintf("cluster %s: hibernation schedule not imported (Capella returned %d)", name, api.Status),
					Action: RetrySync,
				})
			}
		}

		backupsReadable := !free && !poweredOff[state]
		if backupsReadable {
			snap, err := client.GetSnapshotSchedule(ctx, pid, cid)
			switch {
			case err == nil:
				if len(snap) > 0 {
					cl["backupSchedule"] = ExportSnapshotSchedule(normObj(snap))
				}
			case errors.Is(err, capellaapi.ErrForbidden):
				backupsReadable = false
				notRead("backupSchedule", "the API key cannot read backups")
				if !hasNotice(res.Warnings, BackupRoleWarning) {
					res.Warnings = append(res.Warnings, BackupRoleWarning)
				}
			default:
				api, ok := apiErr(err)
				if !ok {
					return nil, err
				}
				notRead("backupSchedule", fmt.Sprintf("Capella returned %d", api.Status))
				res.Warnings = append(res.Warnings, Notice{
					What:   fmt.Sprintf("cluster %s: backup schedule not imported (Capella returned %d)", name, api.Status),
					Action: RetrySync,
				})
			}
		} else if !free {
			notRead("backupSchedule", state)
		}

		if len(activeCIDRs) > 0 {
			cl["allowedCidrs"] = activeCIDRs
			counts["allowed CIDR"] += len(activeCIDRs)
		}

		if poweredOff[state] { // Capella answers 500 for buckets and 422 for credentials: don't ask
			comment(fmt.Sprintf("not imported: buckets and database credentials (the cluster is %s;\nrun cbctl capella sync again when it is on)", state))
			res.Warnings = append(res.Warnings, Notice{
				What:   fmt.Sprintf("cluster %s: buckets and database credentials not imported (%s)", name, state),
				Action: fmt.Sprintf("Turn it on ('cbctl capella cluster power %s --on'), then run 'cbctl capella sync' again.", name),
			})
			notRead("buckets", state)
			notRead("databaseCredentials", state)
			exported = append(exported, cl)
			continue
		}

		singleNode := str(body, "configurationType") == "singleNode"
		var buckets []Obj
		if _, err := guarded("buckets", "buckets", func() error {
			var err error
			if free {
				var list []capellaapi.Bucket
				list, _, err = client.ListFreeBuckets(ctx, pid, cid)
				for _, b := range list {
					buckets = append(buckets, toObj(b))
				}
				return err
			}
			buckets, err = rawList(ctx, client, pid, cid, "/buckets", "data")
			return err
		}); err != nil {
			return nil, err
		}
		if len(buckets) > 0 {
			exportedBuckets := []any{}
			for _, b := range buckets {
				if free { // only the memory quota (and cross-cluster versioning) can be set on free-tier buckets
					mem := b["memoryAllocationInMb"]
					if !truthy(mem) {
						mem = int64(100)
					}
					eb := Obj{"name": b["name"], "memoryAllocationInMb": mem}
					if truthy(b["enableCrossClusterVersioning"]) {
						eb["enableCrossClusterVersioning"] = true
					}
					exportedBuckets = append(exportedBuckets, eb)
					counts["bucket"]++
					continue
				}
				bucketName := str(b, "name")
				var scopes []Obj
				if _, err := guarded("scopes of bucket "+bucketName, "scopes:"+bucketName, func() error {
					list, err := client.ListScopes(ctx, pid, cid, str(b, "id"))
					for _, s := range list {
						scopes = append(scopes, toObj(s))
					}
					return err
				}); err != nil {
					return nil, err
				}
				eb := ExportBucket(b, scopes, opts.Full, singleNode)
				if backupsReadable {
					bsched, err := client.GetBucketBackupSchedule(ctx, pid, cid, str(b, "id"))
					switch {
					case err == nil:
						if len(bsched) > 0 { // before the scopes, as `bucket backup schedule set` writes it
							scopesVal, hadScopes := eb["scopes"]
							delete(eb, "scopes")
							eb["backupSchedule"] = ExportBucketBackupSchedule(normObj(bsched))
							if hadScopes {
								eb["scopes"] = scopesVal
							}
						}
					case errors.Is(err, capellaapi.ErrNotFound):
					default:
						api, ok := apiErr(err)
						if !ok {
							return nil, err
						}
						notRead("bucketBackup:"+bucketName, fmt.Sprintf("Capella returned %d", api.Status))
					}
				} else if !free {
					notRead("bucketBackup:"+bucketName, "the API key cannot read backups")
				}
				exportedBuckets = append(exportedBuckets, eb)
				counts["bucket"]++
				sc := list(eb, "scopes")
				counts["scope"] += len(sc)
				for _, s := range sc {
					counts["collection"] += len(list(s.(Obj), "collections"))
				}
			}
			cl["buckets"] = exportedBuckets
		}

		var users []Obj
		if _, err := guarded("database credentials", "databaseCredentials", func() error {
			var err error
			users, err = rawList(ctx, client, pid, cid, "/users", "data")
			return err
		}); err != nil {
			return nil, err
		}
		if len(users) > 0 {
			creds := []any{}
			for _, u := range users {
				uname := str(u, "name")
				if IsAdvanced(u) {
					key := markerSeq.next("block")
					block, _ := yaml.Marshal([]any{Obj{"name": uname, "userRoles": u["userRoles"]}})
					res.Comments[key] = fmt.Sprintf("%s is an advanced credential (userRoles). cbctl manages basic\ncredentials only, so it is listed here for reference.\n%s",
						uname, strings.TrimRight(string(block), "\n"))
					creds = append(creds, Obj{key: ""})
					continue
				}
				if !truthy(u["access"]) { // basic, but its access was removed outside cbctl
					if opts.KnownAccess != nil {
						if access, pw, ok := opts.KnownAccess(name, uname); ok {
							note := markerSeq.next("comment")
							res.Comments[note] = fmt.Sprintf("%s has no access in Capella (removed outside cbctl); the access below is kept\nfrom the previous capella.yaml: run `cbctl capella apply` to restore it.", uname)
							item := Obj{note: "", "name": uname}
							if pw != "" {
								item["password"] = pw
							}
							item["access"] = access
							creds = append(creds, item)
							res.Warnings = append(res.Warnings, Notice{
								What:   fmt.Sprintf("credential %s/%s has no access in Capella; capella.yaml keeps the access it had", name, uname),
								Action: "Run 'cbctl capella apply' to give it that access again.",
							})
							counts["credential"]++
							continue
						}
					}
					key := markerSeq.next("block")
					block, _ := yaml.Marshal([]any{Obj{"name": uname, "access": []any{}}})
					res.Comments[key] = fmt.Sprintf("%s has no access in Capella, so it is listed here for reference. To give it access:\n  cbctl capella credential create %s %s --bucket BUCKET --password-env VAR\n%s",
						uname, name, uname, strings.TrimRight(string(block), "\n"))
					creds = append(creds, Obj{key: ""})
					continue
				}
				note := markerSeq.next("comment")
				entry := Obj{note: ""}
				for k, v := range ExportCredential(u) {
					entry[k] = v
				}
				env := PasswordEnvName(name, uname)
				res.Comments[note] = fmt.Sprintf("password not exported (Capella never returns it); the existing password is unchanged.\nTo rotate later, put the new password in %s and run\n  cbctl capella credential update %s %s --password-env %s --rotate-password",
					env, name, uname, env)
				creds = append(creds, entry)
				counts["credential"]++
			}
			cl["databaseCredentials"] = creds
		}
		exported = append(exported, cl)
	}

	// Every project and cluster is pinned by id, so a rename in the Capella UI shows as a rename.
	proj := Obj{"id": pid, "name": project.Name}
	if project.Description != "" || opts.Full {
		proj["description"] = project.Description
	}
	if exported == nil {
		exported = []any{}
	}
	res.Data = Obj{"apiVersion": APIVersion, "organizationId": client.OrgID(), "project": proj, "clusters": exported}
	return res, nil
}

// PasswordEnvName is the environment variable the placeholder comment suggests for a credential.
func PasswordEnvName(cluster, credential string) string {
	return "CBCTL_PWD_" + envSafe(cluster) + "_" + envSafe(credential)
}

func envSafe(s string) string {
	b := []byte(strings.ToUpper(s))
	for i, c := range b {
		if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

func hasNotice(list []Notice, n Notice) bool {
	for _, w := range list {
		if w == n {
			return true
		}
	}
	return false
}

// BuildDocument builds the combined `projects:` document for the given projects: it lists each
// project's clusters, filters them by opts.ClusterNames/ClusterIDs and reads everything below.
func BuildDocument(ctx context.Context, client *capellaapi.Client, projects []capellaapi.Project, opts Options) (*Result, error) {
	var results []*Result
	matched := map[string]bool{}
	filter := len(opts.ClusterNames) > 0 || len(opts.ClusterIDs) > 0
	for _, p := range projects {
		found, err := client.ListClusters(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
		}
		chosen := found
		if filter {
			chosen = nil
			for _, c := range found {
				if contains(opts.ClusterNames, c.Name) || contains(opts.ClusterIDs, c.ID) {
					chosen = append(chosen, c)
					matched[c.Name], matched[c.ID] = true, true
				}
			}
		}
		r, err := BuildProject(ctx, client, p, chosen, found, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	var missing []string
	for _, n := range append(append([]string(nil), opts.ClusterNames...), opts.ClusterIDs...) {
		if !matched[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return nil, &MissingClustersError{Names: missing}
	}
	return Combine(results, client.OrgID()), nil
}

// MissingClustersError reports clusters named by the caller that Capella does not have.
type MissingClustersError struct{ Names []string }

// Error implements error.
func (e *MissingClustersError) Error() string {
	return "cluster(s) not found in Capella: " + strings.Join(e.Names, ", ")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Combine joins single-project results into one `projects:` document.
func Combine(results []*Result, orgID string) *Result {
	out := &Result{
		Data:     Obj{"apiVersion": APIVersion, "organizationId": orgID, "projects": []any{}},
		Comments: map[string]string{},
		Counts:   map[string]int{"project": 0},
		Unread:   map[string]*UnreadInfo{},
	}
	projects := []any{}
	for _, r := range results {
		proj := Obj{}
		if p := sub(r.Data, "project"); p != nil {
			for k, v := range p {
				proj[k] = v
			}
		}
		if cl, ok := r.Data["clusters"]; ok {
			proj["clusters"] = cl
		} else {
			proj["clusters"] = []any{}
		}
		projects = append(projects, proj)
		for k, v := range r.Comments {
			out.Comments[k] = v
		}
		for _, w := range r.Warnings {
			if !hasNotice(out.Warnings, w) {
				out.Warnings = append(out.Warnings, w)
			}
		}
		for k, v := range r.Unread {
			out.Unread[k] = v
		}
		out.Counts["project"]++
		for k, v := range r.Counts {
			out.Counts[k] += v
		}
	}
	out.Data["projects"] = projects
	return out
}

// ToProjectsForm converts the single-project form to the projects: form (for comparisons); the
// projects: form is returned as is.
func ToProjectsForm(data Obj) Obj {
	if _, ok := data["projects"]; ok {
		return data
	}
	if _, ok := data["project"]; !ok {
		return data
	}
	out := Obj{}
	for k, v := range data {
		if k != "project" && k != "clusters" {
			out[k] = v
		}
	}
	proj := Obj{}
	for k, v := range sub(data, "project") {
		proj[k] = v
	}
	if cl, ok := data["clusters"]; ok {
		proj["clusters"] = cl
	} else {
		proj["clusters"] = []any{}
	}
	out["projects"] = []any{proj}
	return out
}

// Comparable drops tool comment markers so only real differences count.
func Comparable(v any) any {
	switch t := v.(type) {
	case Obj:
		out := Obj{}
		for k, x := range t {
			if !strings.HasPrefix(k, "__") {
				out[k] = Comparable(x)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			if m, ok := x.(Obj); ok && len(Comparable(m).(Obj)) == 0 && len(m) > 0 {
				continue // a commented-out list entry
			}
			out = append(out, Comparable(x))
		}
		return out
	}
	return v
}

// SortedKeys returns the keys of m in order.
func SortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
