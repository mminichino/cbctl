package importer

import (
	"fmt"
	"strings"
)

// UnreadLabels name the cluster parts that KeepUnread can carry over.
var UnreadLabels = map[string]string{
	"allowedCidrs":        "allowed CIDRs",
	"buckets":             "buckets, scopes and collections",
	"databaseCredentials": "database credentials",
	"hibernation":         "hibernation schedule",
	"backupSchedule":      "backup schedule",
}

func objList(v any) []Obj {
	var out []Obj
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if m, ok := x.(Obj); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// CarryOverProject keeps the values that exist only locally for one project, because Capella
// never returns them: allowAnyIp and password references. prev is the project mapping of the
// current file with its "clusters". It returns what it kept, for the stderr notes.
func CarryOverProject(prev Obj, res *Result) []string {
	var kept []string
	if prev == nil {
		return kept
	}
	oldClusters := map[string]Obj{}
	for _, c := range objList(prev["clusters"]) {
		oldClusters[str(c, "name")] = c
	}
	for _, cl := range objList(res.Data["clusters"]) {
		old, ok := oldClusters[str(cl, "name")]
		if !ok {
			continue
		}
		if truthy(old["allowAnyIp"]) {
			cl["allowAnyIp"] = true
		}
		oldPW := map[string]string{}
		for _, u := range objList(old["databaseCredentials"]) {
			if pw := str(u, "password"); pw != "" {
				oldPW[str(u, "name")] = pw
			}
		}
		for _, u := range objList(cl["databaseCredentials"]) {
			pw, ok := oldPW[str(u, "name")]
			if !ok {
				continue
			}
			u["password"] = pw
			for k := range u {
				if strings.HasPrefix(k, commentPrefix) {
					res.Comments[k] = "password reference kept from the previous capella.yaml (Capella never returns passwords)"
				}
			}
			kept = append(kept, fmt.Sprintf("password reference for %s/%s", str(cl, "name"), str(u, "name")))
		}
	}
	return kept
}

func findByName(items []Obj, name string) Obj {
	for _, b := range items {
		if str(b, "name") == name {
			return b
		}
	}
	return nil
}

// KeepUnread keeps what Capella could not return now (a powered-off cluster, or a read error)
// from the previous file. Dropping it would remove buckets and credentials that still exist from
// the file, and `apply --prune` would then delete them in Capella. The next sync that can read
// them refreshes them.
func KeepUnread(prev Obj, res *Result, fileName string) {
	if prev == nil || len(res.Unread) == 0 {
		return
	}
	oldClusters := objList(prev["clusters"])
	for _, cl := range objList(res.Data["clusters"]) {
		info := res.Unread[str(cl, "id")]
		if info == nil {
			continue
		}
		var old Obj
		for _, c := range oldClusters {
			if id := str(c, "id"); id != "" && id == str(cl, "id") {
				old = c
				break
			}
		}
		if old == nil {
			for _, c := range oldClusters {
				if str(c, "id") == "" && str(c, "name") == str(cl, "name") {
					old = c
					break
				}
			}
		}
		if old == nil {
			continue
		}
		var kept []string
		moved := map[string]any{}
		unkept := false
		for _, f := range info.Fields {
			if part, bucket, ok := strings.Cut(f, ":"); ok && (part == "scopes" || part == "bucketBackup") {
				field := "backupSchedule"
				label := "backup schedule of bucket " + bucket
				if part == "scopes" {
					field, label = "scopes", "scopes of bucket "+bucket
				}
				ob := findByName(objList(old["buckets"]), bucket)
				nb := findByName(objList(cl["buckets"]), bucket)
				if ob != nil && truthy(ob[field]) && nb != nil {
					nb[field] = ob[field]
					kept = append(kept, label)
				}
				continue
			}
			switch {
			case truthy(old[f]):
				moved[f] = old[f]
				kept = append(kept, UnreadLabels[f])
			case f != "backupSchedule": // no schedule in the file: nothing to keep, nothing lost
				unkept = true
			}
		}
		if len(kept) == 0 {
			continue
		}
		info.Kept = true
		if !unkept { // everything that was not read is kept: replace the "not imported" notes and warnings
			for k := range cl {
				if strings.HasPrefix(k, commentPrefix) && strings.HasPrefix(res.Comments[k], "not imported:") {
					delete(cl, k)
				}
			}
			var warnings []Notice
			for _, w := range res.Warnings {
				if strings.HasPrefix(w.What, "cluster "+info.Name+": ") && strings.Contains(w.What, " not imported (") {
					continue
				}
				warnings = append(warnings, w)
			}
			res.Warnings = warnings
		}
		key := fmt.Sprintf("%skept_%s__", commentPrefix, str(cl, "id"))
		cl[key] = ""
		res.Comments[key] = fmt.Sprintf("kept from the previous %s: %s\n(not readable now: %s; the next sync refreshes them)",
			fileName, strings.Join(kept, ", "), info.Reason)
		for f, v := range moved {
			cl[f] = v
		}
		res.Warnings = append(res.Warnings, Notice{
			What:   fmt.Sprintf("cluster %s: %s kept from %s (%s)", info.Name, strings.Join(kept, ", "), fileName, info.Reason),
			Action: "Run 'cbctl capella sync' again when the cluster is on to refresh them.",
		})
	}
}
