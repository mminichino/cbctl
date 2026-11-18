package serverapply

import (
	"fmt"

	"github.com/mminichino/cbctl/internal/manifest"
)

// plan lists the steps a real apply would run, without contacting any node: whether they would
// change anything depends on the cluster, which a dry run does not read.
func (a *applier) plan() {
	add := func(name, target, msg string, args ...any) {
		a.rep.Add(manifest.Step{Name: name, Target: target, Status: manifest.StepPlanned})
		a.pr.Info("Would %s", fmt.Sprintf(msg, args...))
	}
	add("bootstrap", a.primary.IPAddress, "initialize the cluster on %s if it is not configured", a.primary.IPAddress)
	for _, n := range a.spec.Nodes {
		if n.IPAddress != a.primary.IPAddress {
			add("join", n.IPAddress, "join node %s to the cluster at %s if it is not a member", n.IPAddress, a.primary.IPAddress)
		}
	}
	if a.spec.DoRebalance() && len(a.spec.Nodes) > 1 {
		add("rebalance", a.primary.IPAddress, "rebalance if nodes are waiting to be rebalanced in")
	}
	for _, b := range a.spec.Buckets {
		add("bucket", b.Name, "create bucket %q if it does not exist", b.Name)
		for _, s := range b.Scopes {
			if s.Name != defaultName {
				add("scope", b.Name+"."+s.Name, "create scope %q if it does not exist", b.Name+"."+s.Name)
			}
			for _, c := range s.Collections {
				if c != defaultName {
					target := b.Name + "." + s.Name + "." + c
					add("collection", target, "create collection %q if it does not exist", target)
				}
			}
		}
	}
	for _, b := range a.spec.Buckets {
		for _, imp := range b.Import {
			scope, coll := imp.Scope, imp.Collection
			if scope == "" {
				scope = defaultName
			}
			if coll == "" {
				coll = defaultName
			}
			target := b.Name + "." + scope + "." + coll
			add("import", target, "import into %s if it is empty", target)
		}
	}
	a.rep.Summary = a.summary()
}
