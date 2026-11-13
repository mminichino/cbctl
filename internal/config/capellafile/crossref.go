package capellafile

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/presets"
	"github.com/mminichino/cbctl/internal/secrets"
)

var rfc3339Re = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$`)

// FreeTierRegions lists the regions that offer free-tier clusters, per provider.
var FreeTierRegions = map[string][]string{
	"aws":   {"us-east-2", "eu-west-1", "ap-southeast-1"},
	"gcp":   {"us-central1", "europe-west1", "asia-east1"},
	"azure": {"eastus", "swedencentral", "koreacentral"},
}

var (
	singleNodeServices = map[string]bool{"data": true, "index": true, "query": true, "search": true}

	freeTierForbidden = []string{"configurationType", "couchbaseServer", "serviceGroups", "availability", "support",
		"zones", "cmekId", "enablePrivateDNSResolution", "hibernation", "backupSchedule"}
	provisionedRequired = []string{"availability", "support", "serviceGroups"}

	freeTierBucketFields = map[string]bool{"name": true, "memoryAllocationInMb": true, "enableCrossClusterVersioning": true, "scopes": true}

	// diskTypes lists the disk types each provider offers.
	diskTypes = map[string][]string{
		"aws":   {"gp3", "io2"},
		"azure": {"P10", "P15", "P20", "P30", "P40", "P50", "P6", "P60", "Ultra"},
		"gcp":   {"pd-ssd"},
	}
)

// regionShape describes how a provider names its regions (shape only, so new regions pass).
type regionShape struct {
	re       *regexp.Regexp
	examples string
}

var (
	providerOrder = []string{"aws", "gcp", "azure"}
	regionShapes  = map[string]regionShape{
		"aws":   {regexp.MustCompile(`^[a-z]{2}(-gov)?-[a-z]+-\d+$`), "us-east-1, eu-west-1, ap-southeast-1"},
		"gcp":   {regexp.MustCompile(`^[a-z]+-[a-z]+\d+$`), "us-east1, europe-west1, asia-east1"},
		"azure": {regexp.MustCompile(`^[a-z]+\d*$`), "eastus, westeurope, centralindia"},
	}
)

const diskHint = " (see 'cbctl capella cluster disk-types')"

var (
	keyspaceNameRe = regexp.MustCompile(`^[A-Za-z0-9_%-]{1,251}$`)

	ephemeralEviction   = []string{"noEviction", "nruEviction"}
	ephemeralDurability = []string{"none", "majority"}
)

// anyIPCIDRs are the networks that allow every address.
var anyIPCIDRs = map[string]bool{"0.0.0.0/0": true, "::/0": true}

// NormalizeCIDR returns the canonical form of a network: a bare IP becomes /32 (or /128). It
// returns an error when the value is not a network address (host bits set) or not valid.
func NormalizeCIDR(value string) (string, error) {
	text := strings.TrimSpace(value)
	if !strings.Contains(text, "/") {
		if strings.Contains(text, ":") {
			text += "/128"
		} else {
			text += "/32"
		}
	}
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return "", err
	}
	if prefix.Masked() != prefix {
		return "", fmt.Errorf("%s has host bits set", value)
	}
	return prefix.String(), nil
}

// SuggestedNetwork returns the network address a value with host bits set probably meant, for a
// "did you mean" hint; ok is false when value is not a CIDR at all.
func SuggestedNetwork(value string) (string, bool) {
	text := strings.TrimSpace(value)
	if !strings.Contains(text, "/") {
		return "", false
	}
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return "", false
	}
	return prefix.Masked().String(), true
}

// KeyspaceNameIssue returns the naming-rule violation of a scope or collection name, or "" when
// the name is valid. kind is "scope" or "collection". The names are 1-251 characters of A-Z a-z
// 0-9 _ - % that do not start with _ or %; the _default scope is the only exception.
func KeyspaceNameIssue(kind string, name any) string {
	if kind == "scope" && name == "_default" {
		return ""
	}
	text := fmt.Sprint(name)
	if s, ok := name.(string); ok && keyspaceNameRe.MatchString(s) && !strings.ContainsAny(s[:1], "_%") {
		return ""
	}
	shown := text
	if r := []rune(text); len(r) > 40 {
		shown = fmt.Sprintf("%s… (%d characters)", string(r[:40]), len(r))
	}
	rule := "1–251 characters of A-Z a-z 0-9 _ - %, not starting with _ or %"
	if kind == "scope" {
		rule += " (_default is the only exception)"
	}
	return fmt.Sprintf("%s name '%s' is not valid: use %s", kind, shown, rule)
}

// ParseRFC3339 returns the time for an RFC 3339 string; ok is false when v is not one.
func ParseRFC3339(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok || !rfc3339Re.MatchString(strings.TrimSpace(s)) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, strings.ToUpper(strings.TrimSpace(s)))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// BucketTypeRule is a setting a bucket's type does not allow.
type BucketTypeRule struct{ Field, Message string }

// BucketTypeRules returns the settings the bucket's type (couchbase or ephemeral) does not
// allow.
func BucketTypeRules(b Obj) []BucketTypeRule {
	var out []BucketTypeRule
	ephemeral := b["type"] == "ephemeral"
	eviction := asString(b["evictionPolicy"])
	if ephemeral {
		if _, ok := b["storageBackend"]; ok {
			out = append(out, BucketTypeRule{"storageBackend", "not allowed for ephemeral buckets (they keep data in memory only)"})
		}
		if eviction != "" && !containsKey(ephemeralEviction, eviction) {
			out = append(out, BucketTypeRule{"evictionPolicy",
				fmt.Sprintf("%s is not valid for ephemeral buckets (use noEviction or nruEviction)", eviction)})
		}
		if d := asString(b["durabilityLevel"]); d != "" && !containsKey(ephemeralDurability, d) {
			out = append(out, BucketTypeRule{"durabilityLevel",
				fmt.Sprintf("%s needs disk persistence; ephemeral buckets allow none or majority", d)})
		}
	} else if eviction != "" && eviction != "fullEviction" {
		out = append(out, BucketTypeRule{"evictionPolicy",
			fmt.Sprintf("%s is only valid for ephemeral buckets (couchbase buckets use fullEviction)", eviction)})
	}
	if _, ok := b["vbuckets"]; ok && b["storageBackend"] != "magma" {
		out = append(out, BucketTypeRule{"vbuckets", "only allowed with storageBackend: magma"})
	}
	return out
}

// DayValue returns the friendly value of one hibernation day: the day's own key, else weekdays
// or weekend, else "on".
func DayValue(hib Obj, day string) string {
	group := "weekend"
	switch day {
	case "monday", "tuesday", "wednesday", "thursday", "friday":
		group = "weekdays"
	}
	v, ok := hib[day]
	if !ok {
		v, ok = hib[group]
	}
	if !ok {
		return "on"
	}
	if b, isBool := v.(bool); isBool {
		if b {
			return "on"
		}
		return "off"
	}
	return fmt.Sprint(v)
}

// weekDays lists the days Monday to Sunday.
var weekDays = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

type crossChecker struct {
	cfg    *Config
	issues []Issue
}

func (c *crossChecker) add(p Path, msg string, opts ...string) {
	topic, fix := "config", ""
	if len(opts) > 0 && opts[0] != "" {
		topic = opts[0]
	}
	if len(opts) > 1 {
		fix = opts[1]
	}
	c.issues = append(c.issues, Issue{Path: FormatPath(p), Message: msg, Line: c.cfg.LineOf(p), Topic: topic, Fix: fix})
}

func join(p Path, more ...any) Path {
	out := make(Path, 0, len(p)+len(more))
	out = append(out, p...)
	return append(out, more...)
}

// dupes reports entries of items whose key value (after norm) repeats an earlier one. indexes
// gives each item's index in the list at base.
func (c *crossChecker) dupes(items []Obj, indexes []int, key string, base Path, label string, norm func(string) (string, error)) {
	seen := map[string]int{}
	for n, it := range items {
		raw, ok := it[key]
		if !ok || raw == nil {
			continue
		}
		k := fmt.Sprint(raw)
		if norm != nil {
			var err error
			if k, err = norm(k); err != nil {
				continue
			}
		}
		idx := n
		if indexes != nil {
			idx = indexes[n]
		}
		if first, dup := seen[k]; dup {
			where := ""
			if line := c.cfg.LineOf(join(base, first)); line > 0 {
				where = fmt.Sprintf(" (also on line %d)", line)
			}
			c.add(join(base, idx, key), fmt.Sprintf("duplicate %s name '%v'%s", label, raw, where))
		} else {
			seen[k] = idx
		}
	}
}

func (c *crossChecker) dupesAll(items []Obj, key string, base Path, label string, norm func(string) (string, error)) {
	c.dupes(items, nil, key, base, label, norm)
}

// CheckCrossReferences applies the rules JSON Schema cannot express: provider and region shape,
// free-tier and provisioned cluster rules, service groups, disks, allowed CIDRs, hibernation,
// buckets, keyspace names, credentials and duplicates. The document must be schema-valid and
// have defaults applied. It is a port of capellactl's check_cross_references.
func CheckCrossReferences(cfg *Config) []Issue {
	c := &crossChecker{cfg: cfg}
	entries := cfg.Projects()

	if _, ok := cfg.Raw["projects"]; ok {
		var unpinned []Obj
		var indexes []int
		for i, e := range entries {
			if asString(e.Data["id"]) == "" {
				unpinned = append(unpinned, e.Data)
				indexes = append(indexes, i)
			}
		}
		c.dupes(unpinned, indexes, "name", Path{"projects"}, "project", nil)
	}

	freeSeen := 0
	for _, e := range entries {
		for i, cl := range e.Clusters {
			if cl["tier"] != "free" {
				continue
			}
			freeSeen++
			if freeSeen > 1 {
				c.add(join(e.ClustersBase, i, "tier"), "only one free-tier cluster is allowed per organization")
			}
		}
	}
	for _, e := range entries {
		c.checkProject(e)
	}
	return c.issues
}

func (c *crossChecker) checkProject(e ProjectEntry) {
	clusters, cb := e.Clusters, e.ClustersBase

	// Clusters pinned with id: may share a name (Capella allows it); only unpinned duplicates are ambiguous.
	seen := map[string]int{}
	for i, cl := range clusters {
		if asString(cl["id"]) != "" {
			continue
		}
		n := fmt.Sprint(cl["name"])
		if first, dup := seen[n]; dup {
			where := ""
			if line := c.cfg.LineOf(join(cb, first)); line > 0 {
				where = fmt.Sprintf(" (also on line %d)", line)
			}
			c.add(join(cb, i, "name"), fmt.Sprintf("duplicate cluster name '%s'%s", n, where))
		} else {
			seen[n] = i
		}
	}

	// Cluster networks: valid network addresses that do not overlap other clusters of the project.
	type clusterNet struct {
		idx int
		net netip.Prefix
	}
	var nets []clusterNet
	for ci, cl := range clusters {
		raw, _ := asObj(cl["cloudProvider"])["cidr"].(string)
		if raw == "" {
			continue
		}
		path := join(cb, ci, "cloudProvider", "cidr")
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			c.add(path, fmt.Sprintf("%s is not a valid CIDR", raw))
			continue
		}
		if prefix.Masked() != prefix {
			c.add(path, fmt.Sprintf("%s is not a network address (did you mean %s?)", raw, prefix.Masked()))
			continue
		}
		for _, other := range nets {
			if prefix.Overlaps(other.net) {
				c.add(path, fmt.Sprintf("%s overlaps the network of cluster '%v' (%s)", raw, clusters[other.idx]["name"], other.net))
				break
			}
		}
		nets = append(nets, clusterNet{ci, prefix})
	}

	for ci, cl := range clusters {
		cp := join(cb, ci)
		c.checkCIDRs(cl, cp)
		c.checkHibernation(cl, cp)
		c.checkRegion(cl, cp)
		if cl["tier"] == "free" {
			c.checkFreeCluster(cl, cp)
			continue
		}
		c.checkProvisioned(e, cl, cp)
	}
}

func (c *crossChecker) checkRegion(cl Obj, cp Path) {
	cloud := asObj(cl["cloudProvider"])
	prov, _ := cloud["type"].(string)
	region, _ := cloud["region"].(string)
	shape, known := regionShapes[prov]
	if !known || region == "" || strings.Contains(region, "${") {
		return
	}
	if shape.re.MatchString(region) {
		return
	}
	article := func(p string) string {
		if strings.ContainsRune("aeiou", rune(p[0])) {
			return "an " + p
		}
		return "a " + p
	}
	looks := ""
	for _, other := range providerOrder {
		if other != prov && regionShapes[other].re.MatchString(region) {
			looks = fmt.Sprintf("; it looks like %s region", article(other))
			break
		}
	}
	c.add(join(cp, "cloudProvider", "region"),
		fmt.Sprintf("%s is not %s region name%s (%s regions look like %s)", region, article(prov), looks, prov, shape.examples))
}

func (c *crossChecker) checkCIDRs(cl Obj, cp Path) {
	cidrs := objList(cl["allowedCidrs"])
	for i, entry := range cidrs {
		raw, _ := entry["cidr"].(string)
		if v, ok := entry["expiresAt"]; ok {
			if _, valid := ParseRFC3339(v); !valid {
				c.add(join(cp, "allowedCidrs", i, "expiresAt"),
					fmt.Sprintf("%v is not an RFC 3339 time (for example 2026-12-31T23:59:59Z)", v))
			}
		}
		if _, err := NormalizeCIDR(raw); err != nil {
			if net, ok := SuggestedNetwork(raw); ok {
				c.add(join(cp, "allowedCidrs", i, "cidr"), fmt.Sprintf("%s is not a network address (did you mean %s?)", raw, net))
			} else {
				c.add(join(cp, "allowedCidrs", i, "cidr"), fmt.Sprintf("%s is not a valid CIDR", raw))
			}
		}
	}
	c.dupesAll(cidrs, "cidr", join(cp, "allowedCidrs"), "allowed CIDR", NormalizeCIDR)
}

var hourMinuteRe = regexp.MustCompile(`^\d+$`)

func (c *crossChecker) checkHibernation(cl Obj, cp Path) {
	hib, ok := cl["hibernation"].(Obj)
	if !ok {
		return
	}
	for _, day := range DayKeys {
		value, ok := hib[day].(string)
		if !ok || value == "on" || value == "off" || !strings.Contains(value, "-") {
			continue
		}
		start, end, _ := strings.Cut(value, "-")
		var problems []string
		for _, side := range []struct{ label, t string }{{"start", start}, {"end", end}} {
			h, m, _ := strings.Cut(side.t, ":")
			if !hourMinuteRe.MatchString(h) || !hourMinuteRe.MatchString(m) {
				continue
			}
			if m != "00" && m != "30" {
				problems = append(problems, fmt.Sprintf("%s minute must be 00 or 30 (got %s)", side.label, side.t))
			}
			if hour := atoi(h); hour > 23 && !(side.label == "end" && side.t == "24:00") {
				problems = append(problems, fmt.Sprintf("%s hour must be 00–23 (got %s)", side.label, side.t))
			}
		}
		if len(problems) == 0 && start >= strings.ReplaceAll(end, "24:00", "99:99") {
			problems = append(problems, fmt.Sprintf("the on-window must start before it ends (got %s)", value))
		}
		for _, msg := range problems {
			c.add(join(cp, "hibernation", day), msg, "hibernation")
		}
	}
	if hib["ensure"] == "absent" {
		return
	}
	if tz, _ := hib["timezone"].(string); tz == "" {
		c.add(join(cp, "hibernation"), "timezone is required (for example US/Pacific)", "hibernation")
	}
	allOff := true
	for _, d := range weekDays {
		if DayValue(hib, d) != "off" {
			allOff = false
			break
		}
	}
	if allOff {
		c.add(join(cp, "hibernation"), "the cluster cannot be off every day of the week (Capella rule)", "hibernation")
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			break
		}
	}
	return n
}

func (c *crossChecker) checkFreeCluster(cl Obj, cp Path) {
	for _, f := range freeTierForbidden {
		if _, ok := cl[f]; ok {
			c.add(join(cp, f), fmt.Sprintf("not available for free-tier clusters (they have 1 node with data, index, query "+
				"and search, and a fixed size); remove %s or use tier: provisioned", f))
		}
	}
	cloud := asObj(cl["cloudProvider"])
	prov, _ := cloud["type"].(string)
	region, _ := cloud["region"].(string)
	allowed := FreeTierRegions[prov]
	if prov != "" && region != "" && !containsKey(allowed, region) {
		c.add(join(cp, "cloudProvider", "region"),
			fmt.Sprintf("free-tier clusters on %s are available in: %s", prov, strings.Join(allowed, ", ")))
	}
	buckets := objList(cl["buckets"])
	c.dupesAll(buckets, "name", join(cp, "buckets"), "bucket", nil)
	for bi, b := range buckets {
		fields := make([]string, 0, len(b))
		for f := range b {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, f := range fields {
			if !freeTierBucketFields[f] {
				c.add(join(cp, "buckets", bi, f), "not available for free-tier buckets (only memoryAllocationInMb "+
					"and enableCrossClusterVersioning can be set; Capella chooses the rest)")
			}
		}
	}
}

func (c *crossChecker) checkProvisioned(e ProjectEntry, cl Obj, cp Path) {
	missing := false
	for _, f := range provisionedRequired {
		if _, ok := cl[f]; !ok {
			c.add(cp, fmt.Sprintf("missing required field '%s'", f))
			missing = true
		}
	}
	if missing {
		return
	}
	provider, _ := asObj(cl["cloudProvider"])["type"].(string)
	single := cl["configurationType"] == "singleNode"
	groups := objList(cl["serviceGroups"])

	c.checkServiceGroups(cl, cp, provider, single, groups)

	zones := asList(cl["zones"])
	if len(zones) > 0 && asObj(cl["availability"])["type"] == "multi" {
		c.add(join(cp, "zones"), "zones can only be set for availability: single")
	}

	// Buckets, scopes, collections.
	buckets := objList(cl["buckets"])
	c.dupesAll(buckets, "name", join(cp, "buckets"), "bucket", nil)
	defined := map[string]map[string]map[string]bool{}
	for bi, b := range buckets {
		bp := join(cp, "buckets", bi)
		for _, rule := range BucketTypeRules(b) {
			c.add(join(bp, rule.Field), rule.Message)
		}
		replicas, hasReplicas := asInt(b["replicas"])
		switch {
		case single && hasReplicas && replicas != 0:
			c.add(join(bp, "replicas"), "single-node clusters only support replicas: 0 (there is no second node to hold a copy)")
		case !single && hasReplicas && replicas == 0:
			c.add(join(bp, "replicas"), "replicas: 0 is only for single-node clusters; use 1, 2 or 3")
		}
		scopes := objList(b["scopes"])
		c.dupesAll(scopes, "name", join(bp, "scopes"), "scope", nil)
		bname := fmt.Sprint(b["name"])
		defined[bname] = map[string]map[string]bool{}
		for si, s := range scopes {
			sp := join(bp, "scopes", si)
			if msg := KeyspaceNameIssue("scope", s["name"]); msg != "" {
				c.add(join(sp, "name"), msg)
			}
			colls := objList(s["collections"])
			c.dupesAll(colls, "name", join(sp, "collections"), "collection", nil)
			names := map[string]bool{}
			for ci, coll := range colls {
				if msg := KeyspaceNameIssue("collection", coll["name"]); msg != "" {
					c.add(join(sp, "collections", ci, "name"), msg)
				}
				names[fmt.Sprint(coll["name"])] = true
			}
			defined[bname][fmt.Sprint(s["name"])] = names
		}
	}

	c.checkCredentials(e, cl, cp, defined)
}

func (c *crossChecker) checkServiceGroups(cl Obj, cp Path, provider string, single bool, groups []Obj) {
	hasData := false
	for _, g := range groups {
		if containsKey(stringList(g["services"]), "data") {
			hasData = true
		}
	}
	if len(groups) > 0 && !hasData {
		c.add(join(cp, "serviceGroups"), "at least one service group must run the data service", "scaling")
	}
	seenKeys := map[string]bool{}
	for _, g := range groups {
		key := GroupKey(g)
		if seenKeys[key] {
			c.add(join(cp, "serviceGroups"), fmt.Sprintf("two groups run %s; each group needs a different set of services", key), "scaling")
		}
		seenKeys[key] = true
	}
	var total int64
	for gi, g := range groups {
		gp := join(cp, "serviceGroups", gi)
		node := asObj(g["node"])
		disk := asObj(node["disk"])
		if n, ok := asInt(g["numOfNodes"]); ok {
			total += n
			switch {
			case single:
				if n != 1 {
					c.add(join(gp, "numOfNodes"), "singleNode clusters have exactly 1 node; create a multiNode cluster to scale out", "scaling")
				}
			case gi == 0 && (n < 3 || n > 27):
				c.add(join(gp, "numOfNodes"), "must be between 3 and 27", "scaling")
			case gi > 0 && (n < 2 || n > 24):
				c.add(join(gp, "numOfNodes"), "additional service groups allow 2–24 nodes", "scaling")
			}
		}
		diskType, _ := disk["type"].(string)
		if types, known := diskTypes[provider]; known && diskType != "" && !containsKey(types, diskType) {
			c.add(join(gp, "node", "disk", "type"),
				fmt.Sprintf("disk type %s is not valid for %s (use: %s)", diskType, provider, strings.Join(types, ", ")))
		}
		if (provider == "aws" || provider == "gcp") && diskType != "" && disk["storage"] == nil {
			c.add(join(gp, "node", "disk", "storage"), fmt.Sprintf("storage is required for %s disks", provider))
		}
		if provider == "aws" && diskType != "" && disk["iops"] == nil {
			c.add(join(gp, "node", "disk", "iops"), "iops is required for aws disks")
		}
		if (provider == "aws" || provider == "gcp") && disk["autoExpansion"] != nil {
			c.add(join(gp, "node", "disk", "autoExpansion"), "autoExpansion is for azure disks only")
		}
		c.checkDiskRange(provider, disk, join(gp, "node", "disk"))
		if single {
			for _, s := range stringList(g["services"]) {
				if !singleNodeServices[s] {
					c.add(join(gp, "services"), "singleNode clusters run only data, index, query and search")
					break
				}
			}
		}
	}
	if !single && total > 27 {
		c.add(join(cp, "serviceGroups"), fmt.Sprintf("a cluster can have at most 27 nodes (declared %d)", total), "scaling")
	}
	if single {
		if len(groups) > 1 {
			c.add(join(cp, "serviceGroups"), "singleNode clusters have exactly one service group", "scaling")
		}
		if asObj(cl["availability"])["type"] == "multi" {
			c.add(join(cp, "availability", "type"), "singleNode clusters must use availability: single")
		}
	}
}

// GroupKey is the identity of a service group: its sorted services joined by ",".
func GroupKey(group Obj) string {
	services := stringList(group["services"])
	sort.Strings(services)
	return strings.Join(services, ",")
}

func stringList(v any) []string {
	var out []string
	for _, item := range asList(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// checkDiskRange checks the sizes and IOPS a disk type accepts, from the embedded disks.yaml.
func (c *crossChecker) checkDiskRange(provider string, disk Obj, dp Path) {
	diskType, _ := disk["type"].(string)
	facts, ok := presets.DiskTypes()[provider][diskType]
	if diskType == "" || !ok || facts.FixedSize != 0 {
		return
	}
	size, hasSize := asInt(disk["storage"])
	iops, hasIOPS := asInt(disk["iops"])
	if hasSize {
		switch {
		case len(facts.Sizes) > 0 && !containsInt(facts.Sizes, int(size)):
			shown := facts.Sizes
			head := shown
			if len(head) > 5 {
				head = head[:5]
			}
			parts := make([]string, len(head))
			for i, s := range head {
				parts[i] = fmt.Sprint(s)
			}
			c.add(join(dp, "storage"), fmt.Sprintf("%s disks come in %s, … %d GB (got %d)%s", diskType,
				strings.Join(parts, ", "), shown[len(shown)-1], size, diskHint), "scaling")
		case len(facts.Size) > 1 && size > int64(facts.Size[1]):
			c.add(join(dp, "storage"), fmt.Sprintf("%s disks are at most %d GB (got %d)%s", diskType, facts.Size[1], size, diskHint), "scaling")
		}
	}
	var sizePtr *int
	if hasSize {
		s := int(size)
		sizePtr = &s
	}
	if lo, hi, ok := iopsRange(facts, sizePtr); ok && hasIOPS && (iops < int64(lo) || iops > int64(hi)) {
		at := ""
		if len(facts.IOPSCap) > 0 && hasSize {
			at = fmt.Sprintf(" at %d GB", size)
		}
		c.add(join(dp, "iops"), fmt.Sprintf("%s disks take %d–%d IOPS%s (got %d)%s", diskType, lo, hi, at, iops, diskHint), "scaling")
	}
}

func containsInt(list []int, n int) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

// iopsRange returns the (min, max) IOPS a disk of this type and size takes.
func iopsRange(facts presets.DiskType, size *int) (lo, hi int, ok bool) {
	if len(facts.IOPS) < 2 {
		return 0, 0, false
	}
	lo, hi = facts.IOPS[0], facts.IOPS[1]
	if size != nil {
		capIOPS := 0
		for _, step := range facts.IOPSCap {
			if len(step) == 2 && *size >= step[0] {
				capIOPS = step[1]
			}
		}
		if capIOPS != 0 && capIOPS < hi {
			hi = capIOPS
		}
	}
	return lo, hi, true
}

// recommendedIOPS returns Capella's recommended IOPS for a disk when the disk's IOPS are below
// it (a warning: existing disks may have less); 0 otherwise.
func recommendedIOPS(provider string, disk Obj) int {
	size, hasSize := asInt(disk["storage"])
	iops, hasIOPS := asInt(disk["iops"])
	diskType, _ := disk["type"].(string)
	if !hasSize || !hasIOPS || diskType == "" {
		return 0
	}
	facts, ok := presets.DiskTypes()[provider][diskType]
	if !ok || len(facts.Defaults) == 0 {
		return 0
	}
	recommended := facts.DefaultIOPS(int(size))
	if recommended == 0 && len(facts.Defaults[0]) == 2 {
		recommended = facts.Defaults[0][1]
	}
	// Capella allows 3000 for 100-199 GB disks (IOPS Defaults, note 1).
	if recommended == 0 || iops >= int64(recommended) || (size >= 100 && size < 200 && iops >= 3000) {
		return 0
	}
	return recommended
}

func (c *crossChecker) checkCredentials(e ProjectEntry, cl Obj, cp Path, defined map[string]map[string]map[string]bool) {
	creds := objList(cl["databaseCredentials"])
	c.dupesAll(creds, "name", join(cp, "databaseCredentials"), "credential", nil)
	clusterName := fmt.Sprint(cl["name"])
	for ui, u := range creds {
		up := join(cp, "databaseCredentials", ui)
		if pw, ok := u["password"].(string); ok && pw != "" && !secrets.MeetsRules(pw) {
			// The value is never shown.
			c.add(join(up, "password"),
				fmt.Sprintf("password for %v does not meet Capella rules (%s)", u["name"], secrets.Rules), "credentials")
		}
		for ai, a := range objList(u["access"]) {
			for rbi, rb := range objList(asObj(a["resources"])["buckets"]) {
				rp := join(up, "access", ai, "resources", "buckets", rbi)
				if ext, _ := rb["external"].(bool); ext {
					continue
				}
				bname := fmt.Sprint(rb["name"])
				if bname == "*" { // Capella's wildcard
					continue
				}
				scopes, ok := defined[bname]
				if !ok {
					c.add(join(rp, "name"),
						fmt.Sprintf("the credential gives access to bucket '%s', which capella.yaml does not list for cluster '%s'", bname, clusterName),
						"credentials",
						fmt.Sprintf("'cbctl capella sync --project %s' adds the bucket if it exists in Capella, or "+
							"'cbctl capella bucket add %s %s --project %s' adds it", e.Name(), clusterName, bname, e.Name()))
					continue
				}
				for rsi, rs := range objList(rb["scopes"]) {
					sname := fmt.Sprint(rs["name"])
					if sname == "*" {
						continue
					}
					if _, known := scopes[sname]; !known && sname != "_default" {
						c.add(join(rp, "scopes", rsi, "name"), fmt.Sprintf("scope '%s' is not defined in bucket '%s'", sname, bname), "credentials")
						continue
					}
					for rci, cn := range asList(rs["collections"]) {
						cname := fmt.Sprint(cn)
						if sname != "_default" && cname != "*" && !scopes[sname][cname] {
							c.add(join(rp, "scopes", rsi, "collections", rci),
								fmt.Sprintf("collection '%s' is not defined in %s.%s", cname, bname, sname), "credentials")
						}
					}
				}
			}
		}
	}
}
