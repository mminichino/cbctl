package manifest

import (
	"strings"
	"testing"
)

const grp = "serviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}}\n"

func TestCapellaRules(t *testing.T) {
	runRules(t, []rule{
		{"project missing", strings.Replace(capSmall(""), "  project: p\n", "", 1), "/spec/project", "project is required"},
		{"provider invalid", capSmall("provider: aw\n"), "/spec/provider", "did you mean aws"},
		{"cidr host bits", capSmall("cidr: 10.0.0.5/24\n"), "/spec/cidr", "host bits"},
		{"cidr garbage", capSmall("cidr: nope\n"), "/spec/cidr", "not a CIDR"},
		{"cidr any", capSmall("cidr: 0.0.0.0/0\n"), "/spec/cidr", "cannot be"},
		{"availability invalid", capSmall("availability: both\n"), "/spec/availability", "not valid"},
		{"support plan invalid", capSmall("supportPlan: {plan: gold}\n"), "/spec/supportPlan/plan", "not valid"},
		{"timezone invalid", capSmall("supportPlan: {timezone: UTC}\n"), "/spec/supportPlan/timezone", "not valid"},
		{"size and groups", capella("size: small\n" + grp), "/spec/size", "mutually exclusive"},
		{"neither size nor groups", capella("region: x\n"), "/spec", "set size or serviceGroups"},
		{"size unknown", capella("size: huge\n"), "/spec/size", "not valid"},
		{"dev multi availability", capella("size: dev\navailability: multi\n"), "/spec/availability", "single-node"},
		{"no data group", capella("serviceGroups:\n  - {services: [index], nodes: 3, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups", "run the data service"},
		{"data group not first", capella("serviceGroups:\n  - {services: [index], nodes: 3, compute: {cpu: 8, ramGb: 32}}\n  - {services: [data], nodes: 2, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups", "must come first"},
		{"first group 2 nodes", capella("serviceGroups:\n  - {services: [data], nodes: 2, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/0/nodes", "3–27"},
		{"first group 28 nodes", capella("serviceGroups:\n  - {services: [data], nodes: 28, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/0/nodes", "3–27"},
		{"other group 1 node", capella(grp + "  - {services: [index], nodes: 1, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/1/nodes", "2–24"},
		{"other group 25 nodes", capella(grp + "  - {services: [index], nodes: 25, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/1/nodes", "2–24"},
		{"cluster over 27", capella("serviceGroups:\n  - {services: [data], nodes: 20, compute: {cpu: 8, ramGb: 32}}\n  - {services: [index], nodes: 10, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups", "at most 27"},
		{"duplicate service set", capella(grp + "  - {services: [data], nodes: 2, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/1/services", "each group needs a different set"},
		{"duplicate set via order and alias", capella("serviceGroups:\n  - {services: [data, search], nodes: 3, compute: {cpu: 8, ramGb: 32}}\n  - {services: [search, data, data], nodes: 2, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/1/services", "each group needs a different set"},
		{"services missing", capella("serviceGroups:\n  - {nodes: 3, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/0/services", "services is required"},
		{"backup service", capella("serviceGroups:\n  - {services: [data, backup], nodes: 3, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/0/services/1", "only on CouchbaseServer"},
		{"fts alias", capella("serviceGroups:\n  - {services: [data, fts], nodes: 3, compute: {cpu: 8, ramGb: 32}}\n"), "/spec/serviceGroups/0/services/1", "alias"},
		{"compute missing", capella("serviceGroups:\n  - {services: [data], nodes: 3}\n"), "/spec/serviceGroups/0/compute", "compute needs"},
		{"compute pair not offered", capella("serviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 4, ramGb: 64}}\n"), "/spec/serviceGroups/0/compute", "not a node size aws offers"},
		{"compute dev size only single node", capella("serviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 2, ramGb: 8}}\n"), "/spec/serviceGroups/0/compute", "not a node size"},
		{"disk type for provider", capella("provider: gcp\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: gp3}}\n"), "/spec/serviceGroups/0/disk/type", "not valid for gcp"},
		{"disk size too large", capella("serviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: gp3, sizeGb: 20000}}\n"), "/spec/serviceGroups/0/disk/sizeGb", "at most"},
		{"disk size too small", capella("serviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: gp3, sizeGb: 10}}\n"), "/spec/serviceGroups/0/disk/sizeGb", "at least"},
		{"disk iops low", capella("serviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: gp3, iops: 100}}\n"), "/spec/serviceGroups/0/disk/iops", "take 3000"},
		{"gcp iops not allowed", capella("provider: gcp\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {iops: 3000}}\n"), "/spec/serviceGroups/0/disk/iops", "not allowed"},
		{"azure fixed size", capella("provider: azure\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: P30, sizeGb: 512}}\n"), "/spec/serviceGroups/0/disk/sizeGb", "fixes the size"},
		{"azure fixed iops", capella("provider: azure\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: P30, iops: 1}}\n"), "/spec/serviceGroups/0/disk/iops", "fixes it"},
		{"azure ultra size", capella("provider: azure\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: Ultra, sizeGb: 100}}\n"), "/spec/serviceGroups/0/disk/sizeGb", "come in"},
		{"cidr any needs flag", capSmall("allowedCidrs:\n  - {cidr: 0.0.0.0/0}\n"), "/spec/allowedCidrs/0/cidr", "allowAnyIp"},
		{"cidr v6 any needs flag", capSmall("allowedCidrs:\n  - {cidr: '::/0'}\n"), "/spec/allowedCidrs/0/cidr", "allowAnyIp"},
		{"allowed cidr host bits", capSmall("allowedCidrs:\n  - {cidr: 1.2.3.4/24}\n"), "/spec/allowedCidrs/0/cidr", "host bits"},
		{"allowed cidr missing", capSmall("allowedCidrs:\n  - {comment: x}\n"), "/spec/allowedCidrs/0/cidr", "cidr is required"},
		{"allowed cidr duplicate", capSmall("allowedCidrs:\n  - {cidr: 1.2.3.4}\n  - {cidr: 1.2.3.4/32}\n"), "/spec/allowedCidrs/1/cidr", "already listed"},
		{"expiresAt invalid", capSmall("allowedCidrs:\n  - {cidr: 1.2.3.4, expiresAt: 'soon'}\n"), "/spec/allowedCidrs/0/expiresAt", "RFC 3339"},
		{"wait timeout over 24h", capSmall("wait: {timeout: 25h}\n"), "/spec/wait/timeout", "at most 24h"},
		{"wait timeout zero", capSmall("wait: {timeout: 0}\n"), "/spec/wait/timeout", "positive"},
		{"wait timeout garbage", capSmall("wait: {timeout: soon}\n"), "/spec/wait/timeout", "not a duration"},
		{"unknown spec field", capSmall("regon: x\n"), "/spec/regon", "did you mean region"},
	})
	runValid(t, map[string]string{
		"size small":          capSmall(""),
		"size dev":            capella("size: dev\nbuckets:\n  - {name: b, replicas: 0}\n"),
		"dev explicit single": capella("size: dev\navailability: single\n"),
		"groups":              capella(grp),
		"first group 27":      capella("serviceGroups:\n  - {services: [data], nodes: 27, compute: {cpu: 8, ramGb: 32}}\n"),
		"cluster total 27":    capella("serviceGroups:\n  - {services: [data], nodes: 25, compute: {cpu: 8, ramGb: 32}}\n  - {services: [index], nodes: 2, compute: {cpu: 8, ramGb: 32}}\n"),
		"any ip confirmed":    capSmall("allowedCidrs:\n  - {cidr: 0.0.0.0/0, allowAnyIp: true}\n"),
		"wait 24h":            capSmall("wait: {timeout: 24h}\n"),
		"wait seconds":        capSmall("wait: {timeout: 90}\n"),
		"wait disabled":       capSmall("wait: {enabled: false}\n"),
		"gcp disk":            capella("provider: gcp\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: pd-ssd, sizeGb: 200}}\n"),
		"azure ultra":         capella("provider: azure\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: Ultra, sizeGb: 512, iops: 8000}}\n"),
		"azure fixed":         capella("provider: azure\nserviceGroups:\n  - {services: [data], nodes: 3, compute: {cpu: 8, ramGb: 32}, disk: {type: P30}}\n"),
		"expiresAt unquoted":  capSmall("allowedCidrs:\n  - {cidr: 1.2.3.4, expiresAt: 2027-01-31T00:00:00Z}\n"),
	})
}
