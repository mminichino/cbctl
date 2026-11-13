package capellaapi

import (
	"regexp"
	"strings"
)

// roleRule maps an endpoint pattern to the Capella roles that may call it.
type roleRule struct {
	method string // upper-case verb or "*" for any
	re     *regexp.Regexp
	roles  string
}

// rolesTable is ported verbatim from capellactl's _ROLES (api/endpoints.py); first match wins.
var rolesTable = []roleRule{
	{"POST", regexp.MustCompile(`^/v4/organizations/[^/]+/projects$`), "Organization Owner or Project Creator"},
	{"PUT", regexp.MustCompile(`^/v4/organizations/[^/]+/projects/[^/]+$`), "Organization Owner or Project Owner"},
	{"DELETE", regexp.MustCompile(`^/v4/organizations/[^/]+/projects/[^/]+$`), "Organization Owner or Project Owner"},
	{"GET", regexp.MustCompile(`^/v4/organizations(/[^/]+)?(/projects)?$`), "any organization role (Organization Member or above)"},
	// Backups (spec): bucket backup schedules and snapshot schedule options also allow Project Manager; the rest not.
	{"*", regexp.MustCompile(`/(buckets/[^/]+/backup/schedules|cloudsnapshotbackupschedule/options)$`),
		"Organization Owner, Project Owner or Project Manager"},
	{"*", regexp.MustCompile(`/(cloudsnapshotbackups?|cloudsnapshotbackupschedule|backups?)(/|$)`), "Organization Owner or Project Owner"},
	{"*", regexp.MustCompile(`/clusters`), "Organization Owner, Project Owner or Project Manager"},
}

// RequiredRoles returns the Capella roles needed for the endpoint, or "" when unknown.
func RequiredRoles(method, path string) string {
	method = strings.ToUpper(method)
	for _, r := range rolesTable {
		if (r.method == "*" || r.method == method) && r.re.MatchString(path) {
			return r.roles
		}
	}
	return ""
}
