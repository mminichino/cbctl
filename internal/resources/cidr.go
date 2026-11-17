package resources

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/plan"
)

const kindAllowedCIDR = "allowed-cidr"

// ReplaceNote is attached to a CIDR change: the API has no update, so the entry is replaced.
const ReplaceNote = "(replace: API has no update)"

var anyIP = map[string]bool{"0.0.0.0/0": true, "::/0": true}

// Canonical is the form CIDRs are matched in: a bare IP becomes /32 (/128). Capella's own values
// are read leniently, so a value that is not a network address is only trimmed.
func Canonical(cidr string) string {
	if c, err := capellafile.NormalizeCIDR(cidr); err == nil {
		return c
	}
	return strings.TrimSpace(cidr)
}

// ParseTime reads an RFC 3339 time; a time without a zone is UTC. ok is false when v is absent or
// unreadable.
func ParseTime(v any) (time.Time, bool) {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, strings.ToUpper(s)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// IsExpired reports whether an expiresAt value is at or before now.
func IsExpired(expiresAt any, now time.Time) bool {
	t, ok := ParseTime(expiresAt)
	return ok && !t.After(now)
}

// CreateBody is the CIDR to create; empty comment and expiresAt are omitted.
func CreateBody(desired Obj) (cidr, comment, expiresAt string) {
	cidr = Canonical(pyPlain(desired["cidr"]))
	if truthy(desired["comment"]) {
		comment = pyPlain(desired["comment"])
	}
	if truthy(desired["expiresAt"]) {
		expiresAt = pyPlain(desired["expiresAt"])
	}
	return cidr, comment, expiresAt
}

// CIDRChanges compares comment and expiresAt as the file has them: an omitted comment means none
// and an omitted expiresAt means permanent.
func CIDRChanges(desired, observed Obj) []plan.Change {
	var changes []plan.Change
	dComment, oComment := pyPlain(desired["comment"]), pyPlain(observed["comment"])
	if dComment != oComment {
		changes = append(changes, plan.Change{Path: "comment", Before: nilIfEmpty(oComment), After: nilIfEmpty(dComment)})
	}
	dExp, oExp := desired["expiresAt"], observed["expiresAt"]
	dt, dok := ParseTime(dExp)
	ot, ook := ParseTime(oExp)
	if dok != ook || (dok && !dt.Equal(ot)) {
		changes = append(changes, plan.Change{Path: "expiresAt", Before: nilIfEmptyAny(oExp), After: nilIfEmptyAny(dExp)})
	}
	return changes
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nilIfEmptyAny(v any) any {
	if !truthy(v) {
		return nil
	}
	return v
}

func cidrSummary(desired Obj) string {
	var parts []string
	if truthy(desired["comment"]) {
		parts = append(parts, fmt.Sprintf("%q", pyPlain(desired["comment"])))
	}
	if truthy(desired["expiresAt"]) {
		parts = append(parts, "expires "+pyPlain(desired["expiresAt"]))
	} else {
		parts = append(parts, "permanent")
	}
	return strings.Join(parts, " · ")
}

// cidrRec reconciles allowed CIDRs. An entry is identified by its canonical CIDR; the API has no
// update, so a changed comment or expiresAt replaces the entry.
type cidrRec struct {
	plan.Base
	now time.Time
}

func newAllowedCIDR() *cidrRec {
	r := &cidrRec{Base: plan.NewBase(kindAllowedCIDR, plan.Flags{Prunable: true, ReportsUnmanaged: true})}
	r.Bind(r)
	return r
}

func (r *cidrRec) DesiredItems(parent any) []any {
	var items []any
	for _, c := range objsOf(objOf(parent)["allowedCidrs"]) {
		items = append(items, c)
	}
	return items
}

func (r *cidrRec) Key(desired any, parent *plan.Action) string {
	cidr := Canonical(pyPlain(objOf(desired)["cidr"]))
	if parent != nil {
		return parent.Key + "/" + cidr
	}
	return cidr
}

// cidrBody is an allowed CIDR as a mapping; empty fields are left out.
func cidrBody(c capellaapi.AllowedCIDR) Obj {
	o := Obj{"id": c.ID, "cidr": c.CIDR}
	for k, v := range map[string]string{"comment": c.Comment, "expiresAt": c.ExpiresAt, "status": c.Status, "type": c.Type} {
		if v != "" {
			o[k] = v
		}
	}
	return o
}

func (r *cidrRec) Observe(ctx context.Context, rc *plan.RunContext, parent *plan.Action) ([]plan.Observed, error) {
	pid, cid := projectID(parent), ""
	if parent != nil {
		cid = parent.ID()
	}
	if err := needIDs("allowed-cidr", pid, cid); err != nil {
		return nil, err
	}
	r.now = rc.Clock.Now()
	list, err := rc.Client.ListAllowedCIDRs(ctx, pid, cid)
	if err != nil {
		return nil, err
	}
	out := make([]plan.Observed, 0, len(list))
	for _, c := range list {
		out = append(out, plan.Observed{ID: c.ID, Body: cidrBody(c)})
	}
	return out, nil
}

func (r *cidrRec) Match(desired any, observed []plan.Observed) ([]plan.Observed, error) {
	want := Canonical(pyPlain(objOf(desired)["cidr"]))
	var out []plan.Observed
	for _, o := range observed {
		if Canonical(pyPlain(o.Body["cidr"])) == want {
			out = append(out, o)
		}
	}
	return out, nil
}

// Manages is false for an entry that has already expired: Capella removes those itself.
func (r *cidrRec) Manages(o plan.Observed) bool {
	if r.now.IsZero() {
		return true
	}
	return !IsExpired(o.Body["expiresAt"], r.now)
}

func (r *cidrRec) Diff(_ context.Context, rc *plan.RunContext, desiredAny any, observed *plan.Observed, parent *plan.Action) (*plan.Action, error) {
	desired := objOf(desiredAny)
	key := r.Key(desired, parent)
	cidr := Canonical(pyPlain(desired["cidr"]))
	cluster := "CLUSTER"
	if parent != nil {
		cluster = parent.Key
	}
	confirmed := parent != nil && truthy(objOf(parent.Desired)["allowAnyIp"])
	if anyIP[cidr] && confirmed {
		rc.Warning(fmt.Sprintf("allowed CIDR %s on %s lets any IP address connect (allowAnyIp: true)", cidr, cluster),
			fmt.Sprintf("Keep it only if the cluster must be reachable from anywhere; otherwise run 'cbctl capella cidr delete %s %s'.", cluster, cidr))
	}
	if observed == nil {
		switch {
		case IsExpired(desired["expiresAt"], rc.Clock.Now()):
			a := newAction(r, kindAllowedCIDR, key, plan.Noop, desired, nil, parent)
			a.Notes = []string{"(expiresAt is in the past; Capella removed it, not re-created)"}
			return a, nil
		case anyIP[cidr] && !confirmed: // never open a cluster to every address by accident
			a := newAction(r, kindAllowedCIDR, key, plan.Blocked, desired, nil, parent)
			a.Topic = "safety"
			a.Reason = fmt.Sprintf("%s would let every IP address connect; to create it, run 'cbctl capella cidr create %s %s --allow-any-ip' "+
				"(it records allowAnyIp in capella.yaml)", cidr, cluster, cidr)
			return a, nil
		}
		a := newAction(r, kindAllowedCIDR, key, plan.Create, desired, nil, parent)
		a.Summary = cidrSummary(desired)
		return a, nil
	}
	changes := CIDRChanges(desired, observed.Body)
	if len(changes) == 0 {
		return newAction(r, kindAllowedCIDR, key, plan.Noop, desired, observed, parent), nil
	}
	a := newAction(r, kindAllowedCIDR, key, plan.Replace, desired, observed, parent)
	a.Changes = changes
	a.Notes = []string{ReplaceNote}
	return a, nil
}

func (r *cidrRec) UnmanagedLabel(o plan.Observed, parent *plan.Action) string {
	cidr := Canonical(pyPlain(o.Body["cidr"]))
	label := fmt.Sprintf("%s %s", kindAllowedCIDR, cidr)
	if parent != nil {
		label = fmt.Sprintf("%s %s/%s", kindAllowedCIDR, parent.Key, cidr)
	}
	if c := pyPlain(o.Body["comment"]); c != "" {
		label += fmt.Sprintf("  %q", c)
	}
	return label
}

func (r *cidrRec) DeleteAction(_ context.Context, _ *plan.RunContext, o plan.Observed, parent *plan.Action) (*plan.Action, error) {
	cidr := Canonical(pyPlain(o.Body["cidr"]))
	key := cidr
	if parent != nil {
		key = parent.Key + "/" + cidr
	}
	obs := o
	return newAction(r, kindAllowedCIDR, key, plan.Delete, Obj{"cidr": cidr}, &obs, parent), nil
}

func (r *cidrRec) Apply(ctx context.Context, rc *plan.RunContext, a *plan.Action) error {
	pid, cid := projectID(a), ""
	if a.Parent != nil {
		cid = a.Parent.ID()
	}
	if err := needIDs("allowed-cidr "+a.Key, pid, cid); err != nil {
		return err
	}
	d := objOf(a.Desired)
	if a.Type == plan.Create {
		cidr, comment, expires := CreateBody(d)
		id, err := rc.Client.CreateAllowedCIDR(ctx, pid, cid, cidr, comment, expires)
		if err != nil {
			return err
		}
		a.CreatedID = id
		return nil
	}
	if a.Observed == nil || a.Observed.ID == "" {
		return fmt.Errorf("allowed CIDR %s has no id", a.Key)
	}
	old := a.Observed.ID
	switch a.Type {
	case plan.Delete:
		return ignoreNotFound(rc.Client.DeleteAllowedCIDR(ctx, pid, cid, old))
	case plan.Replace:
		// A replace keeps the CIDR (it is the entry's key) and Capella refuses the same CIDR twice
		// on a cluster (422 "The CIDR provided already exists for the cluster"; nightly 44144706943),
		// so the old entry goes first. Access from the range stops until the new one is in.
		cidr, comment, expires := CreateBody(d)
		rc.Warning(fmt.Sprintf("access from %s is briefly interrupted", cidr),
			"None needed: Capella replaces the range, and access returns within seconds.")
		if err := ignoreNotFound(rc.Client.DeleteAllowedCIDR(ctx, pid, cid, old)); err != nil {
			return err
		}
		id, err := createAfterDelete(ctx, rc, func() (string, error) {
			return rc.Client.CreateAllowedCIDR(ctx, pid, cid, cidr, comment, expires)
		})
		if err != nil {
			return fmt.Errorf("allowed CIDR %s was removed but not added back: %w (run 'cbctl capella apply' again to add it)", cidr, err)
		}
		a.CreatedID = id
	}
	return nil
}

// cidrRecreateAttempts and cidrRecreateDelay bound the wait for a deleted entry to leave Capella:
// a create right after the delete can still see the old entry and be refused as a duplicate.
const (
	cidrRecreateAttempts = 6
	cidrRecreateDelay    = 5 * time.Second
)

// createAfterDelete runs create, retrying while Capella still reports the deleted entry as a
// duplicate. Any other error, or the last refusal, is returned.
func createAfterDelete(ctx context.Context, rc *plan.RunContext, create func() (string, error)) (string, error) {
	for attempt := 1; ; attempt++ {
		id, err := create()
		if err == nil || attempt == cidrRecreateAttempts || !capellaapi.IsDuplicateCIDR(err) {
			return id, err
		}
		if serr := rc.Clock.Sleep(ctx, cidrRecreateDelay); serr != nil {
			return "", serr
		}
	}
}
