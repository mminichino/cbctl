package capellafake

import (
	"github.com/google/uuid"
)

// SeedAllowedCIDR adds an allowed CIDR to the cluster's allow list directly, with the given status
// ("active", "expired", ...; "" means active), and returns its ID. Capella keeps expired entries
// listed until it removes them, which a create request cannot model.
func (f *Fake) SeedAllowedCIDR(clusterID, cidr, comment, status string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedAllowedCIDR: unknown cluster %q", clusterID)
	}
	if status == "" {
		status = "active"
	}
	e := &res{ver: 1, obj: Obj{"id": uuid.NewString(), "cidr": cidr, "comment": comment,
		"expiresAt": "", "status": status, "type": "permanent", "audit": f.audit(1)}}
	cl.cidrs = append(cl.cidrs, e)
	return str(e.obj, "id")
}

// SeedOnOffSchedule gives the cluster an on/off schedule directly. sched is the body Capella
// returns ("timezone" and "days"); paused sets the activation status to paused.
func (f *Fake) SeedOnOffSchedule(clusterID string, sched Obj, paused bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedOnOffSchedule: unknown cluster %q", clusterID)
	}
	cl.schedule, cl.scheduleVer, cl.schedulePaused = clone(sched), 1, paused
}

// SetClusterField changes one top-level field of a cluster out of band, as an edit in the Capella
// UI would, with no request recorded. It reports whether the cluster exists.
func (f *Fake) SetClusterField(clusterID, key string, value any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		return false
	}
	cl.obj[key] = value
	cl.ver++
	return true
}

// SeedAdvancedCredential creates an advanced database credential (userRoles instead of access)
// and returns its ID.
func (f *Fake) SeedAdvancedCredential(clusterID, name string, roles []string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedAdvancedCredential: unknown cluster %q", clusterID)
	}
	userRoles := make([]any, len(roles))
	for i, r := range roles {
		userRoles[i] = r
	}
	u := &res{ver: 1, obj: Obj{"id": uuid.NewString(), "name": name, "access": []any{},
		"userRoles": userRoles, "credentialType": "advanced", "audit": f.audit(1)}}
	cl.creds = append(cl.creds, u)
	return str(u.obj, "id")
}
