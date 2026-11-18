package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/plan"
	"github.com/mminichino/cbctl/internal/secrets"
)

const (
	kindCredential = "credential"

	// AllBuckets is the access-map key of a grant that names no bucket.
	AllBuckets = "(all buckets)"
	// PasswordNote is on every credential update: Capella never returns passwords to compare.
	PasswordNote = "note: passwords are not compared; use --rotate-password to change them"
	// AdvancedReason blocks a declared name that belongs to an advanced credential in Capella.
	AdvancedReason = "an advanced credential with this name exists in Capella; cbctl manages basic credentials " +
		"only (advanced credentials are planned for a later release)"
	wildcard = "*"
)

// privilegeSet is a set of canonical privilege names.
type privilegeSet map[string]bool

func canonPrivilegeSet(privs any) privilegeSet {
	out := privilegeSet{}
	for _, p := range listOf(privs) {
		s := fmt.Sprint(p)
		if alias, ok := plan.PrivilegeAliases[s]; ok {
			s = alias
		}
		out[s] = true
	}
	return out
}

// PrivilegeLabel renders data_reader as read, data_writer as write, both as read+write; other
// privileges are listed as they are. nil for an empty set.
func PrivilegeLabel(privs map[string]bool) any {
	if len(privs) == 0 {
		return nil
	}
	var words []string
	if privs["data_reader"] {
		words = append(words, "read")
	}
	if privs["data_writer"] {
		words = append(words, "write")
	}
	var rest []string
	for p := range privs {
		if p != "data_reader" && p != "data_writer" {
			rest = append(rest, p)
		}
	}
	sort.Strings(rest)
	return strings.Join(append(words, rest...), "+")
}

func labelText(privs map[string]bool) string {
	s, _ := PrivilegeLabel(privs).(string)
	return s
}

// WithoutWildcards removes the "*" levels Capella returns for "every bucket, scope or
// collection"; the file writes those levels by leaving them out. A whole-bucket grant comes back
// as scopes [{name: "*"}]. It returns new mappings.
func WithoutWildcards(access any) []Obj {
	var out []Obj
	for _, entry := range objsOf(access) {
		e := Obj{}
		for k, v := range entry {
			if k != "resources" {
				e[k] = v
			}
		}
		resources := objOf(entry["resources"])
		buckets := objsOf(resources["buckets"])
		switch {
		case len(buckets) > 0 && !anyNamed(buckets, wildcard):
			rb := make([]any, 0, len(buckets))
			for _, b := range buckets {
				item := Obj{}
				for k, v := range b {
					if k != "scopes" {
						item[k] = v
					}
				}
				scopes := objsOf(b["scopes"])
				if len(scopes) > 0 && !anyNamed(scopes, wildcard) {
					ss := make([]any, 0, len(scopes))
					for _, s := range scopes {
						sc := Obj{}
						for k, v := range s {
							if k != "collections" {
								sc[k] = v
							}
						}
						if colls := listOf(s["collections"]); len(colls) > 0 && !contains(stringsOf(colls), wildcard) {
							sc["collections"] = append([]any(nil), colls...)
						}
						ss = append(ss, sc)
					}
					item["scopes"] = ss
				}
				rb = append(rb, item)
			}
			res := copyObj(resources)
			res["buckets"] = rb
			e["resources"] = res
		case len(resources) > 0 && len(buckets) == 0:
			e["resources"] = resources
		}
		out = append(out, e)
	}
	return out
}

func anyNamed(items []Obj, name string) bool {
	for _, i := range items {
		if i["name"] == name {
			return true
		}
	}
	return false
}

// resourceKeys are the resources one grant applies to: AllBuckets, "bucket", "bucket/scope" or
// "bucket/scope/collection".
func resourceKeys(entry Obj) []string {
	buckets := objsOf(objOf(entry["resources"])["buckets"])
	if len(buckets) == 0 {
		return []string{AllBuckets}
	}
	var keys []string
	for _, b := range buckets {
		scopes := objsOf(b["scopes"])
		if len(scopes) == 0 {
			keys = append(keys, pyPlain(b["name"]))
		}
		for _, s := range scopes {
			colls := stringsOfAny(s["collections"])
			if len(colls) == 0 {
				keys = append(keys, fmt.Sprintf("%s/%s", pyPlain(b["name"]), pyPlain(s["name"])))
			}
			for _, c := range colls {
				keys = append(keys, fmt.Sprintf("%s/%s/%s", pyPlain(b["name"]), pyPlain(s["name"]), c))
			}
		}
	}
	return keys
}

func stringsOfAny(v any) []string {
	var out []string
	for _, e := range listOf(v) {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

// AccessMap is the canonical access: one privilege set per resource key.
func AccessMap(access any) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, entry := range WithoutWildcards(access) {
		privs := canonPrivilegeSet(entry["privileges"])
		for _, k := range resourceKeys(entry) {
			set := out[k]
			if set == nil {
				set = map[string]bool{}
				out[k] = set
			}
			for p := range privs {
				set[p] = true
			}
		}
	}
	return out
}

func samePrivileges(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// AccessChanges lists `orders/inventory: read → read+write` for each resource whose privileges differ.
func AccessChanges(desired, observed any) []plan.Change {
	d, o := AccessMap(desired), AccessMap(observed)
	keys := map[string]bool{}
	for k := range d {
		keys[k] = true
	}
	for k := range o {
		keys[k] = true
	}
	ordered := sortedKeys(keys)
	sort.SliceStable(ordered, func(i, j int) bool {
		return (ordered[i] == AllBuckets) != (ordered[j] == AllBuckets) && ordered[j] == AllBuckets
	})
	var changes []plan.Change
	for _, k := range ordered {
		if !samePrivileges(d[k], o[k]) {
			changes = append(changes, plan.Change{Path: k, Before: PrivilegeLabel(o[k]), After: PrivilegeLabel(d[k])})
		}
	}
	return changes
}

func resourceText(entry Obj) string {
	buckets := objsOf(objOf(entry["resources"])["buckets"])
	if len(buckets) == 0 {
		return AllBuckets
	}
	sort.SliceStable(buckets, func(i, j int) bool { return pyPlain(buckets[i]["name"]) < pyPlain(buckets[j]["name"]) })
	var parts []string
	for _, b := range buckets {
		scopes := objsOf(b["scopes"])
		if len(scopes) == 0 {
			parts = append(parts, pyPlain(b["name"]))
		}
		sort.SliceStable(scopes, func(i, j int) bool { return pyPlain(scopes[i]["name"]) < pyPlain(scopes[j]["name"]) })
		for _, s := range scopes {
			colls := stringsOfAny(s["collections"])
			sort.Strings(colls)
			tail := ""
			switch {
			case len(colls) == 1:
				tail = "/" + colls[0]
			case len(colls) > 1:
				tail = "/{" + strings.Join(colls, ",") + "}"
			}
			parts = append(parts, fmt.Sprintf("%s/%s%s", pyPlain(b["name"]), pyPlain(s["name"]), tail))
		}
	}
	return strings.Join(parts, ", ")
}

// AccessSummary is `read+write · orders/inventory/{items,suppliers}`; grants to all buckets come last.
func AccessSummary(access any) string {
	entries := WithoutWildcards(access)
	sort.SliceStable(entries, func(i, j int) bool {
		return resourceText(entries[i]) != AllBuckets && resourceText(entries[j]) == AllBuckets
	})
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = fmt.Sprintf("%s · %s", labelText(canonPrivilegeSet(e["privileges"])), resourceText(e))
	}
	return strings.Join(parts, "; ")
}

// APIAccess is the access list as the API takes it: the file's grants without tool-only keys.
func APIAccess(access any) []any {
	out := []any{}
	for _, entry := range objsOf(access) {
		privs := listOf(entry["privileges"])
		if privs == nil {
			privs = []any{}
		}
		e := Obj{"privileges": append([]any(nil), privs...)}
		if buckets := objsOf(objOf(entry["resources"])["buckets"]); len(buckets) > 0 {
			rb := make([]any, 0, len(buckets))
			for _, b := range buckets {
				item := Obj{"name": b["name"]}
				if scopes := objsOf(b["scopes"]); len(scopes) > 0 {
					ss := make([]any, 0, len(scopes))
					for _, s := range scopes {
						sc := Obj{"name": s["name"]}
						if colls := listOf(s["collections"]); len(colls) > 0 {
							sc["collections"] = append([]any(nil), colls...)
						}
						ss = append(ss, sc)
					}
					item["scopes"] = ss
				}
				rb = append(rb, item)
			}
			e["resources"] = Obj{"buckets": rb}
		}
		out = append(out, e)
	}
	return out
}

// IsAdvanced is true for userRoles credentials. A basic credential whose access was removed
// (empty access) is not advanced.
func IsAdvanced(body Obj) bool {
	return truthy(body["userRoles"]) || body["credentialType"] == "advanced"
}

// KeptSecretNote tells that the local file of a generated password is never deleted by cbctl;
// "" when there is no such file.
func KeptSecretNote(dir, cluster, name string) string {
	p := secrets.SecretPath(dir, cluster, name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return fmt.Sprintf("local secret file kept: %s (delete it if no longer needed)", p)
}

func clusterName(cluster *plan.Action) string {
	if cluster == nil {
		return ""
	}
	if n := strOf(objOf(cluster.Desired), "name"); n != "" {
		return n
	}
	return cluster.Key
}

func accessEntries(v any) ([]capellaapi.AccessEntry, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out []capellaapi.AccessEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("access: %w", err)
	}
	return out, nil
}

// credentialRec reconciles database credentials. Capella never returns passwords: they are sent on
// create and afterwards only on explicit rotation, and a changed ${VAR} value is not a change.
type credentialRec struct {
	plan.Base
	opts      Options
	gitWarned bool
}

func newCredential(opts Options) *credentialRec {
	r := &credentialRec{Base: plan.NewBase(kindCredential, plan.Flags{Prunable: true, ReportsUnmanaged: true}), opts: opts}
	r.Bind(r)
	return r
}

func (r *credentialRec) DesiredItems(parent any) []any {
	var items []any
	for _, c := range objsOf(objOf(parent)["databaseCredentials"]) {
		items = append(items, c)
	}
	return items
}

func (r *credentialRec) Key(desired any, parent *plan.Action) string {
	name := pyPlain(objOf(desired)["name"])
	if parent != nil {
		return parent.Key + "/" + name
	}
	return name
}

func (r *credentialRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	if clusterContentsUnavailable(parent) {
		return nil, nil
	}
	pid, cid := clusterIDs(parent)
	if err := needIDs("credential", pid, cid); err != nil {
		return nil, err
	}
	api, err := newRawAPI(rc)
	if err != nil {
		return nil, err
	}
	users, err := api.list(ctx, api.users(pid, cid), "data")
	if err != nil {
		return nil, err
	}
	out := make([]plan.Observed, 0, len(users))
	for _, u := range users {
		out = append(out, plan.Observed{ID: strOf(u, "id"), Body: u})
	}
	return out, nil
}

func (r *credentialRec) Diff(_ context.Context, rc *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	if observed == nil {
		a := newAction(r, kindCredential, key, plan.Create, desired, nil, parent)
		a.Summary = AccessSummary(desired["access"])
		return a, nil
	}
	a := newAction(r, kindCredential, key, plan.Noop, desired, observed, parent)
	if IsAdvanced(observed.Body) {
		a.Type, a.Reason, a.Topic = plan.Blocked, AdvancedReason, "credentials"
		return a, nil
	}
	a.Changes = AccessChanges(desired["access"], observed.Body["access"])
	// An in-memory run keeps no state, so a changed ${VAR} cannot be told from an unchanged one:
	// it never rotates (D3).
	if rc.RotatePasswords && !rc.Ephemeral {
		a.Set("rotate", true)
		a.Label = "rotate password"
	}
	if len(a.Changes) > 0 || truthy(a.Data["rotate"]) {
		a.Type = plan.Update
		if !truthy(a.Data["rotate"]) {
			a.Remarks = append(a.Remarks, PasswordNote)
		}
	}
	return a, nil
}

// DeleteAction is `apply --prune` for a basic credential the file no longer declares. An
// advanced credential is left unchanged.
func (r *credentialRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	name := pyPlain(o.Body["name"])
	key := name
	if parent != nil {
		key = parent.Key + "/" + name
	}
	obs := o
	if IsAdvanced(o.Body) {
		a := newAction(r, kindCredential, key, plan.Noop, Obj{"name": name}, &obs, parent)
		a.Notes = []string{"(advanced credential: not managed by cbctl, left unchanged)"}
		return a, nil
	}
	a := newAction(r, kindCredential, key, plan.Delete, Obj{"name": name}, &obs, parent)
	a.Summary = AccessSummary(o.Body["access"])
	return a, nil
}

func (r *credentialRec) secretDir(rc *plan.RunContext) string {
	if cfg, ok := rc.Config.(*capellafile.Config); ok && cfg != nil {
		return cfg.CredentialsOutput()
	}
	return capellafile.DefaultCredentialsOutput
}

func (r *credentialRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid := clusterIDs(a.Parent)
	if err := needIDs("credential "+a.Key, pid, cid); err != nil {
		return err
	}
	cluster := clusterName(a.Parent)
	d := objOf(a.Desired)
	if a.Type == plan.Create {
		return r.create(ctx, rc, a, pid, cid, cluster)
	}
	if a.Observed == nil || a.Observed.ID == "" {
		return fmt.Errorf("credential %s has no id", a.Key)
	}
	uid := a.Observed.ID
	switch a.Type {
	case plan.Delete:
		if err := ignoreNotFound(rc.Client.DeleteCredential(ctx, pid, cid, uid)); err != nil {
			return err
		}
		if note := KeptSecretNote(r.secretDir(rc), cluster, pyPlain(d["name"])); note != "" {
			rc.Say(note)
		}
	case plan.Update:
		return r.update(ctx, rc, a, pid, cid, uid, cluster)
	}
	return nil
}

func (r *credentialRec) create(ctx context.Context, rc *plan.RunContext, a *plan.Action, pid, cid, cluster string) error {
	d := objOf(a.Desired)
	name := pyPlain(d["name"])
	access, err := accessEntries(APIAccess(d["access"]))
	if err != nil {
		return err
	}
	req := capellaapi.CreateCredentialRequest{Name: name, Access: access}
	if pw, _ := d["password"].(string); pw != "" {
		req.Password = pw
		id, _, err := rc.Client.CreateCredential(ctx, pid, cid, req)
		if err != nil {
			return err
		}
		a.CreatedID = id
		return nil
	}
	dir := r.secretDir(rc)
	// Never create a credential whose generated password cannot be saved.
	if _, err := secrets.Preflight(dir, cluster, name, r.opts.ForceOverwriteSecret); err != nil {
		return err
	}
	if err := secrets.CheckIgnored(dir, r.opts.ForceOverwriteSecret); err != nil {
		return err
	}
	generated, err := secrets.Generate(32)
	if err != nil {
		return err
	}
	req.Password = generated
	id, returned, err := rc.Client.CreateCredential(ctx, pid, cid, req)
	if err != nil {
		return err
	}
	a.CreatedID = id
	if returned != "" {
		generated = returned
	}
	path, err := secrets.WriteGeneratedCredential(dir, cluster, name, id, generated, r.opts.ForceOverwriteSecret)
	if err != nil {
		return fmt.Errorf("credential %s was created but its password could not be saved: %w", a.Key, err)
	}
	rc.Say(secrets.SavedMessage(path))
	if rc.OnGeneratedPassword != nil {
		rc.OnGeneratedPassword(cluster, name, generated)
	}
	if !r.gitWarned {
		if what, action, ok := secrets.GitWarning(dir); ok {
			r.gitWarned = true
			rc.Warning(what, action)
		}
	}
	return nil
}

func (r *credentialRec) update(ctx context.Context, rc *plan.RunContext, a *plan.Action, pid, cid, uid, cluster string) error {
	d := objOf(a.Desired)
	name := pyPlain(d["name"])
	api, err := newRawAPI(rc)
	if err != nil {
		return err
	}
	current, etag, err := api.get(ctx, api.user(pid, cid, uid))
	if err != nil {
		return err
	}
	var req capellaapi.UpdateCredentialRequest
	changed := false
	if len(AccessChanges(d["access"], current["access"])) > 0 {
		if req.Access, err = accessEntries(APIAccess(d["access"])); err != nil {
			return err
		}
		changed = true // never the password on an access change
	}
	var savedPath, savedBackup string
	rotated := false
	if truthy(a.Data["rotate"]) {
		// Capella treats a PUT without access as "no access": a password-only rotation removes
		// every grant. Always send the access with the password.
		if req.Access == nil {
			source := current["access"]
			if truthy(d["access"]) {
				source = APIAccess(d["access"])
			}
			if req.Access, err = accessEntries(orEmptyList(source)); err != nil {
				return err
			}
		}
		password, _ := d["password"].(string)
		if password == "" { // generated; saved before it is sent
			if err := secrets.CheckIgnored(r.secretDir(rc), r.opts.ForceOverwriteSecret); err != nil {
				return err
			}
			if password, err = secrets.Generate(32); err != nil {
				return err
			}
			if savedPath, savedBackup, err = secrets.WriteRotatedCredential(r.secretDir(rc), cluster, name, uid, password); err != nil {
				return err
			}
		}
		req.Password = password
		changed, rotated = true, true
	}
	if !changed {
		return nil
	}
	if err := rc.Client.UpdateCredential(ctx, pid, cid, uid, req, etag); err != nil {
		if savedPath != "" {
			if rerr := secrets.RestoreRotatedCredential(savedPath, savedBackup); rerr != nil {
				return &secrets.RestoreError{Primary: err, Restore: rerr, Path: savedPath, Backup: savedBackup}
			}
		}
		return err
	}
	if rotated {
		rc.Say("password rotated for " + name)
		if savedPath != "" {
			rc.Say(secrets.SavedMessage(savedPath))
		}
	}
	return nil
}

func orEmptyList(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}
