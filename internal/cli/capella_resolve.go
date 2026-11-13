package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// uuidPattern matches a Capella resource ID, which lets users pass an ID where a name is expected.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveProject finds a project by exact, case-sensitive name. A name that looks like an ID is
// matched against project IDs when no project has that name.
func (r *runtime) resolveProject(name string) (*capellaapi.Project, error) {
	projects, err := r.Client.ListProjects(r.Ctx)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	return pickProject(projects, name)
}

func pickProject(projects []capellaapi.Project, name string) (*capellaapi.Project, error) {
	var byName, byID []*capellaapi.Project
	for i := range projects {
		p := &projects[i]
		if p.Name == name {
			byName = append(byName, p)
		}
		if uuidPattern.MatchString(name) && p.ID == name {
			byID = append(byID, p)
		}
	}
	matches := byName
	if len(matches) == 0 {
		matches = byID // names win on a tie
	}
	switch len(matches) {
	case 0:
		return nil, Errorf(ExitConfigInvalid, "check the name with 'cbctl capella project list'", "project %s not found", name)
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, p := range matches {
		ids[i] = p.ID
	}
	return nil, Errorf(ExitConfigInvalid, "use the project ID", "several projects are named %s (%s)", name, strings.Join(ids, ", "))
}

// resolveCluster finds a cluster by exact name (or ID, see resolveProject). With projectName it
// looks only in that project; otherwise in every project. The returned cluster comes from a list
// call, so it has no ETag: callers that update it re-read it with GetCluster.
func (r *runtime) resolveCluster(name, projectName string) (*capellaapi.Project, *capellaapi.Cluster, error) {
	var projects []capellaapi.Project
	if projectName != "" {
		p, err := r.resolveProject(projectName)
		if err != nil {
			return nil, nil, err
		}
		projects = []capellaapi.Project{*p}
	} else {
		var err error
		if projects, err = r.Client.ListProjects(r.Ctx); err != nil {
			return nil, nil, fmt.Errorf("listing projects: %w", err)
		}
	}

	type match struct {
		project *capellaapi.Project
		cluster *capellaapi.Cluster
	}
	var byName, byID []match
	isID := uuidPattern.MatchString(name)
	for i := range projects {
		p := &projects[i]
		clusters, err := r.Client.ListClusters(r.Ctx, p.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
		}
		for j := range clusters {
			c := &clusters[j]
			if c.Name == name {
				byName = append(byName, match{p, c})
			}
			if isID && c.ID == name {
				byID = append(byID, match{p, c})
			}
		}
	}
	matches := byName
	if len(matches) == 0 {
		matches = byID // names win on a tie
	}
	switch len(matches) {
	case 0:
		return nil, nil, Errorf(ExitConfigInvalid, "check the name with 'cbctl capella cluster list'", "cluster %s not found", name)
	case 1:
		return matches[0].project, matches[0].cluster, nil
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.project.Name
	}
	return nil, nil, Errorf(ExitConfigInvalid, "add --project NAME", "several clusters are named %s (projects %s)", name, strings.Join(names, ", "))
}

// resolveBucket reads the bucket called name from cluster cl of project p. A 404 becomes a
// "bucket not found" error.
func (r *runtime) resolveBucket(p, cl, name string) (*capellaapi.Bucket, error) {
	b, _, err := r.Client.GetBucket(r.Ctx, p, cl, capellaapi.BucketID(name))
	if err != nil {
		if errors.Is(err, capellaapi.ErrNotFound) {
			return nil, Errorf(ExitConfigInvalid, "check the name with 'cbctl capella bucket list CLUSTER'", "bucket %s not found", name)
		}
		return nil, fmt.Errorf("reading bucket %s: %w", name, err)
	}
	return b, nil
}
