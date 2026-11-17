package capellafake

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/mminichino/cbctl/internal/engine"
)

const (
	orgPath     = "/v4/organizations/{org}"
	projectsURL = orgPath + "/projects"
	projectURL  = projectsURL + "/{project}"
	clustersURL = projectURL + "/clusters"
	clusterURL  = clustersURL + "/{cluster}"
	freeURL     = clustersURL + "/freeTier"
)

// routeAll registers every endpoint. Literal path segments come before wildcards.
func (f *Fake) routeAll() {
	f.handleAnyOrg("GET", "/v4/organizations", (*rctx).listOrgs)
	f.handleAnyOrg("GET", orgPath, (*rctx).getOrg)

	f.handle("GET", projectsURL, (*rctx).listProjects)
	f.handle("POST", projectsURL, (*rctx).createProject)
	f.handle("GET", projectURL, (*rctx).getProject)
	f.handle("PUT", projectURL, (*rctx).updateProject)
	f.handle("DELETE", projectURL, (*rctx).deleteProject)

	// Free tier first: "freeTier" would otherwise match the {cluster} wildcard.
	f.handle("POST", freeURL, (*rctx).createFreeCluster)
	f.handle("GET", freeURL+"/{cluster}", (*rctx).getFreeCluster)
	f.handle("PUT", freeURL+"/{cluster}", (*rctx).updateFreeCluster)
	f.handle("DELETE", freeURL+"/{cluster}", (*rctx).deleteCluster)
	f.handle("POST", freeURL+"/{cluster}/activationState", (*rctx).turnOn)
	f.handle("DELETE", freeURL+"/{cluster}/activationState", (*rctx).turnOff)
	f.routeFreeBuckets()

	f.handle("GET", clustersURL, (*rctx).listClusters)
	f.handle("POST", clustersURL, (*rctx).createCluster)
	f.handle("GET", clusterURL, (*rctx).getCluster)
	f.handle("PUT", clusterURL, (*rctx).updateCluster)
	f.handle("DELETE", clusterURL, (*rctx).deleteCluster)
	f.handle("PUT", clusterURL+"/deletionProtection", (*rctx).setProtection)
	f.handle("POST", clusterURL+"/activationState", (*rctx).turnOn)
	f.handle("DELETE", clusterURL+"/activationState", (*rctx).turnOff)
	f.handle("GET", clusterURL+"/stats", (*rctx).clusterStats)

	f.routeCIDRs()
	f.routeBuckets()
	f.routeUsers()
	f.routeSchedule()
	f.routeSnapshots()
	f.routeBucketBackups()
}

// ---- organizations -------------------------------------------------------------------------

func (c *rctx) listOrgs() { c.page("data", c.f.orgs) }

func (c *rctx) getOrg() {
	for _, o := range c.f.orgs {
		if str(o, "id") == c.p["org"] {
			c.writeJSON(http.StatusOK, clone(o))
			return
		}
	}
	c.notFound("organization")
}

// ---- projects ------------------------------------------------------------------------------

func (f *Fake) findProject(id string) *project {
	for _, p := range f.projects {
		if str(p.obj, "id") == id {
			return p
		}
	}
	return nil
}

func (f *Fake) newProject(name, desc string) *project {
	p := &project{res: res{ver: 1, obj: Obj{"id": uuid.NewString(), "name": name, "description": desc, "audit": f.audit(1)}}}
	f.projects = append(f.projects, p)
	return p
}

func (c *rctx) project() *project {
	p := c.f.findProject(c.p["project"])
	if p == nil {
		c.notFound("project")
	}
	return p
}

func (c *rctx) listProjects() {
	items := make([]Obj, 0, len(c.f.projects))
	for _, p := range c.f.projects {
		items = append(items, p.obj)
	}
	c.page("data", items)
}

func (c *rctx) createProject() {
	body, ok := c.json()
	if !ok {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	p := c.f.newProject(name, str(body, "description"))
	c.writeJSON(http.StatusCreated, Obj{"id": p.obj["id"]})
}

func (c *rctx) getProject() {
	if p := c.project(); p != nil {
		c.etag(p.ver)
		c.writeJSON(http.StatusOK, clone(p.obj))
	}
}

func (c *rctx) updateProject() {
	p := c.project()
	if p == nil {
		return
	}
	body, ok := c.json()
	if !ok || !c.matches(p.ver) {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	p.obj["name"] = name
	p.obj["description"] = str(body, "description")
	p.touch(c.f)
	c.noContent()
}

func (c *rctx) deleteProject() {
	p := c.project()
	if p == nil {
		return
	}
	if len(p.clusters) > 0 {
		c.err(http.StatusConflict, "the project still contains clusters")
		return
	}
	for i, q := range c.f.projects {
		if q == p {
			c.f.projects = append(c.f.projects[:i], c.f.projects[i+1:]...)
			break
		}
	}
	c.noContent()
}

// ---- clusters ------------------------------------------------------------------------------

func (f *Fake) findClusterAnywhere(id string) (*project, *cluster) {
	for _, p := range f.projects {
		if cl := p.findCluster(id); cl != nil {
			return p, cl
		}
	}
	return nil, nil
}

func (p *project) findCluster(id string) *cluster {
	for _, cl := range p.clusters {
		if str(cl.obj, "id") == id {
			return cl
		}
	}
	return nil
}

func (p *project) removeCluster(cl *cluster) {
	for i, x := range p.clusters {
		if x == cl {
			p.clusters = append(p.clusters[:i], p.clusters[i+1:]...)
			return
		}
	}
}

// newCluster adds a cluster in "deploying" that becomes healthy after the settle polls.
func (f *Fake) newCluster(p *project, body Obj, free bool) *cluster {
	id := uuid.NewString()
	groups := list(body, "serviceGroups")
	nodes := 0
	for _, g := range groups {
		if gm, ok := g.(map[string]any); ok {
			nodes += intOf(gm["numOfNodes"])
		}
	}
	cfg := "multiNode"
	if nodes == 1 {
		cfg = "singleNode"
	}
	obj := Obj{
		"id":                 id,
		"name":               str(body, "name"),
		"description":        str(body, "description"),
		"configurationType":  cfg,
		"connectionString":   "cb." + id[:8] + ".cloud.couchbase.com",
		"cloudProvider":      clone(sub(body, "cloudProvider")),
		"couchbaseServer":    Obj{"version": "7.6.2"},
		"serviceGroups":      groups,
		"availability":       clone(sub(body, "availability")),
		"support":            clone(sub(body, "support")),
		"currentState":       "deploying",
		"deletionProtection": false,
		"audit":              f.audit(1),
	}
	if v := sub(body, "couchbaseServer"); v != nil && str(v, "version") != "" {
		obj["couchbaseServer"] = clone(v)
	}
	// Create-only options Capella echoes back on GET.
	for _, k := range []string{"zones", "cmekId", "enablePrivateDNSResolution"} {
		if v, ok := body[k]; ok {
			obj[k] = v
		}
	}
	if sub(obj, "cloudProvider")["cidr"] == nil {
		sub(obj, "cloudProvider")["cidr"] = "10.0.0.0/20"
	}
	cl := &cluster{res: res{obj: obj, ver: 1}, projectID: str(p.obj, "id"), free: free, target: "healthy"}
	if f.failDeploy {
		cl.target = "deploymentFailed"
		f.failDeploy = false
	}
	p.clusters = append(p.clusters, cl)
	return cl
}

func (c *rctx) cluster() (*project, *cluster) {
	p := c.project()
	if p == nil {
		return nil, nil
	}
	cl := p.findCluster(c.p["cluster"])
	if cl == nil {
		c.notFound("cluster")
		return nil, nil
	}
	return p, cl
}

// freeCluster is cluster() restricted to free-tier clusters.
func (c *rctx) freeCluster() (*project, *cluster) {
	p, cl := c.cluster()
	if cl != nil && !cl.free {
		c.notFound("free-tier cluster")
		return nil, nil
	}
	return p, cl
}

func (cl *cluster) state() string { return str(cl.obj, "currentState") }

// transition starts a state change that settles on target after the configured polls.
func (cl *cluster) transition(state, target string) {
	cl.obj["currentState"] = state
	cl.target = target
	cl.polls = 0
}

// poll advances a transitional cluster by one poll and reports whether it has gone away.
func (f *Fake) poll(p *project, cl *cluster) (gone bool) {
	if len(cl.path) > 0 && cl.pathStep > 0 && f.clock != nil {
		return f.pollPath(p, cl)
	}
	if cl.target == "" {
		return false
	}
	if cl.polls < f.settle {
		cl.polls++
		return false
	}
	target := cl.target
	cl.target = ""
	if target == "deleted" {
		p.removeCluster(cl)
		return true
	}
	cl.obj["currentState"] = target
	return false
}

// pollPath advances a SetTransition clock-driven path.
func (f *Fake) pollPath(p *project, cl *cluster) (gone bool) {
	elapsed := f.clock.Now().Sub(cl.pathStart)
	idx := 0
	if cl.pathStep > 0 {
		idx = int(elapsed / cl.pathStep)
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cl.path) {
		idx = len(cl.path) - 1
	}
	state := cl.path[idx]
	if state == "deleted" {
		cl.path = nil
		p.removeCluster(cl)
		return true
	}
	cl.obj["currentState"] = state
	if idx == len(cl.path)-1 {
		cl.path = nil
		cl.pathStep = 0
		cl.target = ""
	}
	return false
}

func (c *rctx) listClusters() {
	p := c.project()
	if p == nil {
		return
	}
	items := make([]Obj, 0, len(p.clusters))
	for _, cl := range p.clusters {
		items = append(items, cl.obj)
	}
	c.page("data", items)
}

func (c *rctx) createCluster() {
	p := c.project()
	if p == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	switch {
	case str(body, "name") == "":
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	case str(sub(body, "cloudProvider"), "type") == "":
		c.err(http.StatusUnprocessableEntity, "cloudProvider.type is required")
		return
	case len(list(body, "serviceGroups")) == 0:
		c.err(http.StatusUnprocessableEntity, "at least one service group is required")
		return
	}
	cl := c.f.newCluster(p, body, false)
	c.writeJSON(http.StatusAccepted, Obj{"id": cl.obj["id"]})
}

func (c *rctx) getCluster() {
	p, cl := c.cluster()
	if cl == nil {
		return
	}
	if c.f.poll(p, cl) {
		c.notFound("cluster")
		return
	}
	c.etag(cl.ver)
	c.writeJSON(http.StatusOK, clone(cl.obj))
}

func (c *rctx) getFreeCluster() {
	p, cl := c.freeCluster()
	if cl == nil {
		return
	}
	if c.f.poll(p, cl) {
		c.notFound("cluster")
		return
	}
	c.etag(cl.ver)
	c.writeJSON(http.StatusOK, clone(cl.obj))
}

// busy answers 409 when the cluster is mid-operation and reports whether it did.
func (c *rctx) busy(cl *cluster) bool {
	if engine.Transitional[cl.state()] {
		c.err(http.StatusConflict, "the cluster is "+cl.state()+"; wait for the operation to finish")
		return true
	}
	return false
}

func (c *rctx) updateCluster() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok || !c.matches(cl.ver) || c.busy(cl) {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	cl.obj["name"] = name
	cl.obj["description"] = str(body, "description")
	if s := sub(body, "support"); s != nil {
		cl.obj["support"] = clone(s)
	}
	if groups := list(body, "serviceGroups"); len(groups) > 0 && !same(groups, cl.obj["serviceGroups"]) {
		cl.obj["serviceGroups"] = groups
		cl.transition("scaling", "healthy")
	}
	cl.touch(c.f)
	c.noContent()
}

func (c *rctx) updateFreeCluster() {
	_, cl := c.freeCluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok || !c.matches(cl.ver) {
		return
	}
	if name := str(body, "name"); name != "" {
		cl.obj["name"] = name
	}
	cl.obj["description"] = str(body, "description")
	cl.touch(c.f)
	c.noContent()
}

func (c *rctx) deleteCluster() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.state() == "destroying" {
		c.err(http.StatusConflict, "the cluster is already being deleted")
		return
	}
	if c.busy(cl) {
		return
	}
	if on, _ := cl.obj["deletionProtection"].(bool); on {
		c.err(http.StatusConflict, "the cluster has deletion protection on")
		return
	}
	target := "deleted"
	if cl.failDestroy {
		target = "destroyFailed"
		cl.failDestroy = false
	}
	cl.transition("destroying", target)
	c.w.WriteHeader(http.StatusAccepted)
}

func (c *rctx) setProtection() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	on, isBool := body["deletionProtection"].(bool)
	if !isBool {
		c.err(http.StatusUnprocessableEntity, "deletionProtection must be a boolean")
		return
	}
	cl.obj["deletionProtection"] = on
	c.noContent()
}

func (c *rctx) turnOn() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.state() != "turnedOff" {
		c.err(http.StatusConflict, "the cluster is "+cl.state()+", not turnedOff")
		return
	}
	cl.transition("turningOn", "healthy")
	c.w.WriteHeader(http.StatusAccepted)
}

func (c *rctx) turnOff() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if cl.state() != "healthy" {
		c.err(http.StatusConflict, "the cluster is "+cl.state()+", not healthy")
		return
	}
	cl.transition("turningOff", "turnedOff")
	c.w.WriteHeader(http.StatusAccepted)
}

func (c *rctx) clusterStats() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	c.writeJSON(http.StatusOK, cl.stats())
}

// stats derives a capacity summary: half of each data node's RAM is the bucket quota.
func (cl *cluster) stats() Obj {
	dataNodes, ramGB := 0, 0
	for _, g := range list(cl.obj, "serviceGroups") {
		gm, _ := g.(map[string]any)
		hasData := false
		for _, s := range list(gm, "services") {
			if s == "data" {
				hasData = true
			}
		}
		if hasData {
			dataNodes += intOf(gm["numOfNodes"])
			ramGB = intOf(sub(sub(gm, "node"), "compute")["ram"])
		}
	}
	total := dataNodes * ramGB * 1024 / 2
	used := 0
	for _, b := range cl.buckets {
		used += intOf(b.obj["memoryAllocationInMb"])
	}
	return Obj{"freeMemoryInMb": max(total-used, 0), "totalMemoryInMb": total, "maxReplicas": min(max(dataNodes-1, 0), 3)}
}

// ---- free tier -----------------------------------------------------------------------------

func (c *rctx) createFreeCluster() {
	p := c.project()
	if p == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	if str(body, "name") == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	for _, q := range c.f.projects {
		for _, cl := range q.clusters {
			if cl.free {
				c.err(http.StatusUnprocessableEntity, "the organization already has a free-tier cluster")
				return
			}
		}
	}
	provider := clone(sub(body, "cloudProvider"))
	if provider == nil {
		provider = Obj{"type": "aws", "region": "us-east-1"}
	}
	cl := c.f.newCluster(p, Obj{
		"name":          str(body, "name"),
		"description":   str(body, "description"),
		"cloudProvider": provider,
		"serviceGroups": []any{Obj{"numOfNodes": 1, "services": []any{"data", "index", "query", "search"},
			"node": Obj{"compute": Obj{"cpu": 2, "ram": 8}, "disk": Obj{"type": "gp3", "storage": 50, "iops": 3000}}}},
		"availability": Obj{"type": "single"},
		"support":      Obj{"plan": "free", "timezone": "PT"},
	}, true)
	c.writeJSON(http.StatusAccepted, Obj{"id": cl.obj["id"]})
}
