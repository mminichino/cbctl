package capellafake

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/secrets"
)

const (
	bucketsURL = clusterURL + "/buckets"
	bucketURL  = bucketsURL + "/{bucket}"
	scopesURL  = bucketURL + "/scopes"
	scopeURL   = scopesURL + "/{scope}"
	collsURL   = scopeURL + "/collections"
	collURL    = collsURL + "/{collection}"
	usersURL   = clusterURL + "/users"
	userURL    = usersURL + "/{user}"
	cidrsURL   = clusterURL + "/allowedcidrs"
	cidrURL    = cidrsURL + "/{cidr}"
)

// ---- allowed CIDRs -------------------------------------------------------------------------

func (f *Fake) routeCIDRs() {
	f.handle("GET", cidrsURL, (*rctx).listCIDRs)
	f.handle("POST", cidrsURL, (*rctx).createCIDR)
	f.handle("GET", cidrURL, (*rctx).getCIDR)
	f.handle("DELETE", cidrURL, (*rctx).deleteCIDR)
}

func findRes(rs []*res, id string) (int, *res) {
	for i, r := range rs {
		if str(r.obj, "id") == id {
			return i, r
		}
	}
	return -1, nil
}

func (c *rctx) listCIDRs() {
	if _, cl := c.cluster(); cl != nil {
		c.page("data", objs(cl.cidrs))
	}
}

func (c *rctx) createCIDR() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	cidr := str(body, "cidr")
	if cidr == "" {
		c.err(http.StatusUnprocessableEntity, "cidr is required")
		return
	}
	for _, e := range cl.cidrs {
		if str(e.obj, "cidr") == cidr {
			c.err(http.StatusConflict, "the CIDR "+cidr+" is already allowed")
			return
		}
	}
	kind := "permanent"
	if str(body, "expiresAt") != "" {
		kind = "temporary"
	}
	e := &res{ver: 1, obj: Obj{"id": uuid.NewString(), "cidr": cidr, "comment": str(body, "comment"),
		"expiresAt": str(body, "expiresAt"), "status": "active", "type": kind, "audit": c.f.audit(1)}}
	cl.cidrs = append(cl.cidrs, e)
	c.writeJSON(http.StatusCreated, Obj{"id": e.obj["id"]})
}

func (c *rctx) getCIDR() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	if _, e := findRes(cl.cidrs, c.p["cidr"]); e != nil {
		c.etag(e.ver)
		c.writeJSON(http.StatusOK, clone(e.obj))
		return
	}
	c.notFound("allowed CIDR")
}

func (c *rctx) deleteCIDR() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	i, e := findRes(cl.cidrs, c.p["cidr"])
	if e == nil {
		c.notFound("allowed CIDR")
		return
	}
	cl.cidrs = append(cl.cidrs[:i], cl.cidrs[i+1:]...)
	c.noContent()
}

// ---- buckets -------------------------------------------------------------------------------

func (f *Fake) routeBuckets() {
	f.handle("GET", bucketsURL, (*rctx).listBuckets)
	f.handle("POST", bucketsURL, (*rctx).createBucket)
	f.handle("GET", bucketURL, (*rctx).getBucket)
	f.handle("PUT", bucketURL, (*rctx).updateBucket)
	f.handle("DELETE", bucketURL, (*rctx).deleteBucket)

	f.handle("GET", scopesURL, (*rctx).listScopes)
	f.handle("POST", scopesURL, (*rctx).createScope)
	f.handle("GET", scopeURL, (*rctx).getScope)
	f.handle("DELETE", scopeURL, (*rctx).deleteScope)

	f.handle("GET", collsURL, (*rctx).listCollections)
	f.handle("POST", collsURL, (*rctx).createCollection)
	f.handle("GET", collURL, (*rctx).getCollection)
	f.handle("PUT", collURL, (*rctx).updateCollection)
	f.handle("DELETE", collURL, (*rctx).deleteCollection)
}

func (f *Fake) routeFreeBuckets() {
	f.handle("GET", bucketsURL+"/freeTier", (*rctx).listFreeBuckets)
	f.handle("POST", bucketsURL+"/freeTier", (*rctx).createFreeBucket)
	f.handle("GET", bucketsURL+"/freeTier/{bucket}", (*rctx).getBucket)
	f.handle("PUT", bucketsURL+"/freeTier/{bucket}", (*rctx).updateBucket)
	f.handle("DELETE", bucketsURL+"/freeTier/{bucket}", (*rctx).deleteBucket)
}

// bucketName decodes a base64url bucket ID (unpadded, or padded for leniency) to the name.
func bucketName(id string) (string, bool) {
	if b, err := base64.RawURLEncoding.DecodeString(id); err == nil {
		return string(b), true
	}
	if b, err := base64.URLEncoding.DecodeString(id); err == nil {
		return string(b), true
	}
	return "", false
}

func (cl *cluster) findBucket(id string) *bucket {
	name, ok := bucketName(id)
	if !ok {
		return nil
	}
	for _, b := range cl.buckets {
		if str(b.obj, "name") == name {
			return b
		}
	}
	return nil
}

func (cl *cluster) bucketByName(name string) *bucket {
	for _, b := range cl.buckets {
		if str(b.obj, "name") == name {
			return b
		}
	}
	return nil
}

func (c *rctx) bucket() (*cluster, *bucket) {
	_, cl := c.cluster()
	if cl == nil {
		return nil, nil
	}
	b := cl.findBucket(c.p["bucket"])
	if b == nil {
		c.notFound("bucket")
		return nil, nil
	}
	return cl, b
}

func (c *rctx) listBuckets() {
	if _, cl := c.cluster(); cl != nil {
		items := make([]Obj, 0, len(cl.buckets))
		for _, b := range cl.buckets {
			items = append(items, b.obj)
		}
		c.page("data", items)
	}
}

func (c *rctx) listFreeBuckets() {
	_, cl := c.freeCluster()
	if cl == nil {
		return
	}
	items := make([]Obj, 0, len(cl.buckets))
	for _, b := range cl.buckets {
		items = append(items, clone(b.obj))
	}
	c.writeJSON(http.StatusOK, Obj{"data": items, "clusterStats": cl.stats()})
}

// addBucket creates the bucket with its _default scope and collection.
func (cl *cluster) addBucket(name string, fields Obj) *bucket {
	obj := Obj{
		"id":                           capellaapi.BucketID(name),
		"name":                         name,
		"type":                         "couchbase",
		"storageBackend":               "couchstore",
		"memoryAllocationInMb":         100,
		"bucketConflictResolution":     "seqno",
		"durabilityLevel":              "none",
		"replicas":                     1,
		"flush":                        false,
		"timeToLiveInSeconds":          0,
		"evictionPolicy":               "fullEviction",
		"priority":                     0,
		"enableCrossClusterVersioning": false,
		"stats":                        Obj{"itemCount": 0, "opsPerSecond": 0, "diskUsedInMib": 0, "memoryUsedInMib": 0},
	}
	for k, v := range fields {
		if k != "id" && k != "name" && k != "stats" {
			obj[k] = v
		}
	}
	b := &bucket{res: res{obj: obj, ver: 1}}
	b.scopes = []*scope{{name: "_default", colls: []*res{{ver: 1, obj: Obj{"name": "_default"}}}}}
	cl.buckets = append(cl.buckets, b)
	return b
}

func (c *rctx) createBucket() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	if intOf(body["memoryAllocationInMb"]) != 0 && intOf(body["memoryAllocationInMb"]) < 100 {
		c.err(http.StatusUnprocessableEntity, "memoryAllocationInMb must be at least 100")
		return
	}
	if cl.bucketByName(name) != nil {
		c.err(http.StatusConflict, "a bucket named "+strconv.Quote(name)+" already exists")
		return
	}
	b := cl.addBucket(name, body)
	c.writeJSON(http.StatusCreated, Obj{"id": b.obj["id"]})
}

func (c *rctx) createFreeBucket() {
	_, cl := c.freeCluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	if cl.bucketByName(name) != nil {
		c.err(http.StatusConflict, "a bucket named "+strconv.Quote(name)+" already exists")
		return
	}
	fields := Obj{}
	if mem := intOf(body["memoryAllocationInMb"]); mem > 0 {
		fields["memoryAllocationInMb"] = mem
	}
	b := cl.addBucket(name, fields)
	c.writeJSON(http.StatusCreated, Obj{"id": b.obj["id"]})
}

func (c *rctx) getBucket() {
	if _, b := c.bucket(); b != nil {
		c.etag(b.ver)
		c.writeJSON(http.StatusOK, clone(b.obj))
	}
}

// immutable are bucket fields an update cannot change.
var immutable = map[string]bool{
	"id": true, "name": true, "type": true, "storageBackend": true, "bucketConflictResolution": true,
	"evictionPolicy": true, "stats": true,
}

func (c *rctx) updateBucket() {
	_, b := c.bucket()
	if b == nil {
		return
	}
	body, ok := c.json()
	if !ok || !c.matches(b.ver) {
		return
	}
	for k, v := range body {
		if !immutable[k] {
			b.obj[k] = v
		}
	}
	b.touch(c.f)
	c.noContent()
}

func (c *rctx) deleteBucket() {
	cl, b := c.bucket()
	if b == nil {
		return
	}
	for i, x := range cl.buckets {
		if x == b {
			cl.buckets = append(cl.buckets[:i], cl.buckets[i+1:]...)
			break
		}
	}
	c.noContent()
}

// ---- scopes and collections ----------------------------------------------------------------

func (b *bucket) findScope(name string) *scope {
	for _, s := range b.scopes {
		if s.name == name {
			return s
		}
	}
	return nil
}

func (s *scope) findCollection(name string) (int, *res) {
	for i, r := range s.colls {
		if str(r.obj, "name") == name {
			return i, r
		}
	}
	return -1, nil
}

func (s *scope) obj() Obj {
	colls := make([]any, 0, len(s.colls))
	for _, r := range s.colls {
		colls = append(colls, clone(r.obj))
	}
	return Obj{"name": s.name, "collections": colls}
}

func (c *rctx) scope() (*bucket, *scope) {
	_, b := c.bucket()
	if b == nil {
		return nil, nil
	}
	s := b.findScope(c.p["scope"])
	if s == nil {
		c.notFound("scope")
		return nil, nil
	}
	return b, s
}

func (c *rctx) listScopes() {
	_, b := c.bucket()
	if b == nil {
		return
	}
	items := make([]Obj, 0, len(b.scopes))
	for _, s := range b.scopes {
		items = append(items, s.obj())
	}
	c.page("scopes", items)
}

func (c *rctx) createScope() {
	_, b := c.bucket()
	if b == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	if b.findScope(name) != nil {
		c.err(http.StatusConflict, "a scope named "+strconv.Quote(name)+" already exists")
		return
	}
	b.scopes = append(b.scopes, &scope{name: name})
	// Spec documents 201 with no body.
	c.w.WriteHeader(http.StatusCreated)
}

func (c *rctx) getScope() {
	if _, s := c.scope(); s != nil {
		c.etag(1)
		c.writeJSON(http.StatusOK, s.obj())
	}
}

func (c *rctx) deleteScope() {
	b, s := c.scope()
	if s == nil {
		return
	}
	if s.name == "_default" || s.name == "_system" {
		c.err(http.StatusUnprocessableEntity, "the "+s.name+" scope cannot be deleted")
		return
	}
	for i, x := range b.scopes {
		if x == s {
			b.scopes = append(b.scopes[:i], b.scopes[i+1:]...)
			break
		}
	}
	c.noContent()
}

func (c *rctx) listCollections() {
	_, s := c.scope()
	if s == nil {
		return
	}
	c.page("data", objs(s.colls))
}

func (c *rctx) createCollection() {
	_, s := c.scope()
	if s == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	if _, r := s.findCollection(name); r != nil {
		c.err(http.StatusConflict, "a collection named "+strconv.Quote(name)+" already exists")
		return
	}
	obj := Obj{"name": name}
	if v, ok := body["maxTTL"]; ok {
		obj["maxTTL"] = v
	}
	s.colls = append(s.colls, &res{ver: 1, obj: obj})
	// Spec documents 201 with no body.
	c.w.WriteHeader(http.StatusCreated)
}

func (c *rctx) collection() (*scope, int, *res) {
	_, s := c.scope()
	if s == nil {
		return nil, 0, nil
	}
	i, r := s.findCollection(c.p["collection"])
	if r == nil {
		c.notFound("collection")
		return nil, 0, nil
	}
	return s, i, r
}

func (c *rctx) getCollection() {
	if _, _, r := c.collection(); r != nil {
		c.etag(r.ver)
		c.writeJSON(http.StatusOK, clone(r.obj))
	}
}

func (c *rctx) updateCollection() {
	_, _, r := c.collection()
	if r == nil {
		return
	}
	body, ok := c.json()
	if !ok || !c.matches(r.ver) {
		return
	}
	if v, ok := body["maxTTL"]; ok {
		r.obj["maxTTL"] = v
	}
	r.ver++
	c.noContent()
}

func (c *rctx) deleteCollection() {
	s, i, r := c.collection()
	if r == nil {
		return
	}
	if str(r.obj, "name") == "_default" {
		c.err(http.StatusUnprocessableEntity, "the _default collection cannot be deleted")
		return
	}
	s.colls = append(s.colls[:i], s.colls[i+1:]...)
	c.noContent()
}

// ---- database credentials ------------------------------------------------------------------

func (f *Fake) routeUsers() {
	f.handle("GET", usersURL, (*rctx).listUsers)
	f.handle("POST", usersURL, (*rctx).createUser)
	f.handle("GET", userURL, (*rctx).getUser)
	f.handle("PUT", userURL, (*rctx).updateUser)
	f.handle("DELETE", userURL, (*rctx).deleteUser)
}

func (c *rctx) listUsers() {
	if _, cl := c.cluster(); cl != nil {
		c.page("data", objs(cl.creds))
	}
}

// createUser stores a credential. When the request has no password the Fake generates one and
// returns it once; a supplied password must meet Capella's rules and is never returned.
func (c *rctx) createUser() {
	_, cl := c.cluster()
	if cl == nil {
		return
	}
	body, ok := c.json()
	if !ok {
		return
	}
	name := str(body, "name")
	if name == "" {
		c.err(http.StatusUnprocessableEntity, "name is required")
		return
	}
	for _, u := range cl.creds {
		if str(u.obj, "name") == name {
			c.err(http.StatusConflict, "a credential named "+strconv.Quote(name)+" already exists")
			return
		}
	}
	pw := str(body, "password")
	generated := pw == ""
	if generated {
		pw = secrets.MustGenerate(32)
	} else if !secrets.MeetsRules(pw) {
		c.err(http.StatusUnprocessableEntity, "password does not meet the rules: "+secrets.Rules)
		return
	}
	access, _ := body["access"].([]any)
	if access == nil {
		access = []any{}
	}
	u := &res{ver: 1, obj: Obj{"id": uuid.NewString(), "name": name, "access": access, "audit": c.f.audit(1)}}
	cl.creds = append(cl.creds, u)
	out := Obj{"id": u.obj["id"]}
	if generated {
		out["password"] = pw
	}
	c.writeJSON(http.StatusCreated, out)
}

func (c *rctx) user() (*cluster, int, *res) {
	_, cl := c.cluster()
	if cl == nil {
		return nil, 0, nil
	}
	i, u := findRes(cl.creds, c.p["user"])
	if u == nil {
		c.notFound("credential")
		return nil, 0, nil
	}
	return cl, i, u
}

func (c *rctx) getUser() {
	if _, _, u := c.user(); u != nil {
		c.etag(u.ver)
		c.writeJSON(http.StatusOK, clone(u.obj))
	}
}

func (c *rctx) updateUser() {
	_, _, u := c.user()
	if u == nil {
		return
	}
	body, ok := c.json()
	if !ok || !c.matches(u.ver) {
		return
	}
	if pw := str(body, "password"); pw != "" && !secrets.MeetsRules(pw) {
		c.err(http.StatusUnprocessableEntity, "password does not meet the rules: "+secrets.Rules)
		return
	}
	if access, ok := body["access"].([]any); ok {
		u.obj["access"] = access
	}
	u.touch(c.f)
	c.noContent()
}

func (c *rctx) deleteUser() {
	cl, i, u := c.user()
	if u == nil {
		return
	}
	cl.creds = append(cl.creds[:i], cl.creds[i+1:]...)
	c.noContent()
}
