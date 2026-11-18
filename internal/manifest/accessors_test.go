package manifest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocumentKey(t *testing.T) {
	tests := []struct {
		name, doc, want, err string
	}{
		{"string", `{"id":"a"}`, "a", ""},
		{"int", `{"id":42}`, "42", ""},
		{"negative zero", `{"id":-0}`, "0", ""},
		{"float integer", `{"id":1000.0}`, "1000", ""},
		{"exponent integer", `{"id":1e3}`, "1000", ""},
		{"fraction", `{"id":1.5}`, "", "got 1.5"},
		{"too large", `{"id":99999999999999999999}`, "", "too large"},
		{"missing", `{"x":1}`, "", "id is missing"},
		{"empty string", `{"id":""}`, "", "empty string"},
		{"null", `{"id":null}`, "", "got null"},
		{"bool", `{"id":true}`, "", "got a boolean"},
		{"object", `{"id":{}}`, "", "got an object"},
		{"list", `{"id":[]}`, "", "got a list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.doc), &m); err != nil {
				t.Fatal(err)
			}
			got, err := DocumentKey(m, "id")
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("DocumentKey = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestCapellaAccessors(t *testing.T) {
	set, ve := parse(t, capella("size: dev\nbuckets:\n  - {name: b}\n"))
	if ve != nil {
		t.Fatal(ve)
	}
	s := set.Docs[0].Capella
	if !s.SingleNode() || s.EffectiveAvailability() != "single" {
		t.Errorf("dev: single=%v availability=%s", s.SingleNode(), s.EffectiveAvailability())
	}
	if s.EffectiveProvider() != "aws" || s.EffectiveRegion() != "us-east-1" {
		t.Errorf("defaults: %s %s", s.EffectiveProvider(), s.EffectiveRegion())
	}
	if s.WaitTimeout() != 60*time.Minute || !s.WaitEnabled() {
		t.Errorf("wait defaults: %v %v", s.WaitTimeout(), s.WaitEnabled())
	}
	if g := s.EffectiveGroups(); len(g) != 1 || g[0].Nodes != 1 {
		t.Errorf("groups = %+v", g)
	}
	b := s.Buckets[0]
	if b.EffectiveType() != "couchbase" || b.EffectiveMemoryMB() != 1024 || b.EffectiveReplicas(true) != 0 || b.EffectiveReplicas(false) != 1 {
		t.Errorf("bucket defaults: %s %d %d %d", b.EffectiveType(), b.EffectiveMemoryMB(), b.EffectiveReplicas(true), b.EffectiveReplicas(false))
	}
	if s.DataMemoryMB() <= 0 {
		t.Error("DataMemoryMB is not positive")
	}

	set, ve = parse(t, capSmall("provider: azure\nregion: westus\nwait: {timeout: 5m, enabled: false}\n"))
	if ve != nil {
		t.Fatal(ve)
	}
	s = set.Docs[0].Capella
	if s.EffectiveRegion() != "westus" || s.WaitTimeout() != 5*time.Minute || s.WaitEnabled() || s.SingleNode() || s.EffectiveAvailability() != "multi" {
		t.Errorf("explicit values: %s %v %v", s.EffectiveRegion(), s.WaitTimeout(), s.WaitEnabled())
	}
	if _, ok := (AllowedCIDR{ExpiresAt: "2027-01-31T00:00:00Z"}).ExpiryTime(); !ok {
		t.Error("ExpiryTime rejected an RFC 3339 time")
	}
	if _, ok := (AllowedCIDR{}).ExpiryTime(); ok {
		t.Error("ExpiryTime accepted an empty value")
	}
}

func TestCapellaDataRules(t *testing.T) {
	bk := func(s string) string {
		return capSmall("buckets:\n  - name: b\n" + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n") + "\n")
	}
	_ = bk
	runRules(t, []rule{
		{"bucket name", capSmall("buckets:\n  - name: _bad\n"), "/spec/buckets/0/name", "not valid"},
		{"bucket missing name", capSmall("buckets:\n  - memoryMb: 100\n"), "/spec/buckets/0/name", "required"},
		{"duplicate bucket", capSmall("buckets:\n  - name: b\n  - name: b\n"), "/spec/buckets/1/name", "already defined"},
		{"bucket type", capSmall("buckets:\n  - {name: b, type: memcached}\n"), "/spec/buckets/0/type", "not valid"},
		{"storage backend", capSmall("buckets:\n  - {name: b, storageBackend: rocks}\n"), "/spec/buckets/0/storageBackend", "not valid"},
		{"durability", capSmall("buckets:\n  - {name: b, durability: always}\n"), "/spec/buckets/0/durability", "not valid"},
		{"replicas range", capSmall("buckets:\n  - {name: b, replicas: 4}\n"), "/spec/buckets/0/replicas", "0–3"},
		{"memory low", capSmall("buckets:\n  - {name: b, memoryMb: 50}\n"), "/spec/buckets/0/memoryMb", "at least"},
		{"ttl negative", capSmall("buckets:\n  - {name: b, ttl: -1}\n"), "/spec/buckets/0/ttl", "negative"},
		{"scope duplicate", capSmall("buckets:\n  - name: b\n    scopes:\n      - name: s\n      - name: s\n"), "/spec/buckets/0/scopes/1/name", "already defined"},
		{"scope name", capSmall("buckets:\n  - name: b\n    scopes:\n      - name: _x\n"), "/spec/buckets/0/scopes/0/name", "not valid"},
		{"collection name", capSmall("buckets:\n  - name: b\n    scopes:\n      - {name: s, collections: [{name: '%x'}]}\n"), "/spec/buckets/0/scopes/0/collections/0/name", "not valid"},
		{"import both", capSmall("buckets:\n  - name: b\n    import:\n      - {file: data/schedules.jsonl, data: [{a: 1}], credential: c}\n"), "/spec/buckets/0/import/0", "exactly one of file or data"},
		{"import neither", capSmall("buckets:\n  - name: b\n    import:\n      - {credential: c}\n"), "/spec/buckets/0/import/0", "exactly one of file or data"},
		{"import duplicate target", capSmall("buckets:\n  - name: b\n    import:\n      - {data: [{a: 1}], credential: c}\n      - {data: [{a: 1}], credential: c}\n"), "/spec/buckets/0/import/1", "already"},
		{"import data not object", capSmall("buckets:\n  - name: b\n    import:\n      - {data: [1], credential: c}\n"), "/spec/buckets/0/import/0/data/0", "mapping"},
		{"import file missing", capSmall("buckets:\n  - name: b\n    import:\n      - {file: nope.jsonl, credential: c}\n"), "/spec/buckets/0/import/0/file", "nope.jsonl"},
		{"inline duplicate key", capSmall("buckets:\n  - name: b\n    import:\n      - {keyField: id, credential: c, data: [{id: 1}, {id: 1}]}\n"), "/spec/buckets/0/import/0/data/1", "duplicates"},
		{"inline bad key", capSmall("buckets:\n  - name: b\n    import:\n      - {keyField: id, credential: c, data: [{id: 1.5}]}\n"), "/spec/buckets/0/import/0/data/0", "keyField"},
		{"file keys missing field", capSmall("buckets:\n  - name: b\n    import:\n      - {file: data/schedules.jsonl, keyField: nope, credential: c}\n"), "", "nope is missing"},
		{"credential name", capSmall("credentials:\n  - {name: '', access: {privileges: read, allBuckets: true}}\n"), "/spec/credentials/0/name", "required"},
		{"credential password type", capSmall("credentials:\n  - {name: c, password: 5, access: {privileges: read, allBuckets: true}}\n"), "/spec/credentials/0/password", "string reference"},
	})
	runValid(t, map[string]string{
		"credential bucket checked online": capSmall("credentials:\n  - {name: c, access: {privileges: read, bucket: ghost}}\n"),
		"bucket full":                      capSmall("buckets:\n  - {name: b, type: couchbase, storageBackend: magma, durability: majority, replicas: 2, memoryMb: 512, ttl: 10, flush: true}\n"),
		"import file":                      capSmall("buckets:\n  - name: b\n    import:\n      - {file: data/schedules.jsonl, keyField: flightId, credential: c}\ncredentials:\n  - {name: c, password: '${PW}', access: {privileges: read+write, bucket: b}}\n"),
		"import all buckets credential":    capSmall("buckets:\n  - name: b\n    import:\n      - {data: [{a: 1}], credential: c}\ncredentials:\n  - {name: c, password: '${PW}', access: {privileges: write, allBuckets: true}}\n"),
		"scoped credential":                capSmall("buckets:\n  - name: b\n    scopes:\n      - {name: s, collections: [{name: x}]}\n    import:\n      - {scope: s, collection: x, data: [{a: 1}], credential: c}\ncredentials:\n  - {name: c, password: '${PW}', access: {privileges: write, bucket: b, scope: s, collections: [x]}}\n"),
	})
}

func TestLoadFile(t *testing.T) {
	// The project root only: no temp directories outside it.
	dir, err := filepath.Abs("testdata/valid")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "server-min.yaml")
	set, err := Load(context.Background(), Source{Path: "server-min.yaml", Cwd: dir})
	if err != nil {
		t.Fatalf("Load relative: %v", err)
	}
	if set.BaseDir != dir || set.Name != "server-min.yaml" || len(set.Docs) != 1 {
		t.Errorf("set = %+v", set)
	}
	if _, err := Load(context.Background(), Source{Path: path}); err != nil {
		t.Errorf("Load absolute: %v", err)
	}
	if _, err := Load(context.Background(), Source{Path: filepath.Join(dir, "none.yaml")}); err == nil {
		t.Error("missing file accepted")
	}
	if _, err := Load(context.Background(), Source{Path: "-"}); err == nil {
		t.Error("stdin without input accepted")
	}
	var ve *ValidationError
	if _, err := Load(context.Background(), Source{}); !asValidation(err, &ve) {
		t.Errorf("no path: %v", err)
	}
	if _, err := Load(context.Background(), Source{Path: "-", Stdin: strings.NewReader("")}); !asValidation(err, &ve) || !strings.Contains(err.Error(), "no documents") {
		t.Errorf("empty stdin: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(ctx, Source{Path: "-", Stdin: strings.NewReader(srvWith(""))}); err == nil || asValidation(err, &ve) {
		t.Errorf("cancelled context: %v", err)
	}
	if got := ImportPath("/base", "a/b.jsonl"); got != filepath.Join("/base", "a", "b.jsonl") {
		t.Errorf("ImportPath relative = %s", got)
	}
	// An absolute path is OS-specific: "/abs/b.jsonl" has no drive letter on Windows.
	abs := filepath.Join(t.TempDir(), "abs", "b.jsonl")
	if got := ImportPath("/base", abs); got != abs {
		t.Errorf("ImportPath absolute = %s, want %s", got, abs)
	}
}

func asValidation(err error, target **ValidationError) bool {
	ve, ok := err.(*ValidationError) //nolint:errorlint // exact type is the contract here
	if ok {
		*target = ve
	}
	return ok
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{srvWith(""), capSmall(""), "", "---\n---\n", "kind: X\n", "apiVersion: cbctl/v1\nkind: CouchbaseServer\nspec: [", capSmall("buckets:\n  - {name: b, import: [{keyField: id, data: [{id: 1}]}]}\n")} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		set, err := Parse(context.Background(), strings.NewReader(text), "f.yaml", "testdata/valid")
		if err == nil && set == nil {
			t.Fatal("nil set without an error")
		}
		if err != nil && set != nil {
			t.Fatal("set returned together with an error")
		}
		if err != nil {
			_ = err.Error()
		}
	})
}
