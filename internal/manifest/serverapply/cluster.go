package serverapply

import (
	"context"
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/manifest"
	"github.com/mminichino/cbctl/internal/rest"
)

// provisionOptions builds the REST options of node. The services are the canonical manifest
// names mapped to the names the REST layer uses; an arbiter runs none.
func (a *applier) provisionOptions(n manifest.ServerNode) rest.ProvisionOptions {
	opts := rest.ProvisionOptions{
		IPAddress:         n.IPAddress,
		ExternalIPAddress: n.ExternalIPAddress,
		Arbiter:           n.Arbiter,
		ServerGroup:       n.ServerGroup,
		DataPath:          n.DataPath,
		ClusterName:       a.name,
		Username:          a.spec.Credentials.Username,
		Password:          a.pw,
		SSL:               a.spec.TLS != nil && a.spec.TLS.Enabled,
	}
	if n.RAMGiB != nil {
		opts.RAMGiB = *n.RAMGiB
	}
	if !n.Arbiter {
		for _, s := range a.spec.EffectiveServices(n) {
			opts.Services = append(opts.Services, rest.NormalizeServerService(s))
		}
	}
	return opts
}

// bootstrap initializes the primary node (step 1).
func (a *applier) bootstrap(_ context.Context) error {
	ip := a.primary.IPAddress
	err := a.timed("bootstrap", ip, func() (manifest.Step, error) {
		if err := a.checkClusterIdentity(); err != nil {
			return manifest.Step{}, err
		}
		created, err := a.env.Provisioner.BootstrapPrimary(a.provisionOptions(a.primary))
		if err != nil {
			return manifest.Step{}, fmt.Errorf("initializing the primary node %s: %w", ip, err)
		}
		if !created {
			a.pr.Info("Cluster already configured")
			return manifest.Step{Status: manifest.StepUnchanged, Detail: "cluster already configured"}, nil
		}
		a.pr.OK("Cluster created on %s", ip)
		return manifest.Step{Status: manifest.StepDone}, nil
	})
	return err
}

// checkClusterIdentity refuses to reconcile a primary that already belongs to a differently
// named cluster. Bootstrap would otherwise change quotas and the server group of someone
// else's cluster. A cluster without a name, or a node that is not initialized, passes.
func (a *applier) checkClusterIdentity() error {
	name, initialized, err := a.env.Provisioner.ClusterName(a.endpoint(), a.spec.Credentials.Username, a.pw)
	if err != nil {
		return fmt.Errorf("reading the cluster name of %s: %w", a.primary.IPAddress, err)
	}
	if initialized && name != "" && name != a.name {
		return fmt.Errorf("node %s already belongs to another cluster (%q, the manifest is %q); nothing was changed",
			a.primary.IPAddress, name, a.name)
	}
	return nil
}

// joins adds every other node to the primary's cluster, in manifest order (step 2). Joins stay
// sequential: Couchbase Server does not take concurrent topology changes.
func (a *applier) joins(ctx context.Context) error {
	for _, n := range a.spec.Nodes {
		if n.IPAddress == a.primary.IPAddress {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		opts := a.provisionOptions(n)
		opts.RallyIPAddress = a.primary.IPAddress
		err := a.timed("join", n.IPAddress, func() (manifest.Step, error) {
			joined, err := a.env.Provisioner.JoinNode(opts)
			if err != nil {
				return manifest.Step{}, fmt.Errorf("joining node %s: %w", n.IPAddress, err)
			}
			switch {
			case !joined:
				a.pr.Info("Node %s already configured", n.IPAddress)
				return manifest.Step{Status: manifest.StepUnchanged, Detail: "node already configured"}, nil
			case n.Arbiter:
				a.pr.OK("Arbiter node %s added to cluster at %s", n.IPAddress, a.primary.IPAddress)
			default:
				a.pr.OK("Node %s added to cluster at %s", n.IPAddress, a.primary.IPAddress)
			}
			return manifest.Step{Status: manifest.StepDone}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// endpoint is the management endpoint of the primary.
func (a *applier) endpoint() rest.Endpoint {
	return rest.ForServer(a.primary.IPAddress, a.spec.TLS != nil && a.spec.TLS.Enabled)
}

// membership warns about cluster nodes the manifest does not list (step 3). Such nodes are never
// removed: the manifest adds nodes, it does not take them away.
func (a *applier) membership(_ context.Context) error {
	return a.timed("membership", a.primary.IPAddress, func() (manifest.Step, error) {
		nodes, err := a.env.Provisioner.ClusterNodes(a.endpoint(), a.spec.Credentials.Username, a.pw)
		if err != nil {
			return manifest.Step{}, fmt.Errorf("reading the cluster membership: %w", err)
		}
		listed := map[string]bool{}
		for _, n := range a.spec.Nodes {
			for _, addr := range []string{n.IPAddress, n.ExternalIPAddress} {
				if addr != "" {
					host, _ := rest.ParseHostPort(addr, 8091)
					listed[strings.ToLower(host)] = true
				}
			}
		}
		extra := 0
		for _, n := range nodes {
			if !listed[strings.ToLower(n.Host)] {
				a.warn("node %s is in the cluster but not in the manifest; left in place", n.Host)
				extra++
			}
		}
		step := manifest.Step{Status: manifest.StepUnchanged, Detail: plural(len(nodes), "node") + " in the cluster"}
		if extra > 0 {
			step.Detail += fmt.Sprintf(", %d not in the manifest", extra)
		}
		return step, nil
	})
}

// rebalance rebalances the added nodes in (step 4). It is skipped when the manifest says
// rebalance: false and when no node is waiting to be rebalanced in.
func (a *applier) rebalance(_ context.Context) error {
	return a.timed("rebalance", a.primary.IPAddress, func() (manifest.Step, error) {
		if !a.spec.DoRebalance() {
			a.pr.Info("Rebalance skipped (rebalance: false)")
			return manifest.Step{Status: manifest.StepSkipped, Detail: "rebalance: false"}, nil
		}
		pending, err := a.env.Provisioner.PendingNodes(a.endpoint(), a.spec.Credentials.Username, a.pw)
		if err != nil {
			return manifest.Step{}, fmt.Errorf("checking for nodes to rebalance in: %w", err)
		}
		if len(pending) == 0 {
			a.pr.Info("Rebalance not needed")
			return manifest.Step{Status: manifest.StepSkipped, Detail: "no node is waiting to be rebalanced in"}, nil
		}
		opts := a.provisionOptions(a.primary)
		opts.RallyIPAddress = a.primary.IPAddress
		if err := a.env.Provisioner.RebalanceCluster(opts); err != nil {
			return manifest.Step{}, fmt.Errorf("rebalancing %s: %w", strings.Join(pending, ", "), err)
		}
		a.pr.OK("Cluster rebalanced")
		return manifest.Step{Status: manifest.StepDone, Detail: "rebalanced in " + strings.Join(pending, ", ")}, nil
	})
}
