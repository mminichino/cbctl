package capellafile

// HibernationBooleanHint is the tip recorded when a hibernation day key was written as a YAML
// boolean.
const HibernationBooleanHint = `quote "on"/"off" in hibernation to avoid YAML boolean conversion`

// DayKeys are the hibernation keys that carry an on/off/window value.
var DayKeys = []string{"weekdays", "weekend", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// bucketDefaults are applied to the buckets of provisioned clusters; priority is deliberately
// not defaulted (Capella picks it from the server version).
var bucketDefaults = map[string]any{
	"type":                     "couchbase",
	"replicas":                 int64(1),
	"durabilityLevel":          "none",
	"timeToLiveInSeconds":      int64(0),
	"flushEnabled":             false,
	"bucketConflictResolution": "seqno",
}

// BucketDefaults returns a copy of the defaults applied to a bucket of a provisioned cluster
// (replicas is 0 instead of 1 on single-node clusters).
func BucketDefaults(singleNode bool) Obj {
	out := make(Obj, len(bucketDefaults))
	for k, v := range bucketDefaults {
		out[k] = v
	}
	if singleNode {
		out["replicas"] = int64(0)
	}
	return out
}

// ApplyDefaults fills in defaults on the in-memory document only; the file is never changed.
// Free-tier clusters keep their fixed shape (no defaults). The boolean shorthand in hibernation
// day keys (YAML true/false) is normalized to "on"/"off" and a hint is returned for Warnings.
func ApplyDefaults(raw Obj) (hints []string) {
	for _, e := range projectEntries(raw) {
		for _, cl := range e.Clusters {
			if _, ok := cl["tier"]; !ok {
				cl["tier"] = "provisioned"
			}
			if hib, ok := cl["hibernation"].(Obj); ok {
				hints = append(hints, normalizeHibernationBooleans(hib)...)
			}
			if cl["tier"] == "free" {
				continue
			}
			if _, ok := cl["configurationType"]; !ok {
				cl["configurationType"] = "multiNode"
			}
			single := cl["configurationType"] == "singleNode"
			defaults := BucketDefaults(single)
			for _, b := range objList(cl["buckets"]) {
				for k, v := range defaults {
					if _, ok := b[k]; !ok {
						b[k] = v
					}
				}
			}
		}
	}
	return dedupe(hints)
}

// normalizeHibernationBooleans turns unquoted YAML booleans in day keys into "on"/"off". YAML
// 1.1 parsers read on/off as booleans; the tip tells the user to quote them.
func normalizeHibernationBooleans(hib Obj) []string {
	var hints []string
	for _, k := range DayKeys {
		if b, ok := hib[k].(bool); ok {
			if b {
				hib[k] = "on"
			} else {
				hib[k] = "off"
			}
			hints = append(hints, HibernationBooleanHint)
		}
	}
	return hints
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
