// Package models contains shared data structures for cluster nodes.
package models

// ClusterNodeConfig describes a Couchbase Server node for bootstrap.
type ClusterNodeConfig struct {
	IP               string
	RAMGiB           int
	Services         []string
	AlternateAddress string
	AlternatePorts   map[string]int
}

// CapellaNodeConfig describes Capella service-group sizing.
type CapellaNodeConfig struct {
	CPU      int
	RAM      int
	Services []string
}

// NodeSpec is a parsed CLI node specification.
type NodeSpec struct {
	Host             string
	Services         []string
	RAMGiB           int
	AlternateAddress string
	AlternatePorts   map[string]int
}
