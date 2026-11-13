package capellafile

import (
	"strings"
	"testing"
)

func TestCrossRefRules(t *testing.T) {
	const creds = `    buckets:
      - name: orders
        memoryAllocationInMb: 1024
        scopes:
          - name: inventory
            collections:
              - name: items
    databaseCredentials:
      - name: app
        access:
          - privileges: [read]
            resources:
              buckets:
                - name: %s
                  %s
`
	tests := []struct {
		name    string
		text    string
		wantErr string // empty: must load
	}{
		{"external bucket may be undeclared", minimalYAML + sprintfCreds(creds, "legacy", "external: true"), ""},
		{"wildcard bucket and scope are skipped", minimalYAML + sprintfCreds(creds, "'*'", "scopes: [ { name: '*' } ]"), ""},
		{"_default scope needs no declaration", minimalYAML + sprintfCreds(creds, "orders", "scopes: [ { name: _default, collections: [anything] } ]"), ""},
		{"unknown bucket", minimalYAML + sprintfCreds(creds, "ordrs", ""), "bucket 'ordrs', which capella.yaml does not list for cluster 'nova-dev-01'"},
		{"unknown scope", minimalYAML + sprintfCreds(creds, "orders", "scopes: [ { name: billing } ]"), "scope 'billing' is not defined in bucket 'orders'"},
		{"unknown collection", minimalYAML + sprintfCreds(creds, "orders", "scopes: [ { name: inventory, collections: [nope] } ]"), "collection 'nope' is not defined in orders.inventory"},
		{"wildcard collection", minimalYAML + sprintfCreds(creds, "orders", "scopes: [ { name: inventory, collections: ['*'] } ]"), ""},
		{"gov region", strings.Replace(minimalYAML, "region: us-east-1", "region: us-gov-west-1", 1), ""},
		{"disk range ok", strings.Replace(minimalYAML, "iops: 3000", "iops: 16000", 1), ""},
		{"azure premium disk has fixed size", strings.Replace(strings.Replace(minimalYAML, "type: aws, region: us-east-1", "type: azure, region: eastus", 1),
			"disk: { type: gp3, storage: 50, iops: 3000 }", "disk: { type: P30 }", 1), ""},
		{"azure ultra disk with a size that is not offered", strings.Replace(strings.Replace(minimalYAML, "type: aws, region: us-east-1", "type: azure, region: eastus", 1),
			"disk: { type: gp3, storage: 50, iops: 3000 }", "disk: { type: Ultra, storage: 100, iops: 3000 }", 1), "Ultra disks come in 64, 128, 256, 512, 1024, … 15360 GB (got 100)"},
		{"azure ultra iops cap by size", strings.Replace(strings.Replace(minimalYAML, "type: aws, region: us-east-1", "type: azure, region: eastus", 1),
			"disk: { type: gp3, storage: 50, iops: 3000 }", "disk: { type: Ultra, storage: 64, iops: 30000 }", 1), "Ultra disks take 3000–19200 IOPS at 64 GB (got 30000)"},
		{"hibernation end of day 24:00", minimalYAML + "    hibernation:\n      timezone: US/Pacific\n      weekdays: \"08:00-24:00\"\n", ""},
		{"hibernation absent needs no timezone", minimalYAML + "    hibernation:\n      ensure: absent\n", ""},
		{"hibernation on-window 00:00-24:00 is allowed", minimalYAML + "    hibernation:\n      timezone: US/Pacific\n      weekdays: \"00:00-24:00\"\n", ""},
		{"cidr bare IP", minimalYAML + "    allowedCidrs:\n      - cidr: 203.0.113.10\n", ""},
		{"cidr IPv6", minimalYAML + "    allowedCidrs:\n      - cidr: 2001:db8::/32\n", ""},
		{"cidr any IP is allowed (a warning, not an error)", minimalYAML + "    allowedCidrs:\n      - cidr: 0.0.0.0/0\n", ""},
		{"bare IPv4 and /32 are duplicates", minimalYAML + "    allowedCidrs:\n      - cidr: 203.0.113.10\n      - cidr: 203.0.113.10/32\n", "duplicate allowed CIDR name"},
		{"expiresAt with offset", minimalYAML + "    allowedCidrs:\n      - cidr: 203.0.113.10\n        expiresAt: \"2030-01-01T00:00:00+02:00\"\n", ""},
		{"ephemeral bucket ok", minimalYAML + "    buckets:\n      - name: e\n        type: ephemeral\n        evictionPolicy: nruEviction\n        durabilityLevel: majority\n", ""},
		{"magma vbuckets ok", minimalYAML + "    buckets:\n      - name: m\n        storageBackend: magma\n        vbuckets: 128\n", ""},
		{"_default scope may be declared", minimalYAML + "    buckets:\n      - name: b\n        scopes:\n          - name: _default\n            collections:\n              - name: c\n", ""},
		{"keyspace name with % - _", minimalYAML + "    buckets:\n      - name: b\n        scopes:\n          - name: a%b-c_d\n", ""},
		{"pinned clusters may share a name", pinnedTwins, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadText(tt.text, "capella.yaml", t.TempDir(), testEnv(nil))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

var pinnedTwins = `apiVersion: cbctl/v1
project:
  name: p
clusters:
  - id: aaa
    name: twin
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.10.0.0/24 }
    availability: { type: multi }
    support: { plan: basic, timezone: PT }
    serviceGroups:
      - node: { compute: { cpu: 4, ram: 16 }, disk: { type: gp3, storage: 50, iops: 3000 } }
        numOfNodes: 3
        services: [data]
  - id: bbb
    name: twin
    cloudProvider: { type: aws, region: us-east-1, cidr: 10.11.0.0/24 }
    availability: { type: multi }
    support: { plan: basic, timezone: PT }
    serviceGroups:
      - node: { compute: { cpu: 4, ram: 16 }, disk: { type: gp3, storage: 50, iops: 3000 } }
        numOfNodes: 3
        services: [data]
`

func sprintfCreds(format string, a, b string) string {
	return strings.Replace(strings.Replace(format, "%s", a, 1), "%s", b, 1)
}

func TestFreeTierIsLimitedPerOrganizationAcrossProjects(t *testing.T) {
	text := `apiVersion: cbctl/v1
projects:
  - name: a
    clusters:
      - name: f1
        tier: free
        cloudProvider: { type: gcp, region: us-central1 }
  - name: b
    clusters:
      - name: f2
        tier: free
        cloudProvider: { type: gcp, region: us-central1 }
`
	ce := loadErr(t, text)
	if len(ce.Issues) != 1 || !strings.Contains(ce.Issues[0].Message, "only one free-tier cluster") ||
		ce.Issues[0].Path != "projects[1].clusters[0].tier" {
		t.Errorf("issues = %+v", ce.Issues)
	}
}

func TestIssuePathsAndWhere(t *testing.T) {
	ce := loadErr(t, strings.Replace(minimalYAML, "numOfNodes: 3", "numOfNodes: 2", 1))
	i := ce.Issues[0]
	if i.Path != "clusters[0].serviceGroups[0].numOfNodes" {
		t.Errorf("Path = %q", i.Path)
	}
	if i.Where != "project nova-dev › cluster nova-dev-01 › service group data,index,query › numOfNodes" {
		t.Errorf("Where = %q", i.Where)
	}
	if i.Line != 16 || i.Topic != "scaling" {
		t.Errorf("Line=%d Topic=%q", i.Line, i.Topic)
	}
}

func TestNormalizeCIDR(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"203.0.113.10", "203.0.113.10/32", false},
		{" 203.0.113.0/24 ", "203.0.113.0/24", false},
		{"2001:db8::1", "2001:db8::1/128", false},
		{"2001:DB8::/32", "2001:db8::/32", false},
		{"0.0.0.0/0", "0.0.0.0/0", false},
		{"::/0", "::/0", false},
		{"203.0.113.10/24", "", true},
		{"nope", "", true},
		{"10.0.0.0/33", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := NormalizeCIDR(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("NormalizeCIDR(%q) = %q, %v; want %q (err %v)", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
	if s, ok := SuggestedNetwork("203.0.113.10/24"); !ok || s != "203.0.113.0/24" {
		t.Errorf("SuggestedNetwork = %q %v", s, ok)
	}
	if _, ok := SuggestedNetwork("nope"); ok {
		t.Error("SuggestedNetwork(nope) should not suggest")
	}
}

func TestKeyspaceNameIssue(t *testing.T) {
	tests := []struct {
		kind string
		name any
		bad  bool
	}{
		{"scope", "inventory", false},
		{"scope", "_default", false},
		{"collection", "_default", true}, // only the scope may be declared as _default
		{"scope", "_system", true},
		{"scope", "%x", true},
		{"scope", "a b", true},
		{"scope", "a.b", true},
		{"collection", "A-z_0%", false},
		{"collection", "", true},
		{"collection", strings.Repeat("a", 251), false},
		{"collection", strings.Repeat("a", 252), true},
		{"scope", 12, true},
	}
	for _, tt := range tests {
		got := KeyspaceNameIssue(tt.kind, tt.name)
		if (got != "") != tt.bad {
			t.Errorf("KeyspaceNameIssue(%s, %v) = %q, bad=%v", tt.kind, tt.name, got, tt.bad)
		}
	}
	long := KeyspaceNameIssue("scope", strings.Repeat("é", 300))
	if !strings.Contains(long, "… (300 characters)") {
		t.Errorf("long name not shortened: %q", long)
	}
}

func TestBucketTypeRules(t *testing.T) {
	tests := []struct {
		name string
		b    Obj
		want []string
	}{
		{"couchbase ok", Obj{"type": "couchbase", "evictionPolicy": "fullEviction"}, nil},
		{"couchbase with ephemeral eviction", Obj{"type": "couchbase", "evictionPolicy": "nruEviction"}, []string{"evictionPolicy"}},
		{"ephemeral ok", Obj{"type": "ephemeral", "evictionPolicy": "noEviction", "durabilityLevel": "majority"}, nil},
		{"ephemeral all bad", Obj{"type": "ephemeral", "storageBackend": "magma", "evictionPolicy": "fullEviction", "durabilityLevel": "persistToMajority"},
			[]string{"storageBackend", "evictionPolicy", "durabilityLevel"}},
		{"vbuckets need magma", Obj{"vbuckets": int64(128), "storageBackend": "couchstore"}, []string{"vbuckets"}},
		{"vbuckets with magma", Obj{"vbuckets": int64(128), "storageBackend": "magma"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, r := range BucketTypeRules(tt.b) {
				got = append(got, r.Field)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("fields = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDayValueAndGroupKey(t *testing.T) {
	hib := Obj{"weekdays": "08:00-18:00", "weekend": false, "friday": "off"}
	want := map[string]string{
		"monday": "08:00-18:00", "friday": "off", "saturday": "off", "sunday": "off",
	}
	for day, w := range want {
		if got := DayValue(hib, day); got != w {
			t.Errorf("DayValue(%s) = %q, want %q", day, got, w)
		}
	}
	if got := DayValue(Obj{}, "monday"); got != "on" {
		t.Errorf("default day = %q", got)
	}
	if got := GroupKey(Obj{"services": []any{"query", "data", "index"}}); got != "data,index,query" {
		t.Errorf("GroupKey = %q", got)
	}
}

func TestParseRFC3339(t *testing.T) {
	for _, ok := range []string{"2026-12-31T23:59:59Z", "2026-12-31t23:59:59z", "2026-12-31T23:59:59.5+02:00"} {
		if _, valid := ParseRFC3339(ok); !valid {
			t.Errorf("ParseRFC3339(%q) should be valid", ok)
		}
	}
	for _, bad := range []any{"2026-12-31", "tomorrow", "2026-13-31T00:00:00Z", 5, nil, "2026-12-31 23:59:59"} {
		if _, valid := ParseRFC3339(bad); valid {
			t.Errorf("ParseRFC3339(%v) should be invalid", bad)
		}
	}
}
