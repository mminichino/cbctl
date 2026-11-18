package manifest

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	srvHead = "apiVersion: cbctl/v1\nkind: CouchbaseServer\nmetadata:\n  name: test\nspec:\n"
	capHead = "apiVersion: cbctl/v1\nkind: CouchbaseCapella\nmetadata:\n  name: test\nspec:\n  project: p\n"
	srvBase = "credentials:\n  password: ${PW}\nnodes:\n  - ipAddress: 10.0.0.1\n"
)

func indent(body string) string {
	body = strings.TrimRight(body, "\n")
	return "  " + strings.ReplaceAll(body, "\n", "\n  ") + "\n"
}

// srv builds a CouchbaseServer manifest from spec lines.
func srv(spec string) string { return srvHead + indent(spec) }

// srvWith appends spec lines to a valid minimal Server spec.
func srvWith(extra string) string { return srv(srvBase + extra) }

// cap builds a CouchbaseCapella manifest from spec lines (project p is added).
func capella(spec string) string { return capHead + indent(spec) }

// capSmall is a valid small cluster plus extra spec lines.
func capSmall(extra string) string { return capella("size: small\n" + extra) }

func parse(t *testing.T, text string) (*Set, *ValidationError) {
	t.Helper()
	set, err := Parse(context.Background(), strings.NewReader(text), "m.yaml", "testdata/valid")
	if err == nil {
		return set, nil
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	if set != nil {
		t.Fatal("documents returned together with an error")
	}
	return nil, ve
}

type rule struct {
	name string
	doc  string
	// ptr and msg must both match one issue; ptr may be empty to match any pointer.
	ptr, msg string
}

func runRules(t *testing.T, rules []rule) {
	t.Helper()
	for _, r := range rules {
		t.Run(r.name, func(t *testing.T) {
			_, ve := parse(t, r.doc)
			if ve == nil {
				t.Fatalf("manifest accepted, want an issue %q at %q\n%s", r.msg, r.ptr, r.doc)
			}
			for _, is := range ve.Issues {
				if strings.Contains(is.Msg, r.msg) && (r.ptr == "" || is.Pointer == r.ptr) {
					if is.Line == 0 {
						t.Errorf("issue has no line: %s", is)
					}
					return
				}
			}
			t.Errorf("no issue %q at %q in:\n%s", r.msg, r.ptr, ve)
		})
	}
}

func runValid(t *testing.T, docs map[string]string) {
	t.Helper()
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			if _, ve := parse(t, doc); ve != nil {
				t.Fatalf("manifest rejected:\n%s\n%s", ve, doc)
			}
		})
	}
}

func TestEnvelopeRules(t *testing.T) {
	runRules(t, []rule{
		{"apiVersion missing", "kind: CouchbaseServer\nmetadata: {name: x}\nspec: {}\n", "/apiVersion", "apiVersion is required"},
		{"apiVersion wrong", "apiVersion: cbctl/v2\nkind: CouchbaseServer\nmetadata: {name: x}\nspec: {}\n", "/apiVersion", "not supported"},
		{"kind missing is capella.yaml", "apiVersion: cbctl/v1\nprojects: []\n", "/kind", "looks like a capella.yaml"},
		{"kind unknown", "apiVersion: cbctl/v1\nkind: Couchbase\nmetadata: {name: x}\nspec: {}\n", "/kind", "unknown kind"},
		{"kind case sensitive", "apiVersion: cbctl/v1\nkind: couchbaseserver\nmetadata: {name: x}\nspec: {}\n", "/kind", "did you mean CouchbaseServer"},
		{"spec missing", "apiVersion: cbctl/v1\nkind: CouchbaseServer\nmetadata: {name: x}\n", "", "spec is required"},
		{"name missing", "apiVersion: cbctl/v1\nkind: CouchbaseServer\nspec:\n" + indent(srvBase), "/metadata/name", "name is required"},
		{"name upper case", strings.Replace(srv(srvBase), "name: test", "name: Test", 1), "/metadata/name", "kebab case"},
		{"name underscore", strings.Replace(srv(srvBase), "name: test", "name: a_b", 1), "/metadata/name", "kebab case"},
		{"name leading dash", strings.Replace(srv(srvBase), "name: test", "name: -a", 1), "/metadata/name", "kebab case"},
		{"name trailing dash", strings.Replace(srv(srvBase), "name: test", "name: a-", 1), "/metadata/name", "kebab case"},
		{"name too long", strings.Replace(srv(srvBase), "name: test", "name: "+strings.Repeat("a", 64), 1), "/metadata/name", "limit is 63"},
		{"unknown top-level field", srv(srvBase) + "extra: 1\n", "/extra", "unknown field"},
		{"unknown metadata field", strings.Replace(srv(srvBase), "name: test", "name: test\n  nmae: x", 1), "/metadata/nmae", "unknown field"},
		{"labels must be strings", strings.Replace(srv(srvBase), "name: test", "name: test\n  labels: {a: [1]}", 1), "", "cannot use"},
	})
	runValid(t, map[string]string{
		"name one char":    strings.Replace(srv(srvBase), "name: test", "name: a", 1),
		"name 63 chars":    strings.Replace(srv(srvBase), "name: test", "name: "+strings.Repeat("a", 63), 1),
		"name with digits": strings.Replace(srv(srvBase), "name: test", "name: 0-a-1", 1),
	})
}

func TestServerRules(t *testing.T) {
	runRules(t, []rule{
		{"no nodes", srv("credentials:\n  password: ${PW}\n"), "/spec/nodes", "at least one node"},
		{"empty nodes", srv("credentials:\n  password: ${PW}\nnodes: []\n"), "/spec/nodes", "at least one node"},
		{"two primaries", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, primary: true}\n  - {ipAddress: 10.0.0.2, primary: true}\n"),
			"/spec/nodes/1/primary", "only one node can be primary"},
		{"primary is arbiter", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1}\n  - {ipAddress: 10.0.0.2, primary: true, arbiter: true}\n"),
			"/spec/nodes/1/primary", "cannot be an arbiter"},
		{"first node arbiter and no primary", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, arbiter: true}\n  - {ipAddress: 10.0.0.2}\n"),
			"/spec/nodes/0", "it is an arbiter"},
		{"no data node", srv("credentials:\n  password: ${PW}\nnodeDefaults: {services: [index, query]}\nnodes:\n  - {ipAddress: 10.0.0.1}\n"),
			"/spec/nodes", "data service"},
		{"duplicate ip", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1}\n  - {ipAddress: 10.0.0.1}\n"),
			"/spec/nodes/1/ipAddress", "must be unique"},
		{"duplicate ip case-insensitive host", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: Db1.example.com}\n  - {ipAddress: db1.example.com}\n"),
			"/spec/nodes/1/ipAddress", "must be unique"},
		{"ip missing", srv("credentials:\n  password: ${PW}\nnodes:\n  - {serverGroup: a}\n"), "/spec/nodes/0/ipAddress", "ipAddress is required"},
		{"ip invalid", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 'a b'}\n"), "/spec/nodes/0/ipAddress", "not an IP address"},
		{"ip bad port", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: '10.0.0.1:0'}\n"), "/spec/nodes/0/ipAddress", "invalid port"},
		{"external ip invalid", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, externalIpAddress: '/x'}\n"),
			"/spec/nodes/0/externalIpAddress", "not an IP address"},
		{"arbiter with services", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1}\n  - {ipAddress: 10.0.0.2, arbiter: true, services: [data]}\n"),
			"/spec/nodes/1/services", "arbiter cannot be combined with services"},
		{"arbiter with empty services list", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1}\n  - {ipAddress: 10.0.0.2, arbiter: true, services: []}\n"),
			"/spec/nodes/1/services", "arbiter cannot be combined with services"},
		{"node ramGiB zero", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, ramGiB: 0}\n"), "/spec/nodes/0/ramGiB", "greater than 0"},
		{"node ramGiB negative", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, ramGiB: -1}\n"), "/spec/nodes/0/ramGiB", "greater than 0"},
		{"default ramGiB zero", srv("credentials:\n  password: ${PW}\nnodeDefaults: {ramGiB: 0}\nnodes:\n  - {ipAddress: 10.0.0.1}\n"), "/spec/nodeDefaults/ramGiB", "greater than 0"},
		{"ramGiB fraction", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, ramGiB: 1.5}\n"), "/spec/nodes/0/ramGiB", "expected an integer"},
		{"service unknown", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, services: [data, quary]}\n"), "/spec/nodes/0/services/1", "did you mean query"},
		{"empty services", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, services: []}\n"), "/spec/nodes/0/services", "must not be empty"},
		{"quotaMiB 99", srvWith("buckets:\n  - {name: b, quotaMiB: 99}\n"), "/spec/buckets/0/quotaMiB", "at least 100"},
		{"replicas 4", srvWith("buckets:\n  - {name: b, replicas: 4}\n"), "/spec/buckets/0/replicas", "0–3"},
		{"replicas -1", srvWith("buckets:\n  - {name: b, replicas: -1}\n"), "/spec/buckets/0/replicas", "0–3"},
		{"bucket name missing", srvWith("buckets:\n  - {quotaMiB: 128}\n"), "/spec/buckets/0/name", "bucket name is required"},
		{"bucket name invalid", srvWith("buckets:\n  - {name: 'a b'}\n"), "/spec/buckets/0/name", "not valid"},
		{"bucket duplicate", srvWith("buckets:\n  - {name: b}\n  - {name: b}\n"), "/spec/buckets/1/name", "already defined"},
		{"scope invalid", srvWith("buckets:\n  - name: b\n    scopes: [{name: _x}]\n"), "/spec/buckets/0/scopes/0/name", "not valid"},
		{"scope duplicate", srvWith("buckets:\n  - name: b\n    scopes: [{name: s}, {name: s}]\n"), "/spec/buckets/0/scopes/1/name", "already defined"},
		{"collection invalid", srvWith("buckets:\n  - name: b\n    scopes: [{name: s, collections: ['a b']}]\n"), "/spec/buckets/0/scopes/0/collections/0", "not valid"},
		{"collection duplicate", srvWith("buckets:\n  - name: b\n    scopes: [{name: s, collections: [c, c]}]\n"), "/spec/buckets/0/scopes/0/collections/1", "already listed"},
		{"password missing", srv("nodes:\n  - {ipAddress: 10.0.0.1}\n"), "/spec/credentials/password", "password is required"},
		{"password literal", srv("credentials: {password: hunter2}\nnodes:\n  - {ipAddress: 10.0.0.1}\n"), "/spec/credentials/password", "not a literal"},
		{"password partial ref", srv("credentials: {password: 'x${PW}'}\nnodes:\n  - {ipAddress: 10.0.0.1}\n"), "/spec/credentials/password", "not a literal"},
		{"password number", srv("credentials: {password: 12}\nnodes:\n  - {ipAddress: 10.0.0.1}\n"), "/spec/credentials/password", "string reference"},
		{"password empty", srv("credentials: {password: ''}\nnodes:\n  - {ipAddress: 10.0.0.1}\n"), "/spec/credentials/password", "must not be empty"},
		{"caFile without tls", srvWith("tls: {caFile: ca.pem}\n"), "/spec/tls/caFile", "tls.enabled"},
		{"credential on server import", srvWith("buckets:\n  - name: b\n    import:\n      - {credential: x, data: [{a: 1}]}\n"), "/spec/buckets/0/import/0/credential", "CouchbaseCapella only"},
		{"unknown nodes field", srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, arbitor: true}\n"), "/spec/nodes/0/arbitor", "did you mean arbiter"},
		{"spec wrong type", srv("nodes: x\ncredentials:\n  password: ${PW}\n"), "/spec/nodes", "expected a list"},
	})
	runValid(t, map[string]string{
		"minimum":                srv(srvBase),
		"arbiter second":         srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1}\n  - {ipAddress: 10.0.0.2, arbiter: true}\n"),
		"primary not first":      srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1}\n  - {ipAddress: 10.0.0.2, primary: true}\n"),
		"data on one node only":  srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, services: [data]}\n  - {ipAddress: 10.0.0.2, services: [index]}\n"),
		"quota 100 replicas 3":   srvWith("buckets:\n  - {name: b, quotaMiB: 100, replicas: 3}\n"),
		"default scope names":    srvWith("buckets:\n  - name: b\n    scopes: [{name: _default, collections: [_default]}]\n"),
		"host name with port":    srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 'db1.example.com:8091'}\n"),
		"ipv6":                   srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: '2001:db8::1'}\n  - {ipAddress: '[2001:db8::2]:8091'}\n"),
		"caFile with tls":        srvWith("tls: {enabled: true, caFile: ca.pem}\n"),
		"rebalance false":        srvWith("rebalance: false\n"),
		"backup and fts aliases": srv("credentials:\n  password: ${PW}\nnodes:\n  - {ipAddress: 10.0.0.1, services: [data, fts, backup]}\n"),
	})
}

func TestServerDefaults(t *testing.T) {
	set, ve := parse(t, srv(`credentials:
  password: ${PW}
nodeDefaults:
  services: [fts, Data, data]
  dataPath: /data
  ramGiB: 8
nodes:
  - {ipAddress: 10.0.0.1}
  - {ipAddress: 10.0.0.2, primary: true, ramGiB: 4, dataPath: /other}
  - {ipAddress: 10.0.0.3, services: [query]}
  - {ipAddress: 10.0.0.4, arbiter: true}
buckets:
  - name: b
  - {name: c, quotaMiB: 500, replicas: 2}
`))
	if ve != nil {
		t.Fatal(ve)
	}
	s := set.Docs[0].Server
	if s.Credentials.Username != "Administrator" {
		t.Errorf("username = %q", s.Credentials.Username)
	}
	if !s.DoRebalance() {
		t.Error("rebalance should default to true")
	}
	if got := s.PrimaryIndex(); got != 1 {
		t.Errorf("primary = %d, want 1", got)
	}
	for i, p := range []bool{false, true, false, false} {
		if s.Nodes[i].Primary != p {
			t.Errorf("node %d primary = %v", i, s.Nodes[i].Primary)
		}
	}
	wantSvc := []string{"data,search", "data,search", "query", ""}
	for i, w := range wantSvc {
		if got := s.Nodes[i].Services.String(); got != w {
			t.Errorf("node %d services = %q, want %q", i, got, w)
		}
	}
	if s.Nodes[0].DataPath != "/data" || s.Nodes[1].DataPath != "/other" {
		t.Errorf("data paths = %q %q", s.Nodes[0].DataPath, s.Nodes[1].DataPath)
	}
	if *s.Nodes[0].RAMGiB != 8 || *s.Nodes[1].RAMGiB != 4 {
		t.Errorf("ram = %d %d", *s.Nodes[0].RAMGiB, *s.Nodes[1].RAMGiB)
	}
	if *s.Buckets[0].QuotaMiB != 128 || *s.Buckets[0].Replicas != 0 || *s.Buckets[1].QuotaMiB != 500 || *s.Buckets[1].Replicas != 2 {
		t.Errorf("bucket defaults wrong: %+v %+v", s.Buckets[0], s.Buckets[1])
	}
}

func TestServerDefaultServices(t *testing.T) {
	set, ve := parse(t, srv(srvBase))
	if ve != nil {
		t.Fatal(ve)
	}
	if got := set.Docs[0].Server.Nodes[0].Services.String(); got != "data,index,query,search" {
		t.Errorf("services = %q", got)
	}
	if !set.Docs[0].Server.Nodes[0].Primary {
		t.Error("a single node is the primary")
	}
}
