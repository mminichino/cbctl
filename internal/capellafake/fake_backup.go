package capellafake

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	scheduleURL   = clusterURL + "/onOffSchedule"
	snapSchedURL  = clusterURL + "/cloudsnapshotbackupschedule"
	snapshotsURL  = clusterURL + "/cloudsnapshotbackups"
	bktSchedURL   = bucketURL + "/backup/schedules"
	bktCyclesURL  = bucketURL + "/backup/cycles"
	bktBackupsURL = bucketURL + "/backups"
	backupsURL    = clusterURL + "/backups"
)

// snapshot is a cloud snapshot or a snapshot restore. It reports "pending" until it has been
// listed settle times, then "complete".
type snapshot struct {
	obj   Obj
	polls int
}

type backup struct{ obj Obj }

type export struct {
	obj      Obj
	cycleID  string
	backupID string
	polls    int
}

// ---- on/off schedule -----------------------------------------------------------------------

func (f *Fake) routeSchedule() {
	f.handle("GET", scheduleURL, (*rctx).getSchedule)
	f.handle("POST", scheduleURL, (*rctx).createSchedule)
	f.handle("PUT", scheduleURL, (*rctx).updateSchedule)
	f.handle("DELETE", scheduleURL, (*rctx).deleteSchedule)
	f.handle("POST", scheduleURL+"/activationState", (*rctx).resumeSchedule)
	f.handle("DELETE", scheduleURL+"/activationState", (*rctx).pauseSchedule)
}

func (c *rctx) getSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.schedule == nil {
		c.notFound("on/off schedule")
		return
	}
	c.etag(cl.scheduleVer)
	body := clone(cl.schedule)
	body["activationStatus"] = "active"
	if cl.schedulePaused {
		body["activationStatus"] = "paused"
	}
	c.writeJSON(http.StatusOK, body)
}

func (c *rctx) createSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	if !c.validSchedule(body) {
		return
	}
	if cl.schedule != nil {
		c.err(http.StatusConflict, "the cluster already has an on/off schedule")
		return
	}
	cl.schedule, cl.scheduleVer, cl.schedulePaused = clone(body), 1, false
	c.noContent()
}

// validSchedule refuses what the live API refuses: a custom day whose from is not before its to,
// where a to of 0:00 means the end of the day. From 0:00 to 0:00 (the whole day) is refused too,
// as Capella does (422 "Monday has a 'from' time boundary that is later than the 'to'").
func (c *rctx) validSchedule(body Obj) bool {
	days, _ := body["days"].([]any)
	for _, d := range days {
		day, _ := d.(map[string]any)
		if day["state"] != "custom" {
			continue
		}
		from, to := sub(day, "from"), sub(day, "to")
		start := intOf(from["hour"])*60 + intOf(from["minute"])
		end := intOf(to["hour"])*60 + intOf(to["minute"])
		if end == 0 && start != 0 {
			end = 24 * 60
		}
		if start >= end {
			name, _ := day["day"].(string)
			if name != "" {
				name = strings.ToUpper(name[:1]) + name[1:]
			}
			c.err(http.StatusUnprocessableEntity,
				fmt.Sprintf("%s has a 'from' time boundary that is later than the 'to' time boundary.", name))
			return false
		}
	}
	return true
}

func (c *rctx) updateSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	if !c.validSchedule(body) {
		return
	}
	if cl.schedule == nil {
		c.notFound("on/off schedule")
		return
	}
	if !c.matches(cl.scheduleVer) {
		return
	}
	cl.schedule = clone(body)
	cl.scheduleVer++
	c.noContent()
}

func (c *rctx) deleteSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.schedule == nil {
		c.notFound("on/off schedule")
		return
	}
	cl.schedule, cl.schedulePaused = nil, false
	c.noContent()
}

func (c *rctx) pauseSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.schedule == nil {
		c.notFound("on/off schedule")
		return
	}
	cl.schedulePaused = true
	c.noContent()
}

func (c *rctx) resumeSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.schedule == nil {
		c.notFound("on/off schedule")
		return
	}
	cl.schedulePaused = false
	c.noContent()
}

// ---- cloud snapshots -----------------------------------------------------------------------

func (f *Fake) routeSnapshots() {
	f.handle("GET", snapSchedURL, (*rctx).getSnapshotSchedule)
	f.handle("PUT", snapSchedURL, (*rctx).putSnapshotSchedule)
	f.handle("DELETE", snapSchedURL, (*rctx).deleteSnapshotSchedule)
	f.handle("GET", snapSchedURL+"/options", (*rctx).snapshotOptions)

	f.handle("GET", snapshotsURL+"/regions", (*rctx).snapshotRegions)
	f.handle("GET", snapshotsURL+"/restores", (*rctx).listRestores)
	f.handle("GET", snapshotsURL, (*rctx).listSnapshots)
	f.handle("POST", snapshotsURL, (*rctx).createSnapshot)
	f.handle("PUT", snapshotsURL+"/{backup}", (*rctx).updateSnapshot)
	f.handle("DELETE", snapshotsURL+"/{backup}", (*rctx).deleteSnapshot)
	f.handle("POST", snapshotsURL+"/{backup}/restore", (*rctx).restoreSnapshot)
	f.handle("GET", projectURL+"/cloudsnapshotbackups", (*rctx).listProjectSnapshots)
}

func (c *rctx) getSnapshotSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.snapSchedule == nil {
		c.noContent()
		return
	}
	c.writeJSON(http.StatusOK, clone(cl.snapSchedule))
}

func (c *rctx) putSnapshotSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	opts := snapshotScheduleOptions()
	if iv := intOf(body["interval"]); iv != 0 && !slices.Contains(opts["intervals"].([]int), iv) {
		c.err(http.StatusUnprocessableEntity, "interval is not one of the allowed intervals")
		return
	}
	if r := intOf(body["retention"]); r != 0 && (r < opts["minimumRetention"].(int) || r > opts["maximumRetention"].(int)) {
		c.err(http.StatusUnprocessableEntity, "retention is outside the allowed range")
		return
	}
	cl.snapSchedule = clone(body)
	c.noContent()
}

func (c *rctx) deleteSnapshotSchedule() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	cl.snapSchedule = nil
	c.noContent()
}

func snapshotScheduleOptions() Obj {
	return Obj{"intervals": []int{1, 2, 4, 6, 8, 12, 24}, "minimumRetention": 24, "maximumRetention": 8760}
}

func (c *rctx) snapshotOptions() {
	if _, cl := c.cluster(); cl != nil {
		c.writeJSON(http.StatusOK, snapshotScheduleOptions())
	}
}

func (c *rctx) snapshotRegions() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	own := str(sub(cl.obj, "cloudProvider"), "region")
	out := []string{}
	for _, r := range []string{"us-east-1", "us-east-2", "us-west-2", "eu-west-1"} {
		if r != own {
			out = append(out, r)
		}
	}
	c.writeJSON(http.StatusOK, out)
}

// advance moves a pending snapshot toward "complete" one list at a time.
func (f *Fake) advance(s *snapshot, field string) {
	holder := s.obj
	if field == "progress" {
		holder = sub(s.obj, "progress")
	}
	if str(holder, "status") != "pending" {
		return
	}
	if s.polls < f.settle {
		s.polls++
		return
	}
	holder["status"] = "complete"
}

func (c *rctx) listSnapshots() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	items := make([]Obj, 0, len(cl.snapshots))
	for _, s := range cl.snapshots {
		c.f.advance(s, "progress")
		items = append(items, s.obj)
	}
	c.page("data", items)
}

func (c *rctx) listRestores() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	items := make([]Obj, 0, len(cl.restores))
	for _, s := range cl.restores {
		c.f.advance(s, "")
		items = append(items, s.obj)
	}
	c.page("data", items)
}

func (c *rctx) createSnapshot() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	if cl.state() != "healthy" {
		c.err(http.StatusConflict, "the cluster is "+cl.state()+"; a snapshot needs a healthy cluster")
		return
	}
	regions := list(body, "regionsToCopy")
	if len(regions) > 2 {
		c.err(http.StatusUnprocessableEntity, "regionsToCopy takes at most two regions")
		return
	}
	retention := 168
	if r := intOf(body["retention"]); r > 0 {
		retention = r
	}
	copies := make([]any, 0, len(regions))
	for _, r := range regions {
		copies = append(copies, Obj{"regionCode": r, "status": "pending"})
	}
	created := c.f.clockNow()
	s := &snapshot{obj: Obj{
		"id": uuid.NewString(), "createdAt": created.Format(time.RFC3339), "type": "adhoc",
		"progress":     Obj{"status": "pending"},
		"retention":    retention,
		"expiration":   created.Add(time.Duration(retention) * time.Hour).Format(time.RFC3339),
		"databaseSize": 0, "crossRegionCopies": copies,
	}}
	cl.snapshots = append(cl.snapshots, s)
	c.writeJSON(http.StatusAccepted, Obj{"backupId": s.obj["id"]})
}

func (cl *cluster) findSnapshot(id string) (int, *snapshot) {
	for i, s := range cl.snapshots {
		if str(s.obj, "id") == id {
			return i, s
		}
	}
	return -1, nil
}

func (c *rctx) updateSnapshot() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	_, s := cl.findSnapshot(c.p["backup"])
	if s == nil {
		c.notFound("snapshot")
		return
	}
	r := intOf(body["retention"])
	if r <= 0 {
		c.err(http.StatusUnprocessableEntity, "retention must be a positive number of hours")
		return
	}
	s.obj["retention"] = r
	c.noContent()
}

func (c *rctx) deleteSnapshot() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	i, s := cl.findSnapshot(c.p["backup"])
	if s == nil {
		c.notFound("snapshot")
		return
	}
	cl.snapshots = append(cl.snapshots[:i], cl.snapshots[i+1:]...)
	// Spec documents 202 with an empty object body (not 204).
	c.writeJSON(http.StatusAccepted, Obj{})
}

func (c *rctx) restoreSnapshot() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if _, ok := c.json(); !ok {
		return
	}
	_, s := cl.findSnapshot(c.p["backup"])
	if s == nil {
		c.notFound("snapshot")
		return
	}
	if cl.state() != "healthy" {
		c.err(http.StatusConflict, "the cluster is "+cl.state()+"; a restore needs a healthy cluster")
		return
	}
	// "snapshot" is the creation time of the restored snapshot (date-time in the spec).
	r := &snapshot{obj: Obj{"id": uuid.NewString(), "snapshot": s.obj["createdAt"], "status": "pending", "createdAt": c.f.now()}}
	cl.restores = append(cl.restores, r)
	c.writeJSON(http.StatusAccepted, Obj{"restoreId": r.obj["id"]})
}

func (c *rctx) listProjectSnapshots() {
	p := c.project()
	if p == nil {
		return
	}
	items := []Obj{}
	for _, cl := range p.clusters {
		if len(cl.snapshots) == 0 {
			continue
		}
		oldest, newest := cl.snapshots[0], cl.snapshots[0]
		for _, s := range cl.snapshots {
			if str(s.obj, "createdAt") < str(oldest.obj, "createdAt") {
				oldest = s
			}
			if str(s.obj, "createdAt") > str(newest.obj, "createdAt") {
				newest = s
			}
		}
		items = append(items, Obj{
			"clusterId": cl.obj["id"], "clusterName": cl.obj["name"], "projectName": p.obj["name"],
			"region":             str(sub(cl.obj, "cloudProvider"), "region"),
			"mostRecentSnapshot": Obj{"createdAt": newest.obj["createdAt"]},
			"oldestSnapshot":     Obj{"createdAt": oldest.obj["createdAt"]},
		})
	}
	c.page("data", items)
}

// ---- bucket backups ------------------------------------------------------------------------

func (f *Fake) routeBucketBackups() {
	f.handle("GET", bktSchedURL, (*rctx).getBucketSchedule)
	f.handle("POST", bktSchedURL, (*rctx).createBucketSchedule)
	f.handle("PUT", bktSchedURL, (*rctx).updateBucketSchedule)
	f.handle("DELETE", bktSchedURL, (*rctx).deleteBucketSchedule)

	f.handle("GET", bktCyclesURL, (*rctx).listCycles)
	f.handle("GET", bktCyclesURL+"/{cycle}", (*rctx).listCycleBackups)
	f.handle("POST", bktBackupsURL, (*rctx).createBackup)
	f.handle("POST", bktBackupsURL+"/{backup}/export", (*rctx).exportBackup)
	f.handle("GET", bktBackupsURL+"/{backup}/exports/{export}", (*rctx).getExport)

	f.handle("GET", backupsURL, (*rctx).listClusterBackups)
	f.handle("GET", backupsURL+"/{backup}", (*rctx).getBackup)
	f.handle("DELETE", backupsURL+"/{backup}", (*rctx).deleteBackup)
	f.handle("POST", backupsURL+"/{backup}/restore", (*rctx).restoreBackup)
}

func (c *rctx) getBucketSchedule() {
	cl, b := c.bucket()
	if b == nil {
		return
	}
	if b.backupSched == nil {
		c.notFound("backup schedule")
		return
	}
	body := clone(b.backupSched)
	if str(body, "type") == "" {
		body["type"] = "weekly"
	}
	body["clusterID"] = cl.obj["id"]
	body["bucketId"] = b.obj["id"]
	c.writeJSON(http.StatusOK, body)
}

func (c *rctx) createBucketSchedule() {
	_, b := c.bucket()
	if b == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	if b.backupSched != nil {
		c.err(http.StatusConflict, "the bucket already has a backup schedule")
		return
	}
	b.backupSched = clone(body)
	c.w.WriteHeader(http.StatusAccepted)
}

func (c *rctx) updateBucketSchedule() {
	_, b := c.bucket()
	if b == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	if b.backupSched == nil {
		c.notFound("backup schedule")
		return
	}
	b.backupSched = clone(body)
	c.noContent()
}

func (c *rctx) deleteBucketSchedule() {
	_, b := c.bucket()
	if b == nil {
		return
	}
	if b.backupSched == nil {
		c.notFound("backup schedule")
		return
	}
	b.backupSched = nil
	c.w.WriteHeader(http.StatusAccepted)
}

func (cl *cluster) bucketBackups(bucketID string) []*backup {
	var out []*backup
	for _, b := range cl.backups {
		if str(b.obj, "bucketID") == bucketID {
			out = append(out, b)
		}
	}
	return out
}

func (c *rctx) createBackup() {
	cl, b := c.bucket()
	if b == nil {
		return
	}
	if cl.state() != "healthy" {
		c.err(http.StatusConflict, "the cluster is "+cl.state()+"; a backup needs a healthy cluster")
		return
	}
	status := ""
	if c.f.failNextBackup {
		status, c.f.failNextBackup = "failed", false
	}
	cl.backups = append(cl.backups, &backup{obj: c.f.newBackupObj(cl, b, uuid.NewString(), "full", "manual", status)})
	c.w.WriteHeader(http.StatusAccepted)
}

func (c *rctx) listCycles() {
	cl, b := c.bucket()
	if b == nil {
		return
	}
	// One entry per cycle, started at its first backup, as Capella lists them.
	cycles := []Obj{}
	at := map[string]Obj{}
	for _, bk := range cl.bucketBackups(str(b.obj, "id")) {
		id := str(bk.obj, "cycleID")
		if c, ok := at[id]; ok {
			if str(bk.obj, "date") < str(c, "createdAt") {
				c["createdAt"] = bk.obj["date"]
			}
			continue
		}
		c := Obj{"cycleID": id, "createdAt": bk.obj["date"]}
		at[id] = c
		cycles = append(cycles, c)
	}
	c.writeJSON(http.StatusOK, Obj{"data": cycles})
}

func (c *rctx) listCycleBackups() {
	cl, b := c.bucket()
	if b == nil {
		return
	}
	out := []Obj{}
	for _, bk := range cl.bucketBackups(str(b.obj, "id")) {
		if str(bk.obj, "cycleID") == c.p["cycle"] {
			out = append(out, clone(bk.obj))
		}
	}
	if len(out) == 0 {
		c.notFound("backup cycle")
		return
	}
	c.writeJSON(http.StatusOK, Obj{"data": out})
}

func (c *rctx) listClusterBackups() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	latest := map[string]*backup{}
	order := []string{}
	for _, bk := range cl.backups {
		id := str(bk.obj, "bucketID")
		if _, seen := latest[id]; !seen {
			order = append(order, id)
		}
		latest[id] = bk
	}
	out := []Obj{}
	for _, id := range order {
		out = append(out, clone(latest[id].obj))
	}
	c.writeJSON(http.StatusOK, Obj{"data": out})
}

func (cl *cluster) findBackup(id string) *backup {
	for _, b := range cl.backups {
		if str(b.obj, "id") == id {
			return b
		}
	}
	return nil
}

func (c *rctx) getBackup() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if b := cl.findBackup(c.p["backup"]); b != nil {
		c.writeJSON(http.StatusOK, clone(b.obj))
		return
	}
	c.notFound("backup")
}

// deleteBackup removes every backup in the backup's cycle, like Capella.
func (c *rctx) deleteBackup() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	b := cl.findBackup(c.p["backup"])
	if b == nil {
		c.notFound("backup")
		return
	}
	cycle := str(b.obj, "cycleID")
	cl.backups = slices.DeleteFunc(cl.backups, func(x *backup) bool { return str(x.obj, "cycleID") == cycle })
	c.w.WriteHeader(http.StatusAccepted)
}

func (c *rctx) restoreBackup() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	if cl.findBackup(c.p["backup"]) == nil {
		c.notFound("backup")
		return
	}
	for _, k := range []string{"targetClusterID", "sourceClusterID", "backupID", "services"} {
		if body[k] == nil {
			c.err(http.StatusUnprocessableEntity, k+" is required")
			return
		}
	}
	c.w.WriteHeader(http.StatusAccepted)
}

func (c *rctx) exportBackup() {
	cl, b := c.bucket()
	if b == nil {
		return
	}
	bk := cl.findBackup(c.p["backup"])
	if bk == nil {
		c.notFound("backup")
		return
	}
	if st := str(bk.obj, "status"); st != "ready" {
		c.err(http.StatusUnprocessableEntity, "the backup is "+st+"; only a backup in the ready state can be exported")
		return
	}
	cycle := str(bk.obj, "cycleID")
	for _, e := range cl.exports {
		if e.cycleID == cycle && str(e.obj, "status") != "failed" {
			c.err(http.StatusConflict, "the backup's cycle already has an export")
			return
		}
	}
	id := uuid.NewString()
	created := c.f.now()
	e := &export{cycleID: cycle, backupID: str(bk.obj, "id"), obj: Obj{
		"id": id, "exportId": id, "tenantId": c.f.OrgID, "projectId": cl.projectID, "clusterId": cl.obj["id"],
		"cycleId": cycle, "bucketName": b.obj["name"], "status": "pending", "createdAt": created,
	}}
	cl.exports = append(cl.exports, e)
	c.writeJSON(http.StatusAccepted, Obj{"exportId": id, "createdAt": created})
}

func (c *rctx) getExport() {
	cl, b := c.bucket()
	if b == nil {
		return
	}
	for _, e := range cl.exports {
		eid := str(e.obj, "id")
		if eid == "" {
			eid = str(e.obj, "exportId")
		}
		if eid != c.p["export"] || e.backupID != c.p["backup"] {
			continue
		}
		if str(e.obj, "status") == "pending" {
			if e.polls < c.f.settle {
				e.polls++
			} else {
				e.obj["status"] = "complete"
				e.obj["backupDownloadURL"] = "https://capella-fake.test/exports/" + eid
			}
		}
		c.writeJSON(http.StatusOK, clone(e.obj))
		return
	}
	c.notFound("export")
}

// newBackupObj builds a bucket-backup record with the fields the Management API schema requires.
func (f *Fake) newBackupObj(cl *cluster, b *bucket, cycleID, method, source, status string) Obj {
	if cycleID == "" {
		cycleID = uuid.NewString()
	}
	if method == "" {
		method = "full"
	}
	if source == "" {
		source = "manual"
	}
	if status == "" {
		status = "ready"
	}
	date := f.now()
	provider := str(sub(cl.obj, "cloudProvider"), "type")
	if provider == "" {
		provider = "aws"
	}
	return Obj{
		"id": uuid.NewString(), "clusterID": cl.obj["id"], "tenantID": f.OrgID, "projectID": cl.projectID,
		"cycleID": cycleID, "date": date, "restoreBefore": date, "status": status, "method": method,
		"bucketName": b.obj["name"], "bucketID": b.obj["id"], "source": source, "provider": provider,
		"stats":                Obj{"sizeInMb": 0, "items": 0, "mutations": 0, "tombstones": 0, "gsi": 0, "fts": 0, "cbas": 0, "event": 0},
		"elapsedTimeInSeconds": 0,
		"scheduleInfo":         Obj{"backupType": "weekly", "backupTime": date, "increment": 24, "retention": "30days"},
	}
}
