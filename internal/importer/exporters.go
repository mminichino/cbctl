package importer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/config/capellafile"
)

// Obj is the JSON-compatible mapping used for file documents and API bodies.
type Obj = capellafile.Obj

// Days are the hibernation days, Monday first.
var Days = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// diskKeys are the disk fields written to the file, in order.
var diskKeys = []string{"type", "storage", "iops", "autoExpansion"}

// systemScopes are never written to the file.
var systemScopes = map[string]bool{"_system": true}

// poweredOff lists the cluster states in which buckets and credentials cannot be read.
var poweredOff = map[string]bool{"turnedOff": true, "turningOff": true, "turningOn": true}

// PoweredOff reports whether a cluster in this state cannot be read for buckets and credentials.
func PoweredOff(state string) bool { return poweredOff[state] }

// bucketBackupDefaults are the weekly schedule fields of a bucket backup schedule and their defaults.
var bucketBackupDefaults = []struct {
	Key string
	Val any
}{
	{"dayOfWeek", "sunday"},
	{"startAt", int64(0)},
	{"incrementalEvery", int64(24)},
	{"retentionTime", "30days"},
	{"costOptimizedRetention", false},
}

// anyIP are the CIDRs that need allowAnyIp in the file.
var anyIP = map[string]bool{"0.0.0.0/0": true, "::/0": true}

// toObj converts an API struct to a normalized JSON-compatible mapping.
func toObj(v any) Obj {
	raw, err := json.Marshal(v)
	if err != nil {
		return Obj{}
	}
	var out Obj
	if err := json.Unmarshal(raw, &out); err != nil {
		return Obj{}
	}
	if n, ok := capellafile.NormalizeNumbers(out).(Obj); ok {
		return n
	}
	return out
}

func str(o Obj, k string) string {
	s, _ := o[k].(string)
	return s
}

func sub(o Obj, k string) Obj {
	m, _ := o[k].(Obj)
	return m
}

func list(o Obj, k string) []any {
	l, _ := o[k].([]any)
	return l
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int64:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case Obj:
		return len(t) > 0
	}
	return true
}

func intOf(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	return 0
}

func isFree(body Obj) bool {
	return strings.EqualFold(str(sub(body, "support"), "plan"), "free")
}

func pick(o Obj, keys ...string) Obj {
	out := Obj{}
	for _, k := range keys {
		if v, ok := o[k]; ok && truthy(v) {
			out[k] = v
		}
	}
	return out
}

// ExportFreeCluster is the free tier: only name, description and cloud provider are configurable;
// the id is always recorded.
func ExportFreeCluster(body Obj, full bool) Obj {
	out := Obj{"id": body["id"], "name": body["name"]}
	if truthy(body["description"]) || full {
		out["description"] = str(body, "description")
	}
	out["tier"] = "free"
	out["cloudProvider"] = pick(sub(body, "cloudProvider"), "type", "region", "cidr")
	return out
}

// ExportCluster converts a provisioned (or free) cluster body to its file mapping. Values equal
// to the defaults are left out unless full is set; the id is written when pinID is set.
func ExportCluster(body Obj, full, pinID bool) Obj {
	if isFree(body) {
		return ExportFreeCluster(body, full)
	}
	out := Obj{}
	if pinID {
		out["id"] = body["id"]
	}
	out["name"] = body["name"]
	if truthy(body["description"]) || full {
		out["description"] = str(body, "description")
	}
	ct := str(body, "configurationType")
	if ct == "" {
		ct = "multiNode"
	}
	if ct != "multiNode" || full {
		out["configurationType"] = ct
	}
	out["cloudProvider"] = pick(sub(body, "cloudProvider"), "type", "region", "cidr")
	if v := str(sub(body, "couchbaseServer"), "version"); v != "" {
		out["couchbaseServer"] = Obj{"version": v}
	}
	avail := str(sub(body, "availability"), "type")
	if avail == "" {
		avail = "multi"
	}
	out["availability"] = Obj{"type": avail}
	out["support"] = pick(sub(body, "support"), "plan", "timezone")
	groups := []any{}
	for _, gv := range list(body, "serviceGroups") {
		g, _ := gv.(Obj)
		node := sub(g, "node")
		diskSrc := sub(node, "disk")
		disk := Obj{}
		for _, k := range diskKeys {
			if v, ok := diskSrc[k]; ok {
				disk[k] = v
			}
		}
		compute := sub(node, "compute")
		services := []string{}
		for _, s := range list(g, "services") {
			services = append(services, fmt.Sprint(s))
		}
		sort.Strings(services)
		svc := make([]any, len(services))
		for i, s := range services {
			svc[i] = s
		}
		groups = append(groups, Obj{
			"node":       Obj{"compute": Obj{"cpu": compute["cpu"], "ram": compute["ram"]}, "disk": disk},
			"numOfNodes": g["numOfNodes"],
			"services":   svc,
		})
	}
	out["serviceGroups"] = groups
	if truthy(body["cmekId"]) {
		out["cmekId"] = body["cmekId"]
	}
	if _, has := body["enablePrivateDNSResolution"]; truthy(body["enablePrivateDNSResolution"]) || (full && has) {
		out["enablePrivateDNSResolution"] = truthy(body["enablePrivateDNSResolution"])
	}
	if truthy(body["deletionProtection"]) || full {
		out["deletionProtection"] = truthy(body["deletionProtection"])
	}
	return out
}

func hhmm(b Obj, end bool) string {
	h, m := intOf(b["hour"]), intOf(b["minute"])
	if end && h == 0 && m == 0 {
		return "24:00"
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

func dayValue(d Obj) string {
	if state := str(d, "state"); state == "on" || state == "off" {
		return state
	}
	return hhmm(sub(d, "from"), false) + "-" + hhmm(sub(d, "to"), true)
}

// StartHM turns '02:00' or an ISO 8601 instant into 'HH:MM' in UTC; "" when absent or unreadable.
func StartHM(value any) string {
	text, _ := value.(string)
	if text == "" {
		return ""
	}
	if len(text) == 5 && text[2] == ':' {
		return text
	}
	t, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return ""
	}
	t = t.UTC()
	return fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
}

// ExportSnapshotSchedule converts a cloud snapshot schedule: startTime as the UTC time of day.
func ExportSnapshotSchedule(body Obj) Obj {
	out := Obj{"interval": body["interval"], "retention": body["retention"]}
	if hm := StartHM(body["startTime"]); hm != "" {
		out["startTime"] = hm
	}
	if regions := list(body, "copyToRegions"); len(regions) > 0 {
		out["copyToRegions"] = append([]any(nil), regions...)
	}
	return out
}

// ExportBucketBackupSchedule converts a bucket backup schedule: the weeklySchedule fields, flat.
func ExportBucketBackupSchedule(body Obj) Obj {
	weekly := sub(body, "weeklySchedule")
	out := Obj{}
	for _, f := range bucketBackupDefaults {
		if v, ok := weekly[f.Key]; ok {
			out[f.Key] = v
		} else {
			out[f.Key] = f.Val
		}
	}
	return out
}

// ExportSchedule converts the API form of a hibernation schedule (seven days, Monday first) to
// the friendly form, compacting weekdays and weekend when uniform.
func ExportSchedule(body Obj) Obj {
	byDay := map[string]string{}
	for _, dv := range list(body, "days") {
		if d, ok := dv.(Obj); ok {
			byDay[str(d, "day")] = dayValue(d)
		}
	}
	out := Obj{"timezone": body["timezone"]}
	overrides := Obj{}
	for _, grp := range []struct {
		name string
		days []string
	}{{"weekdays", Days[:5]}, {"weekend", Days[5:]}} {
		var values []string
		for _, d := range grp.days {
			if v, ok := byDay[d]; ok {
				values = append(values, v)
			}
		}
		if len(values) == 0 {
			continue
		}
		common, best := values[0], 0
		for i, v := range values { // ties: the earliest day's value
			n := 0
			for _, w := range values {
				if w == v {
					n++
				}
			}
			if n > best || (n == best && i == 0) {
				common, best = v, n
			}
		}
		out[grp.name] = common
		for _, d := range grp.days {
			if v, ok := byDay[d]; ok && v != common {
				overrides[d] = v
			}
		}
	}
	for k, v := range overrides {
		out[k] = v
	}
	if str(body, "activationStatus") == "paused" {
		out["paused"] = true
	}
	return out
}

// ExportCIDR converts an allowed CIDR entry.
func ExportCIDR(c Obj) Obj {
	out := Obj{"cidr": c["cidr"]}
	if truthy(c["comment"]) {
		out["comment"] = c["comment"]
	}
	if truthy(c["expiresAt"]) {
		out["expiresAt"] = c["expiresAt"]
	}
	return out
}

var bucketFields = []string{"type", "storageBackend", "vbuckets", "memoryAllocationInMb", "bucketConflictResolution",
	"durabilityLevel", "replicas", "flushEnabled", "timeToLiveInSeconds", "evictionPolicy", "priority",
	"enableCrossClusterVersioning"}

// ExportBucket converts a bucket and its scopes. Values equal to the defaults are left out unless
// full is set; the _system scope and the _default collection of _default are dropped.
func ExportBucket(b Obj, scopes []Obj, full, singleNode bool) Obj {
	src := Obj{}
	for k, v := range b {
		src[k] = v
	}
	if _, ok := src["flushEnabled"]; !ok {
		if f, ok := src["flush"]; ok {
			src["flushEnabled"] = truthy(f)
		}
	}
	defaults := capellafile.BucketDefaults(singleNode)
	out := Obj{"name": b["name"]}
	for _, f := range bucketFields {
		v, ok := src[f]
		if !ok || v == nil {
			continue
		}
		if f == "storageBackend" && str(src, "type") == "ephemeral" {
			continue
		}
		if !full {
			if d, has := defaults[f]; has && fmt.Sprint(v) == fmt.Sprint(d) {
				continue
			}
			if f == "priority" && !truthy(v) {
				continue
			}
			if f == "enableCrossClusterVersioning" && !truthy(v) {
				continue
			}
		}
		out[f] = v
	}
	exported := []any{}
	for _, s := range scopes {
		name := str(s, "name")
		if systemScopes[name] {
			continue
		}
		var colls []Obj
		for _, cv := range list(s, "collections") {
			c, _ := cv.(Obj)
			if name == "_default" && str(c, "name") == "_default" {
				continue
			}
			colls = append(colls, c)
		}
		if name == "_default" && len(colls) == 0 {
			continue
		}
		entry := Obj{"name": name}
		if len(colls) > 0 {
			cs := make([]any, len(colls))
			for i, c := range colls {
				item := Obj{"name": c["name"]}
				if ttl, ok := c["maxTTL"]; full || (ok && intOf(ttl) != 0) {
					item["maxTTL"] = ttl
					if !ok {
						item["maxTTL"] = int64(0)
					}
				}
				cs[i] = item
			}
			entry["collections"] = cs
		}
		exported = append(exported, entry)
	}
	if len(exported) > 0 {
		out["scopes"] = exported
	}
	return out
}

// Wildcard is what Capella returns for "every bucket/scope/collection".
const Wildcard = "*"

// WithoutWildcards strips the wildcard levels Capella returns for whole-bucket and whole-scope
// grants; the file writes those levels by leaving them out. It returns new mappings.
func WithoutWildcards(access []any) []Obj {
	var out []Obj
	for _, av := range access {
		entry, _ := av.(Obj)
		e := Obj{}
		for k, v := range entry {
			if k != "resources" {
				e[k] = v
			}
		}
		res := sub(entry, "resources")
		buckets := list(res, "buckets")
		hasWild := false
		for _, bv := range buckets {
			if b, _ := bv.(Obj); str(b, "name") == Wildcard {
				hasWild = true
			}
		}
		switch {
		case len(buckets) > 0 && !hasWild:
			rb := []any{}
			for _, bv := range buckets {
				b, _ := bv.(Obj)
				item := Obj{}
				for k, v := range b {
					if k != "scopes" {
						item[k] = v
					}
				}
				scopes := list(b, "scopes")
				scopeWild := false
				for _, sv := range scopes {
					if s, _ := sv.(Obj); str(s, "name") == Wildcard {
						scopeWild = true
					}
				}
				if len(scopes) > 0 && !scopeWild {
					ns := []any{}
					for _, sv := range scopes {
						s, _ := sv.(Obj)
						sc := Obj{}
						for k, v := range s {
							if k != "collections" {
								sc[k] = v
							}
						}
						colls := list(s, "collections")
						collWild := false
						for _, c := range colls {
							if c == Wildcard {
								collWild = true
							}
						}
						if len(colls) > 0 && !collWild {
							sc["collections"] = append([]any(nil), colls...)
						}
						ns = append(ns, sc)
					}
					item["scopes"] = ns
				}
				rb = append(rb, item)
			}
			ne := Obj{}
			for k, v := range res {
				ne[k] = v
			}
			ne["buckets"] = rb
			e["resources"] = ne
		case res != nil && len(buckets) == 0:
			e["resources"] = res
		}
		out = append(out, e)
	}
	return out
}

// ExportCredential converts a basic credential: name and access (never a password).
func ExportCredential(u Obj) Obj {
	access := []any{}
	for _, a := range WithoutWildcards(list(u, "access")) {
		privs := []string{}
		for _, p := range list(a, "privileges") {
			privs = append(privs, fmt.Sprint(p))
		}
		sort.Strings(privs)
		pv := make([]any, len(privs))
		for i, p := range privs {
			pv[i] = p
		}
		entry := Obj{"privileges": pv}
		if res := sub(a, "resources"); res != nil && len(list(res, "buckets")) > 0 {
			buckets := []any{}
			for _, bv := range list(res, "buckets") {
				b, _ := bv.(Obj)
				rb := Obj{"name": b["name"]}
				if scopes := list(b, "scopes"); len(scopes) > 0 {
					ss := []any{}
					for _, sv := range scopes {
						s, _ := sv.(Obj)
						item := Obj{"name": s["name"]}
						if colls := list(s, "collections"); len(colls) > 0 {
							item["collections"] = append([]any(nil), colls...)
						}
						ss = append(ss, item)
					}
					rb["scopes"] = ss
				}
				buckets = append(buckets, rb)
			}
			entry["resources"] = Obj{"buckets": buckets}
		}
		access = append(access, entry)
	}
	return Obj{"name": u["name"], "access": access}
}

// IsAdvanced reports a credential that uses userRoles, which cbctl does not manage.
func IsAdvanced(u Obj) bool {
	return truthy(u["userRoles"]) || str(u, "credentialType") == "advanced"
}
