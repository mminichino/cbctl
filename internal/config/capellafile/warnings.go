package capellafile

import (
	"fmt"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/presets"
)

// Notice is a valid-but-suspicious finding: What happened and what to do about it.
type Notice struct {
	What   string `json:"what"`
	Action string `json:"action"`
}

// Warnings returns the valid but probably unintended settings of cfg. "validate" prints them
// all; "plan" and "apply" only print how many there are.
func Warnings(cfg *Config) []Notice {
	return warningsAt(cfg, engine.RealClock{}.Now())
}

func onLine(line int) string {
	if line > 0 {
		return fmt.Sprintf(" (line %d)", line)
	}
	return ""
}

func warningsAt(cfg *Config, now time.Time) []Notice {
	var out []Notice
	where := func(p Path) string { return Describe(cfg.Raw, p) + onLine(cfg.LineOf(p)) }

	for _, e := range cfg.Projects() {
		for ci, cl := range e.Clusters {
			cp := join(e.ClustersBase, ci)
			clusterName := fmt.Sprint(cl["name"])
			free := cl["tier"] == "free"
			provider, _ := asObj(cl["cloudProvider"])["type"].(string)

			if !free {
				if _, ok := cl["deletionProtection"]; !ok {
					out = append(out, Notice{
						What: fmt.Sprintf("%s: deletionProtection is not set, so the cluster can be deleted by 'destroy' or 'cluster delete'",
							where(cp)),
						Action: fmt.Sprintf("Run 'cbctl capella cluster protect %s --project %s --on'; it records the setting in capella.yaml.",
							clusterName, e.Name()),
					})
				}
			}

			for i, c := range objList(cl["allowedCidrs"]) {
				raw := fmt.Sprint(c["cidr"])
				if norm, err := NormalizeCIDR(raw); err == nil && anyIPCIDRs[norm] && cl["allowAnyIp"] != true {
					p := join(cp, "allowedCidrs", i, "cidr")
					out = append(out, Notice{
						What: fmt.Sprintf("%s: %s lets every IP address connect, and capella.yaml does not record that this is "+
							"intended, so 'apply' will not create it where it does not exist yet", where(p), raw),
						Action: fmt.Sprintf("'cbctl capella sync --project %s' records what Capella allows, or "+
							"'cbctl capella cidr delete %s %s --project %s' stops allowing it.", e.Name(), clusterName, raw, e.Name()),
					})
				}
				if t, ok := ParseRFC3339(c["expiresAt"]); ok && !t.After(now) {
					p := join(cp, "allowedCidrs", i, "expiresAt")
					out = append(out, Notice{
						What: fmt.Sprintf("%s: expiresAt is in the past, so Capella will remove this IP range", where(p)),
						Action: fmt.Sprintf("Run 'cbctl capella cidr delete %s %s' to remove it now, or set a later expiresAt.",
							clusterName, raw),
					})
				}
			}

			for gi, g := range objList(cl["serviceGroups"]) {
				gp := join(cp, "serviceGroups", gi)
				node := asObj(g["node"])
				disk := asObj(node["disk"])
				if rec := recommendedIOPS(provider, disk); rec != 0 {
					p := join(gp, "node", "disk", "iops")
					out = append(out, Notice{
						What: fmt.Sprintf("%s: %v IOPS is below Capella's recommended %d for %v GB, and Capella may refuse a "+
							"lower value on the next change", where(p), disk["iops"], rec, disk["storage"]),
						Action: fmt.Sprintf("Run 'cbctl capella cluster update %s --group %s --disk-iops %d'.",
							clusterName, strings.Join(stringList(g["services"]), ","), rec),
					})
				}
				compute := asObj(node["compute"])
				cpu, okCPU := asInt(compute["cpu"])
				ram, okRAM := asInt(compute["ram"])
				if offered, known := presets.ComputeSizes()[provider]; known && okCPU && okRAM &&
					!offered.Offered(presets.Size{CPU: int(cpu), RAM: int(ram)}, cl["configurationType"] == "singleNode") {
					p := join(gp, "node", "compute")
					out = append(out, Notice{
						What: fmt.Sprintf("%s: %d vCPU / %d GB RAM is not a node size Capella documents for %s", where(p), cpu, ram, provider),
						Action: fmt.Sprintf("Check the size with 'cbctl capella cluster options %s'; Capella rejects sizes it does not offer.",
							provider),
					})
				}
			}

			for ui, u := range objList(cl["databaseCredentials"]) {
				for ai, a := range objList(u["access"]) {
					if len(objList(asObj(a["resources"])["buckets"])) == 0 {
						p := join(cp, "databaseCredentials", ui, "access", ai)
						out = append(out, Notice{
							What: fmt.Sprintf("%s: grants access to ALL buckets", where(p)),
							Action: fmt.Sprintf("If that is not intended, limit the access to one bucket with 'cbctl capella credential update %s %v --bucket BUCKET'.",
								clusterName, u["name"]),
						})
					}
				}
			}

			for bi, b := range objList(cl["buckets"]) {
				if _, ok := b["flush"]; ok {
					p := join(cp, "buckets", bi, "flush")
					out = append(out, Notice{
						What:   fmt.Sprintf("%s: flush is deprecated", where(p)),
						Action: "Rename it to flushEnabled.",
					})
				}
			}
		}
	}

	for _, hint := range cfg.Hints {
		out = append(out, Notice{
			What:   "hibernation uses an unquoted YAML boolean for a day (read as on/off)",
			Action: "Tip: " + hint + ".",
		})
	}
	if note := cfg.CredentialsOutputNote(); note != "" {
		out = append(out, Notice{What: note, Action: "Run 'cbctl capella doctor' to list everything left from capellactl."})
	}
	return out
}
