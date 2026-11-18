package capellafile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// fixtureObj parses a fixture without validating it or applying defaults, like data an importer
// would hand to Dump.
func fixtureObj(t *testing.T, rel string) Obj {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	root, issues := parseYAML(string(b), rel)
	if len(issues) > 0 {
		t.Fatalf("%s: %v", rel, issues)
	}
	return toObj(root).(Obj)
}

func TestDumpCanonicalStyle(t *testing.T) {
	out, err := Dump(Obj{
		"projects": []any{Obj{
			"name": "p",
			"clusters": []any{Obj{
				"buckets":       []any{Obj{"scopes": []any{Obj{"collections": []any{Obj{"name": "c"}}, "name": "s"}}, "replicas": int64(1), "name": "b"}},
				"support":       Obj{"timezone": "PT", "plan": "basic"},
				"cloudProvider": Obj{"cidr": "10.0.0.0/24", "region": "us-east-1", "type": "aws"},
				"name":          "c1",
				"serviceGroups": []any{Obj{
					"services":   []any{"data", "index"},
					"numOfNodes": int64(3),
					"node":       Obj{"disk": Obj{"iops": int64(3000), "storage": int64(50), "type": "gp3"}, "compute": Obj{"ram": int64(16), "cpu": int64(4)}},
				}},
				"deletionProtection": true,
				"tier":               "provisioned",
			}},
		}},
		"organizationId": "${CAPELLA_ORG_ID}",
		"apiVersion":     "cbctl/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `apiVersion: cbctl/v1
organizationId: ${CAPELLA_ORG_ID}
projects:
  - name: p
    clusters:
      - name: c1
        tier: provisioned
        cloudProvider: {type: aws, region: us-east-1, cidr: 10.0.0.0/24}
        support: {plan: basic, timezone: PT}
        serviceGroups:
          - node:
              compute: {cpu: 4, ram: 16}
              disk: {type: gp3, storage: 50, iops: 3000}
            numOfNodes: 3
            services: [data, index]
        deletionProtection: true
        buckets:
          - name: b
            replicas: 1
            scopes:
              - name: s
                collections:
                  - name: c
`
	if out != want {
		t.Errorf("Dump() =\n%s\nwant\n%s", out, want)
	}
}

func TestDumpFlowStyleForCollectionNamesAndPrivileges(t *testing.T) {
	out, err := Dump(Obj{"databaseCredentials": []any{Obj{
		"name": "u",
		"access": []any{Obj{
			"privileges": []string{"data_reader"},
			"resources":  Obj{"buckets": []any{Obj{"name": "b", "scopes": []any{Obj{"name": "s", "collections": []string{"x1", "x2"}}}}}},
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"privileges: [data_reader]", "collections: [x1, x2]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestDumpQuotesAmbiguousStrings(t *testing.T) {
	out, err := Dump(Obj{"hibernation": Obj{"weekend": "off", "friday": "on", "monday": "08:00-18:00", "timezone": "US/Pacific"}, "x": "123", "flag": "true", "z": ""})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`weekend: "off"`, `friday: "on"`, `x: "123"`, `flag: "true"`, `z: ""`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	var back Obj
	if err := yaml.Unmarshal([]byte(out), &back); err != nil {
		t.Fatal(err)
	}
	if back["x"] != "123" || back["flag"] != "true" {
		t.Errorf("round trip changed types: %#v", back)
	}
}

func TestDumpedFixturesLoadBackIdentically(t *testing.T) {
	files, _ := filepath.Glob("testdata/valid/*.yaml")
	for _, f := range files {
		rel := filepath.Join("valid", filepath.Base(f))
		t.Run(filepath.Base(f), func(t *testing.T) {
			dir := t.TempDir()
			// Secret files referenced by the fixture live next to it.
			if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o755); err != nil {
				t.Fatal(err)
			}
			pw, _ := os.ReadFile("testdata/valid/secrets/reporting_ro.txt")
			if err := os.WriteFile(filepath.Join(dir, "secrets", "reporting_ro.txt"), pw, 0o600); err != nil {
				t.Fatal(err)
			}
			orig, err := Load(f, testEnv(nil))
			if err != nil {
				t.Fatal(err)
			}
			out, err := Dump(fixtureObj(t, rel))
			if err != nil {
				t.Fatal(err)
			}
			again, err := LoadText(out, "capella.yaml", dir, testEnv(nil))
			if err != nil {
				t.Fatalf("dumped text does not load: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(orig.Raw, again.Raw) {
				t.Errorf("document changed by Dump + Load\n%s", out)
			}
		})
	}
}

func TestToNodeScalars(t *testing.T) {
	for _, v := range []any{int(3), int64(3), 1.5, true, "s", nil} {
		n := ToNode(v, "x")
		if n.Kind != yaml.ScalarNode {
			t.Errorf("ToNode(%v).Kind = %d", v, n.Kind)
		}
	}
	existing := &yaml.Node{Kind: yaml.ScalarNode, Value: "keep", Tag: "!!str"}
	if ToNode(existing, "x") != existing {
		t.Error("ToNode must return a *yaml.Node as is")
	}
	if n := ToNode(map[string]int{"b": 1, "a": 2}, "x"); n.Kind != yaml.MappingNode || n.Content[0].Value != "a" {
		t.Errorf("typed map not converted in key order: %+v", n)
	}
}
