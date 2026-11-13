package cli

import (
	"errors"
	"fmt"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// Helpers shared by the resource commands (project, cidr, credential, bucket, scope, collection).

// clusterTarget is a cluster together with the project it lives in.
type clusterTarget struct {
	Project *capellaapi.Project
	Cluster *capellaapi.Cluster
}

// locateCluster resolves a cluster name or ID, optionally restricted to one project.
func (r *runtime) locateCluster(name, project string) (*clusterTarget, error) {
	p, c, err := r.resolveCluster(name, project)
	if err != nil {
		return nil, err
	}
	return &clusterTarget{Project: p, Cluster: c}, nil
}

// ids returns the project and cluster IDs.
func (t *clusterTarget) ids() (string, string) { return t.Project.ID, t.Cluster.ID }

// freeTier reports whether the cluster is a free-tier cluster (Capella reports support plan "free").
func (t *clusterTarget) freeTier() bool { return isFreeCluster(t.Cluster) }

// protected reports whether deletion protection is on for the cluster.
func (t *clusterTarget) protected() bool {
	return t.Cluster.DeletionProtection != nil && *t.Cluster.DeletionProtection
}

// refuseIfProtected fails with ExitBlocked when the cluster has deletion protection on.
func (t *clusterTarget) refuseIfProtected() error {
	if !t.protected() {
		return nil
	}
	return Errorf(ExitBlocked, fmt.Sprintf("run 'cbctl capella cluster protect %s --off' first", t.Cluster.Name),
		"cluster %s has deletion protection on", t.Cluster.Name)
}

// statusDoc is the small JSON document of commands that change something.
type statusDoc struct {
	Status string `json:"status"`
}

// confirmDelete asks the destructive-action question. When the user declines it reports that
// nothing was deleted and returns false.
func (r *runtime) confirmDelete(question string) (bool, error) {
	ok, err := confirm(r, question)
	if err != nil {
		return false, err
	}
	if !ok {
		r.report(statusDoc{Status: "cancelled"}, "cancelled; nothing was deleted")
	}
	return ok, nil
}

// isAmbiguous reports whether err means a write may or may not have been applied.
func isAmbiguous(err error) bool {
	var amb *capellaapi.AmbiguousWriteError
	return errors.As(err, &amb)
}

// notFoundOr turns a 404 into a "kind name not found" usage error (exit 3) and wraps other
// errors with what was being done.
func notFoundOr(err error, kind, name, action, doing string) error {
	if errors.Is(err, capellaapi.ErrNotFound) {
		return usageError(action, "%s %s not found", kind, name)
	}
	return fmt.Errorf("%s: %w", doing, err)
}

// isSystemCollection reports whether Capella manages the collection: everything in _system and
// _default._default.
func isSystemCollection(scope, name string) bool {
	return scope == "_system" || (scope == "_default" && name == "_default")
}
