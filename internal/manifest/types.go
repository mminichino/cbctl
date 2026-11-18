package manifest

import (
	"encoding/json"
	"time"

	"github.com/mminichino/cbctl/internal/yamlio"
)

// Envelope is the part every manifest document shares. Spec keeps the raw JSON of the spec
// section; the typed form is in Document.Server or Document.Capella.
type Envelope struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   Metadata        `json:"metadata"`
	Spec       json.RawMessage `json:"spec,omitempty"`
}

// Metadata names the cluster.
type Metadata struct {
	// Name is the cluster name: kebab case, 1-63 characters.
	Name string `json:"name"`
	// Labels are informational only; they are not sent anywhere.
	Labels map[string]string `json:"labels,omitempty"`
}

// ServerSpec is the spec of a CouchbaseServer document.
type ServerSpec struct {
	Credentials  ServerCredentials `json:"credentials"`
	TLS          *TLS              `json:"tls,omitempty"`
	NodeDefaults NodeDefaults      `json:"nodeDefaults"`
	Nodes        []ServerNode      `json:"nodes"`
	// Rebalance is nil when absent, which means true.
	Rebalance *bool          `json:"rebalance,omitempty"`
	Buckets   []ServerBucket `json:"buckets,omitempty"`
}

// ServerCredentials is the full-admin user set when the cluster is initialized.
type ServerCredentials struct {
	// Username defaults to Administrator.
	Username string    `json:"username,omitempty"`
	Password SecretRef `json:"password"`
}

// TLS selects TLS for the management and SDK connections.
type TLS struct {
	Enabled bool `json:"enabled"`
	// CAFile is an optional CA bundle; empty means the system trust store. Relative paths
	// resolve from the manifest's directory.
	CAFile string `json:"caFile,omitempty"`
}

// NodeDefaults apply to every node that does not set its own value.
type NodeDefaults struct {
	// Services applies to nodes that set neither services nor arbiter.
	Services ServiceSet `json:"services,omitempty"`
	DataPath string     `json:"dataPath,omitempty"`
	RAMGiB   *int       `json:"ramGiB,omitempty"`
}

// ServerNode is one machine. After Load, the zero values of Services, DataPath and RAMGiB have
// been filled from NodeDefaults (see Effective).
type ServerNode struct {
	IPAddress         string     `json:"ipAddress"`
	ExternalIPAddress string     `json:"externalIpAddress,omitempty"`
	ServerGroup       string     `json:"serverGroup,omitempty"`
	DataPath          string     `json:"dataPath,omitempty"`
	Services          ServiceSet `json:"services,omitempty"`
	RAMGiB            *int       `json:"ramGiB,omitempty"`
	Arbiter           bool       `json:"arbiter,omitempty"`
	Primary           bool       `json:"primary,omitempty"`
}

// ServerBucket is a bucket with its scopes, collections and imports.
type ServerBucket struct {
	Name     string        `json:"name"`
	QuotaMiB *int          `json:"quotaMiB,omitempty"`
	Replicas *int          `json:"replicas,omitempty"`
	Scopes   []ServerScope `json:"scopes,omitempty"`
	Import   []Import      `json:"import,omitempty"`
}

// ServerScope is a scope and the names of its collections.
type ServerScope struct {
	Name        string   `json:"name"`
	Collections []string `json:"collections,omitempty"`
}

// Import loads documents into one collection, from a JSON Lines file or from inline data.
type Import struct {
	// Scope and Collection default to _default.
	Scope      string `json:"scope,omitempty"`
	Collection string `json:"collection,omitempty"`
	File       string `json:"file,omitempty"`
	// Data holds one JSON object per entry, converted from YAML.
	Data     []json.RawMessage `json:"data,omitempty"`
	KeyField string            `json:"keyField,omitempty"`
	// Credential names a spec.credentials entry (CouchbaseCapella only).
	Credential string `json:"credential,omitempty"`
}

// CapellaSpec is the spec of a CouchbaseCapella document.
type CapellaSpec struct {
	OrganizationID string       `json:"organizationId,omitempty"`
	Project        string       `json:"project"`
	Provider       string       `json:"provider,omitempty"`
	Region         string       `json:"region,omitempty"`
	CIDR           string       `json:"cidr,omitempty"`
	Availability   string       `json:"availability,omitempty"`
	ServerVersion  string       `json:"serverVersion,omitempty"`
	Description    string       `json:"description,omitempty"`
	Size           string       `json:"size,omitempty"`
	SupportPlan    *SupportPlan `json:"supportPlan,omitempty"`
	// DeletionProtection is nil when absent: an existing cluster keeps its setting (C18).
	DeletionProtection *bool           `json:"deletionProtection,omitempty"`
	ServiceGroups      []ServiceGroup  `json:"serviceGroups,omitempty"`
	AllowedCIDRs       []AllowedCIDR   `json:"allowedCidrs,omitempty"`
	Buckets            []CapellaBucket `json:"buckets,omitempty"`
	Credentials        []Credential    `json:"credentials,omitempty"`
	Wait               *Wait           `json:"wait,omitempty"`
}

// SupportPlan is the Capella support plan and time zone.
type SupportPlan struct {
	Plan     string `json:"plan,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// ServiceGroup is a set of nodes that run the same services.
type ServiceGroup struct {
	Services ServiceSet `json:"services"`
	Nodes    int        `json:"nodes"`
	Compute  Compute    `json:"compute"`
	Disk     *Disk      `json:"disk,omitempty"`
}

// Compute is the node size: vCPUs and RAM in GB.
type Compute struct {
	CPU   int `json:"cpu"`
	RAMGb int `json:"ramGb"`
}

// Disk is the per-node disk. Zero or nil fields take the provider default.
type Disk struct {
	Type          string `json:"type,omitempty"`
	SizeGb        *int   `json:"sizeGb,omitempty"`
	IOPS          *int   `json:"iops,omitempty"`
	AutoExpansion *bool  `json:"autoExpansion,omitempty"`
}

// AllowedCIDR is one allow-list entry.
type AllowedCIDR struct {
	CIDR    string `json:"cidr"`
	Comment string `json:"comment,omitempty"`
	// ExpiresAt is an RFC 3339 time, empty when the entry does not expire. It is a string so a
	// malformed value is reported with its line; ExpiryTime parses it.
	ExpiresAt  string `json:"expiresAt,omitempty"`
	AllowAnyIP bool   `json:"allowAnyIp,omitempty"`
}

// ExpiryTime returns ExpiresAt as a time; ok is false when it is empty or not RFC 3339.
func (a AllowedCIDR) ExpiryTime() (t time.Time, ok bool) {
	if a.ExpiresAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	return t, err == nil
}

// CapellaBucket is a Capella bucket. Pointer fields are nil when absent so apply can tell a
// default from a value (and change only what the manifest sets).
type CapellaBucket struct {
	Name           string         `json:"name"`
	Type           string         `json:"type,omitempty"`
	StorageBackend string         `json:"storageBackend,omitempty"`
	Durability     string         `json:"durability,omitempty"`
	MemoryMB       *int           `json:"memoryMb,omitempty"`
	Replicas       *int           `json:"replicas,omitempty"`
	TTL            *int           `json:"ttl,omitempty"`
	Flush          *bool          `json:"flush,omitempty"`
	Scopes         []CapellaScope `json:"scopes,omitempty"`
	Import         []Import       `json:"import,omitempty"`
}

// CapellaScope is a scope with its collections.
type CapellaScope struct {
	Name        string              `json:"name"`
	Collections []CapellaCollection `json:"collections,omitempty"`
}

// CapellaCollection is a collection and its optional maximum TTL in seconds.
type CapellaCollection struct {
	Name   string `json:"name"`
	MaxTTL *int   `json:"maxTtl,omitempty"`
}

// Credential is a database credential. A nil Password means Capella generates one.
type Credential struct {
	Name     string     `json:"name"`
	Password *SecretRef `json:"password,omitempty"`
	Access   Access     `json:"access"`
}

// Access is what a credential may do.
type Access struct {
	// Privileges is read, write or read+write.
	Privileges  string   `json:"privileges"`
	Bucket      string   `json:"bucket,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	Collections []string `json:"collections,omitempty"`
	AllBuckets  bool     `json:"allBuckets,omitempty"`
}

// Wait controls waiting for Capella operations.
type Wait struct {
	// Timeout is a duration such as 45m (bare numbers are seconds); the default is 60m, the maximum 24h.
	Timeout Duration `json:"timeout,omitempty"`
	// Enabled is nil when absent, which means true.
	Enabled *bool `json:"enabled,omitempty"`
}

// ServiceSet is a set of service names. Load normalizes it: lower case, aliases mapped,
// duplicates removed, sorted.
type ServiceSet []string

// Document is one validated manifest document.
type Document struct {
	// Index is the 0-based position among the non-empty documents of the file.
	Index    int
	Envelope Envelope
	// Server is set if and only if Envelope.Kind is KindServer.
	Server *ServerSpec
	// Capella is set if and only if Envelope.Kind is KindCapella.
	Capella *CapellaSpec
	// Lines maps JSON pointers of the document to positions in the file.
	Lines yamlio.LineIndex

	source string
}
