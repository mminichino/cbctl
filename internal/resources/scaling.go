package resources

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/plan"
)

// OtherServices are the services whose last nodes cannot be removed without --allow-service-removal.
var OtherServices = []string{"index", "query", "search", "analytics", "eventing"}

func servicesOf(g Obj) []string { return stringsOf(g["services"]) }

func runsData(g Obj) bool { return contains(servicesOf(g), "data") }

func dataGroups(groups []Obj) []Obj {
	var out []Obj
	for _, g := range groups {
		if runsData(g) {
			out = append(out, g)
		}
	}
	return out
}

// DataNodes is the number of nodes in the groups that run the data service.
func DataNodes(groups []Obj) int64 {
	var n int64
	for _, g := range dataGroups(groups) {
		n += intOf(g["numOfNodes"])
	}
	return n
}

// DataMemory is data nodes times RAM per node (GB): the data service's share the memory guard
// scales by.
func DataMemory(groups []Obj) int64 {
	var n int64
	for _, g := range dataGroups(groups) {
		ram := intOf(objOf(objOf(g["node"])["compute"])["ram"])
		n += intOf(g["numOfNodes"]) * ram
	}
	return n
}

func mergeObj(base, overlay Obj) Obj {
	out := make(Obj, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// AfterChange returns the service groups once the change is applied: desired values over
// observed ones, by group key. A nil desired means serviceGroups is not declared and nothing
// changes.
func AfterChange(observed, desired []Obj) []Obj {
	if desired == nil {
		return observed
	}
	byKey := map[string]Obj{}
	for _, g := range observed {
		byKey[plan.GroupKey(g)] = g
	}
	out := make([]Obj, 0, len(desired))
	for _, d := range desired {
		o := byKey[plan.GroupKey(d)]
		oNode, dNode := objOf(o["node"]), objOf(d["node"])
		node := mergeObj(oNode, dNode)
		node["compute"] = mergeObj(objOf(oNode["compute"]), objOf(dNode["compute"]))
		merged := mergeObj(o, d)
		merged["node"] = node
		out = append(out, merged)
	}
	return out
}

func mb(n float64) string { return commas(int64(math.RoundToEven(n))) }

// ReducesCapacity reports whether old to new removes a group, data nodes or data memory.
func ReducesCapacity(old, updated []Obj) bool {
	newKeys := map[string]bool{}
	for _, g := range updated {
		newKeys[plan.GroupKey(g)] = true
	}
	for _, g := range old {
		if !newKeys[plan.GroupKey(g)] {
			return true
		}
	}
	return DataNodes(updated) < DataNodes(old) || DataMemory(updated) < DataMemory(old)
}

// Check applies the scale-in and scale-down guards to a change from the old to the new service
// groups. It returns the reasons the change is blocked, and a "checks: …" remark when it passes.
// Capella's stats are read for the memory guard; when they cannot be read the change is blocked
// unless rc.SkipCapacityCheck is set. A transport error is returned as err.
func Check(ctx context.Context, rc *plan.RunContext, projectID, clusterID string, old, updated []Obj) (blocked []string, checks string, err error) {
	n, m := DataNodes(old), DataNodes(updated)
	if m == 0 {
		return []string{"scale-in blocked: removing the last group that runs data"}, "", nil
	}
	oldGroups := map[string]Obj{}
	var oldOrder []string
	for _, g := range old {
		k := plan.GroupKey(g)
		if _, seen := oldGroups[k]; !seen {
			oldOrder = append(oldOrder, k)
		}
		oldGroups[k] = g
	}
	newKeys := map[string]bool{}
	for _, g := range updated {
		newKeys[plan.GroupKey(g)] = true
	}
	var removed []string
	for _, k := range oldOrder {
		if !newKeys[k] {
			removed = append(removed, k)
		}
	}
	inGroups := func(groups []Obj, s string) bool {
		for _, g := range groups {
			if contains(servicesOf(g), s) {
				return true
			}
		}
		return false
	}
	var lost []string
	for _, s := range OtherServices {
		if inGroups(old, s) && !inGroups(updated, s) {
			lost = append(lost, s)
		}
	}
	for _, k := range removed {
		var services []string
		for _, s := range lost {
			if contains(servicesOf(oldGroups[k]), s) {
				services = append(services, s)
			}
		}
		var need []string
		if len(services) > 0 && !rc.AllowServiceRemoval {
			need = append(need, "--allow-service-removal")
		}
		if !rc.AllowServiceGroupRemoval {
			need = append(need, "--allow-service-group-removal")
		}
		if len(need) > 0 {
			what := "removes a whole service group"
			if len(services) > 0 {
				what = "removes the last " + strings.Join(services, ", ") + " nodes"
			}
			blocked = append(blocked, fmt.Sprintf("removing serviceGroups[%s] %s; pass %s", k, what, strings.Join(need, " ")))
		}
	}
	if len(removed) == 0 && len(lost) > 0 && !rc.AllowServiceRemoval {
		blocked = append(blocked, fmt.Sprintf("scale-in blocked: the last %s nodes would be removed; pass --allow-service-removal",
			strings.Join(lost, ", ")))
	}
	if len(blocked) > 0 {
		return blocked, "", nil
	}
	if DataMemory(updated) >= DataMemory(old) && m >= n {
		return nil, "", nil
	}

	var notes []string
	var change string
	if m != n {
		change = fmt.Sprintf("%d → %d nodes", n, m)
	} else {
		change = fmt.Sprintf("%d → %d GB per node", DataMemory(old)/max(n, 1), DataMemory(updated)/max(m, 1))
	}
	stats, err := rc.Client.GetClusterStats(ctx, projectID, clusterID)
	if err != nil {
		var apiErr *capellaapi.APIError
		if !errors.As(err, &apiErr) {
			return nil, "", err
		}
		stats = nil
	}
	if stats == nil {
		if !rc.SkipCapacityCheck {
			return []string{"cannot verify capacity (stats unavailable); pass --skip-capacity-check to proceed at your own risk"}, "", nil
		}
		notes = append(notes, "memory not checked (--skip-capacity-check)")
	} else {
		total, free := float64(stats.TotalMemoryInMb), float64(stats.FreeMemoryInMb)
		allocated := total - free
		projected := total * float64(DataMemory(updated)) / float64(max(DataMemory(old), 1))
		if allocated > projected {
			blocked = append(blocked, fmt.Sprintf("scale-in blocked: buckets allocate %s MB but data service would have ~%s MB after scale-in (%s)",
				mb(allocated), mb(projected), change))
		} else {
			notes = append(notes, fmt.Sprintf("memory ok (%s / ~%s MB)", mb(allocated), mb(projected)))
		}
	}

	api, err := newRawAPI(rc)
	if err != nil {
		return nil, "", err
	}
	buckets, err := api.list(ctx, api.buckets(projectID, clusterID), "data")
	if err != nil {
		return nil, "", err
	}
	var worst Obj
	var replicas int64
	for _, b := range buckets {
		if r := intOf(b["replicas"]); worst == nil || r > replicas {
			worst, replicas = b, r
		}
	}
	if worst != nil && replicas > m-1 {
		suffix := "s"
		if replicas == 1 {
			suffix = ""
		}
		blocked = append(blocked, fmt.Sprintf("scale-in blocked: bucket '%s' has %d replica%s and needs at least %d data nodes",
			strOf(worst, "name"), replicas, suffix, replicas+1))
	} else {
		s := ""
		if replicas != 0 {
			s = "s"
		}
		notes = append(notes, fmt.Sprintf("replicas ok (max %d, needs %d data node%s)", replicas, replicas+1, s))
	}
	if len(blocked) > 0 {
		return blocked, "", nil
	}
	return nil, "checks: " + strings.Join(notes, " · "), nil
}
