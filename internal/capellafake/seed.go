package capellafake

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/mminichino/cbctl/internal/capellaapi"
)

// Field mutates a seeded resource object (Phase 4 §3.5).
type Field func(map[string]any)

// WithField sets a single key on a seeded object.
func WithField(key string, value any) Field {
	return func(m map[string]any) {
		if m != nil {
			m[key] = value
		}
	}
}

func applyFields(obj Obj, fields []Field) {
	for _, field := range fields {
		if field != nil {
			field(obj)
		}
	}
}

// SeedFreeCluster creates a free-tier cluster already in state and returns its ID.
func (f *Fake) SeedFreeCluster(projectID, name, state string, fields ...Field) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.findProject(projectID)
	if p == nil {
		f.t.Fatalf("capellafake: SeedFreeCluster: unknown project %q", projectID)
	}
	for _, q := range f.projects {
		for _, cl := range q.clusters {
			if cl.free {
				f.t.Fatalf("capellafake: SeedFreeCluster: organization already has a free-tier cluster")
			}
		}
	}
	c := f.newCluster(p, Obj{
		"name":          name,
		"cloudProvider": Obj{"type": "aws", "region": "us-east-1"},
		"serviceGroups": []any{Obj{"numOfNodes": 1, "services": []any{"data", "index", "query", "search"},
			"node": Obj{"compute": Obj{"cpu": 2, "ram": 8}, "disk": Obj{"type": "gp3", "storage": 50, "iops": 3000}}}},
		"availability": Obj{"type": "single"},
		"support":      Obj{"plan": "free", "timezone": "PT"},
	}, true)
	c.obj["currentState"] = state
	c.target = ""
	applyFields(c.obj, fields)
	return c.obj["id"].(string)
}

// SeedBucket creates a bucket on the cluster and returns its Capella bucket ID.
func (f *Fake) SeedBucket(clusterID, name string, fields ...Field) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedBucket: unknown cluster %q", clusterID)
	}
	extra := Obj{}
	applyFields(extra, fields)
	b := cl.addBucket(name, extra)
	// addBucket ignores stats (a create body cannot set them); a seed may, to model stored data.
	if stats, ok := extra["stats"].(map[string]any); ok {
		merged := Obj{}
		for k, v := range b.obj["stats"].(Obj) {
			merged[k] = v
		}
		for k, v := range stats {
			merged[k] = v
		}
		b.obj["stats"] = merged
	}
	return str(b.obj, "id")
}

// SeedScope creates a scope (and optional collections with maxTTL seconds) under a bucket.
func (f *Fake) SeedScope(clusterID, bucket, scopeName string, collections map[string]int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedScope: unknown cluster %q", clusterID)
	}
	b := cl.bucketByName(bucket)
	if b == nil {
		f.t.Fatalf("capellafake: SeedScope: unknown bucket %q", bucket)
	}
	if b.findScope(scopeName) != nil {
		f.t.Fatalf("capellafake: SeedScope: scope %q already exists", scopeName)
	}
	s := &scope{name: scopeName}
	for name, ttl := range collections {
		obj := Obj{"name": name}
		if ttl != 0 {
			obj["maxTTL"] = ttl
		}
		s.colls = append(s.colls, &res{ver: 1, obj: obj})
	}
	b.scopes = append(b.scopes, s)
}

// SeedCredential creates a database credential and returns its ID.
func (f *Fake) SeedCredential(clusterID, name string, access []capellaapi.AccessEntry, advanced bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedCredential: unknown cluster %q", clusterID)
	}
	accessAny := []any{}
	if access != nil {
		raw, err := json.Marshal(access)
		if err != nil {
			f.t.Fatalf("capellafake: SeedCredential: marshal access: %v", err)
		}
		if err := json.Unmarshal(raw, &accessAny); err != nil {
			f.t.Fatalf("capellafake: SeedCredential: unmarshal access: %v", err)
		}
	}
	obj := Obj{"id": uuid.NewString(), "name": name, "access": accessAny, "audit": f.audit(1)}
	if advanced {
		obj["credentialType"] = "advanced"
	}
	u := &res{ver: 1, obj: obj}
	cl.creds = append(cl.creds, u)
	return str(u.obj, "id")
}

// SeedSnapshot creates a cloud snapshot backup record and returns its ID.
func (f *Fake) SeedSnapshot(clusterID string, createdAt string, retentionHours int, status string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedSnapshot: unknown cluster %q", clusterID)
	}
	if createdAt == "" {
		createdAt = f.clockNow().UTC().Format(time.RFC3339)
	}
	if retentionHours <= 0 {
		retentionHours = 168
	}
	if status == "" {
		status = "complete"
	}
	created, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		created = f.clockNow()
		createdAt = created.Format(time.RFC3339)
	}
	s := &snapshot{obj: Obj{
		"id": uuid.NewString(), "createdAt": createdAt, "type": "adhoc",
		"progress":     Obj{"status": status},
		"retention":    retentionHours,
		"expiration":   created.Add(time.Duration(retentionHours) * time.Hour).Format(time.RFC3339),
		"databaseSize": 0, "crossRegionCopies": []any{},
	}}
	if status == "complete" || status == "pending" {
		s.polls = f.settle + 1
	}
	cl.snapshots = append(cl.snapshots, s)
	return str(s.obj, "id")
}

// SeedBucketBackup creates an on-demand bucket backup record and returns its ID. Fields override
// the record, for example WithField("cycleID", "cy-1"), WithField("date", ...) or
// WithField("status", "pending").
func (f *Fake) SeedBucketBackup(clusterID, bucket, method, source string, fields ...Field) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedBucketBackup: unknown cluster %q", clusterID)
	}
	b := cl.bucketByName(bucket)
	if b == nil {
		f.t.Fatalf("capellafake: SeedBucketBackup: unknown bucket %q", bucket)
	}
	obj := f.newBackupObj(cl, b, "", method, source, "ready")
	applyFields(obj, fields)
	cl.backups = append(cl.backups, &backup{obj: obj})
	return str(obj, "id")
}

// DetachCluster removes a cluster from fake state and returns a function that puts it back.
// Used by parity tests to temporarily clear a one-per-org free-tier cluster around create.
func (f *Fake) DetachCluster(clusterID string) (restore func()) {
	f.mu.Lock()
	p, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.mu.Unlock()
		return func() {}
	}
	p.removeCluster(cl)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, existing := f.findClusterAnywhere(str(cl.obj, "id")); existing != nil {
			return
		}
		if proj := f.findProject(cl.projectID); proj != nil {
			proj.clusters = append(proj.clusters, cl)
		}
	}
}

// ClearProjectClusters removes every cluster from a project (parity DELETE project prep).
func (f *Fake) ClearProjectClusters(projectID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.findProject(projectID); p != nil {
		p.clusters = nil
	}
}

// SeedExport creates a bucket-backup export record and returns its ID.
// Status "failed" leaves the cycle free for a subsequent POST /export (parity with capellactl).
func (f *Fake) SeedExport(clusterID, backupID, status string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SeedExport: unknown cluster %q", clusterID)
	}
	bk := cl.findBackup(backupID)
	if bk == nil {
		f.t.Fatalf("capellafake: SeedExport: unknown backup %q", backupID)
	}
	if status == "" {
		status = "pending"
	}
	id := uuid.NewString()
	bucketName := str(bk.obj, "bucketName")
	e := &export{cycleID: str(bk.obj, "cycleID"), backupID: backupID, obj: Obj{
		"id": id, "exportId": id, "tenantId": f.OrgID, "projectId": cl.projectID, "clusterId": cl.obj["id"],
		"cycleId": str(bk.obj, "cycleID"), "bucketName": bucketName, "status": status, "createdAt": f.now(),
	}}
	if status == "complete" || status == "pending" {
		e.polls = f.settle + 1
	}
	cl.exports = append(cl.exports, e)
	return id
}

// SetTransition records a clock-driven state path for a cluster.
//
// When the Fake has a FakeClock and step > 0, every GET computes
// idx = min(elapsed/step, len(path)-1). Otherwise this adapts onto the existing
// poll/settle model: current state becomes path[0] and the settle target becomes path[len-1]
// (intermediate states are skipped; use SettlePolls to control how many GETs it takes).
func (f *Fake) SetTransition(clusterID string, path []string, step time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: SetTransition: unknown cluster %q", clusterID)
	}
	if len(path) == 0 {
		cl.path = nil
		cl.target = ""
		return
	}
	cl.obj["currentState"] = path[0]
	cl.polls = 0
	if len(path) == 1 {
		cl.path = nil
		cl.pathStep = 0
		cl.target = ""
		return
	}
	if f.clock != nil && step > 0 {
		cl.path = append([]string(nil), path...)
		cl.pathStep = step
		cl.pathStart = f.clock.Now()
		cl.target = ""
		return
	}
	// Adapter onto poll/settle: jump from first to last after SettlePolls GETs.
	cl.path = nil
	cl.pathStep = 0
	cl.transition(path[0], path[len(path)-1])
}

// FailDestroy makes the next successful DELETE of the cluster settle on destroyFailed
// instead of removing the cluster.
func (f *Fake) FailDestroy(clusterID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, cl := f.findClusterAnywhere(clusterID)
	if cl == nil {
		f.t.Fatalf("capellafake: FailDestroy: unknown cluster %q", clusterID)
	}
	cl.failDestroy = true
}

// Advance moves the Fake's clock forward by d (convenience for Clock().Advance(d)).
func (f *Fake) Advance(d time.Duration) {
	if f.clock != nil {
		f.clock.Advance(d)
	}
}

// RenameProject changes a project's name as the Capella UI would, keeping its ID. It reports
// whether the project exists.
func (f *Fake) RenameProject(projectID, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.findProject(projectID)
	if p == nil {
		return false
	}
	p.obj["name"] = name
	p.ver++
	return true
}

// RemoveProject deletes a project and its clusters out of band (as another user would), with no
// request recorded. It reports whether the project existed.
func (f *Fake) RemoveProject(projectID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, p := range f.projects {
		if str(p.obj, "id") == projectID {
			f.projects = append(f.projects[:i], f.projects[i+1:]...)
			return true
		}
	}
	return false
}

// FailNextBucketBackup makes the next on-demand bucket backup end in status "failed".
func (f *Fake) FailNextBucketBackup() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNextBackup = true
}
