package capellafile

import (
	"strings"
	"testing"
	"time"
)

func warningTexts(cfg *Config, now time.Time) string {
	var sb strings.Builder
	for _, n := range warningsAt(cfg, now) {
		sb.WriteString(n.What + " | " + n.Action + "\n")
	}
	return sb.String()
}

func TestWarnings(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		text    string
		wants   []string
		unwants []string
	}{
		{
			name: "deletionProtection unset on a provisioned cluster",
			text: minimalYAML,
			wants: []string{"cluster nova-dev-01 (line 7): deletionProtection is not set",
				"cbctl capella cluster protect nova-dev-01 --project nova-dev"},
		},
		{
			name:    "deletionProtection set",
			text:    minimalYAML + "    deletionProtection: true\n",
			unwants: []string{"deletionProtection"},
		},
		{
			name: "any IP not recorded as intended",
			text: minimalYAML + "    deletionProtection: true\n    allowedCidrs:\n      - cidr: 0.0.0.0/0\n",
			wants: []string{"lets every IP address connect, and capella.yaml does not record that this is intended",
				"allowed IP range 0.0.0.0/0 (line 20)", "cbctl capella cidr delete nova-dev-01 0.0.0.0/0 --project nova-dev"},
		},
		{
			name:    "any IP recorded with allowAnyIp",
			text:    minimalYAML + "    deletionProtection: true\n    allowAnyIp: true\n    allowedCidrs:\n      - cidr: 0.0.0.0/0\n",
			unwants: []string{"every IP"},
		},
		{
			name:  "expired range",
			text:  minimalYAML + "    deletionProtection: true\n    allowedCidrs:\n      - cidr: 203.0.113.10\n        expiresAt: \"2026-01-01T00:00:00Z\"\n",
			wants: []string{"expiresAt is in the past"},
		},
		{
			name:    "future range",
			text:    minimalYAML + "    deletionProtection: true\n    allowedCidrs:\n      - cidr: 203.0.113.10\n        expiresAt: \"2027-01-01T00:00:00Z\"\n",
			unwants: []string{"expiresAt"},
		},
		{
			name: "iops below the recommendation",
			text: strings.Replace(minimalYAML, "storage: 50, iops: 3000", "storage: 300, iops: 3000", 1) + "    deletionProtection: true\n",
			wants: []string{"3000 IOPS is below Capella's recommended 7110 for 300 GB",
				"--group data,index,query --disk-iops 7110"},
		},
		{
			name:    "100-199 GB may use 3000 IOPS",
			text:    strings.Replace(minimalYAML, "storage: 50, iops: 3000", "storage: 150, iops: 3000", 1) + "    deletionProtection: true\n",
			unwants: []string{"IOPS"},
		},
		{
			name: "credential with access to all buckets",
			text: minimalYAML + "    deletionProtection: true\n    databaseCredentials:\n      - name: all\n        access:\n          - privileges: [read]\n",
			wants: []string{"credential all › access[0] (line 22): grants access to ALL buckets",
				"cbctl capella credential update nova-dev-01 all --bucket BUCKET"},
		},
		{
			name:  "compute size Capella does not list",
			text:  strings.Replace(minimalYAML, "cpu: 4, ram: 16", "cpu: 7, ram: 13", 1) + "    deletionProtection: true\n",
			wants: []string{"7 vCPU / 13 GB RAM is not a node size Capella documents for aws"},
		},
		{
			name:    "known compute size",
			text:    minimalYAML + "    deletionProtection: true\n",
			unwants: []string{"vCPU"},
		},
		{
			name:  "boolean hibernation shorthand",
			text:  minimalYAML + "    deletionProtection: true\n    hibernation:\n      timezone: US/Pacific\n      weekdays: true\n      weekend: false\n",
			wants: []string{"unquoted YAML boolean", `quote "on"/"off"`},
		},
		{
			name:    "free-tier clusters need no deletion protection",
			text:    "apiVersion: cbctl/v1\nprojects:\n  - name: p\n    clusters:\n      - name: f\n        tier: free\n        cloudProvider: { type: aws, region: us-east-2 }\n",
			unwants: []string{"deletionProtection"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := warningTexts(mustLoadText(t, tt.text), now)
			for _, w := range tt.wants {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, u := range tt.unwants {
				if strings.Contains(got, u) {
					t.Errorf("unexpected %q in:\n%s", u, got)
				}
			}
		})
	}
}

func TestWarningsUsesTheClock(t *testing.T) {
	cfg := mustLoadText(t, minimalYAML+"    allowedCidrs:\n      - cidr: 203.0.113.10\n        expiresAt: \"2000-01-01T00:00:00Z\"\n")
	if got := Warnings(cfg); !strings.Contains(warningTexts(cfg, time.Now()), "in the past") || len(got) == 0 {
		t.Errorf("Warnings() = %v", got)
	}
}
