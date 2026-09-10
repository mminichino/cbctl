package server

import (
	"github.com/mminichino/cbctl/internal/rest"
)

// BootstrapPrimary initializes a single-node cluster via provisioner options.
func BootstrapPrimary(opts rest.ProvisionOptions) (bool, error) {
	return rest.NewClient().BootstrapPrimary(opts)
}

// JoinNode adds a node to an existing rally cluster without rebalancing.
func JoinNode(opts rest.ProvisionOptions) (bool, error) {
	return rest.NewClient().JoinNode(opts)
}

// RebalanceCluster rebalances all known nodes on the rally cluster.
func RebalanceCluster(opts rest.ProvisionOptions) error {
	return rest.NewClient().RebalanceCluster(opts)
}
