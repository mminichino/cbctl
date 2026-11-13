package capellaapi

// Object is a JSON object passed through verbatim for payloads whose shape is owned by the
// caller (schedules, snapshots, bucket backups, free-tier clusters).
type Object = map[string]any

// Audit is Capella audit metadata.
type Audit struct {
	CreatedBy  string `json:"createdBy,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
	ModifiedBy string `json:"modifiedBy,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	Version    int    `json:"version,omitempty"`
}

// OrganizationPreferences is organization session preferences.
type OrganizationPreferences struct {
	SessionDuration int `json:"sessionDuration"`
}

// Organization is a Capella organization.
type Organization struct {
	ID          string                   `json:"id,omitempty"`
	Name        string                   `json:"name,omitempty"`
	Description string                   `json:"description,omitempty"`
	Preferences *OrganizationPreferences `json:"preferences,omitempty"`
	Audit       *Audit                   `json:"audit,omitempty"`
}

// Project is a Capella project.
type Project struct {
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`
	Audit       *Audit `json:"audit,omitempty"`
}

// CreateProjectRequest is the POST /projects body.
type CreateProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// IDResponse is the body returned by create calls.
type IDResponse struct {
	ID string `json:"id"`
}

// CloudProvider is the cluster cloud provider settings.
type CloudProvider struct {
	Type   string `json:"type,omitempty"`
	Region string `json:"region,omitempty"`
	CIDR   string `json:"cidr,omitempty"`
}

// CouchbaseServer is the server version setting.
type CouchbaseServer struct {
	Version string `json:"version,omitempty"`
}

// Availability is the availability topology setting.
type Availability struct {
	Type string `json:"type,omitempty"`
}

// Support is the support plan setting.
type Support struct {
	Plan     string `json:"plan,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// Compute is node compute sizing.
type Compute struct {
	CPU int `json:"cpu"`
	RAM int `json:"ram"`
}

// Disk is node disk sizing.
type Disk struct {
	Type          string `json:"type,omitempty"`
	Storage       *int   `json:"storage,omitempty"`
	IOPS          *int   `json:"iops,omitempty"`
	AutoExpansion *bool  `json:"autoExpansion,omitempty"`
}

// NodeConfig is compute plus disk for a service group.
type NodeConfig struct {
	Compute *Compute `json:"compute,omitempty"`
	Disk    *Disk    `json:"disk,omitempty"`
}

// ServiceGroupRequest is a service group in a create-cluster request.
type ServiceGroupRequest struct {
	NumOfNodes int        `json:"numOfNodes"`
	Services   []string   `json:"services"`
	Node       NodeConfig `json:"node"`
}

// ServiceGroup is a service group on an existing cluster.
type ServiceGroup struct {
	NumOfNodes int         `json:"numOfNodes"`
	Services   []string    `json:"services,omitempty"`
	Node       *NodeConfig `json:"node,omitempty"`
}

// CreateClusterRequest is the POST /clusters body.
type CreateClusterRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ConfigurationType is "singleNode" for one-node clusters; empty lets Capella decide.
	ConfigurationType string                `json:"configurationType,omitempty"`
	CloudProvider     CloudProvider         `json:"cloudProvider"`
	CouchbaseServer   *CouchbaseServer      `json:"couchbaseServer,omitempty"`
	ServiceGroups     []ServiceGroupRequest `json:"serviceGroups"`
	Availability      Availability          `json:"availability"`
	Support           Support               `json:"support"`
}

// UpdateClusterRequest is the PUT /clusters/{id} body. Capella replaces the whole resource, so
// callers send the current name, description, support and service groups with their changes.
type UpdateClusterRequest struct {
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	Support       *Support       `json:"support,omitempty"`
	ServiceGroups []ServiceGroup `json:"serviceGroups,omitempty"`
}

// Cluster is a Capella cluster.
type Cluster struct {
	ID                         string           `json:"id,omitempty"`
	AppServiceID               string           `json:"appServiceId,omitempty"`
	Name                       string           `json:"name,omitempty"`
	Description                string           `json:"description,omitempty"`
	ConfigurationType          string           `json:"configurationType,omitempty"`
	ConnectionString           string           `json:"connectionString,omitempty"`
	CloudProvider              *CloudProvider   `json:"cloudProvider,omitempty"`
	CouchbaseServer            *CouchbaseServer `json:"couchbaseServer,omitempty"`
	ServiceGroups              []ServiceGroup   `json:"serviceGroups,omitempty"`
	Availability               *Availability    `json:"availability,omitempty"`
	Support                    *Support         `json:"support,omitempty"`
	CurrentState               string           `json:"currentState,omitempty"`
	Audit                      *Audit           `json:"audit,omitempty"`
	CMEKID                     string           `json:"cmekId,omitempty"`
	EnablePrivateDNSResolution *bool            `json:"enablePrivateDNSResolution,omitempty"`
	DeletionProtection         *bool            `json:"deletionProtection,omitempty"`

	// ETag is the resource version from the response header; GetCluster sets it.
	ETag string `json:"-"`
}

// ClusterStats is the cluster memory and replica capacity summary.
type ClusterStats struct {
	FreeMemoryInMb  int `json:"freeMemoryInMb"`
	TotalMemoryInMb int `json:"totalMemoryInMb"`
	MaxReplicas     int `json:"maxReplicas"`
}

// CreateBucketRequest is the POST /buckets body. The pointer and omitempty fields are only sent
// when the caller sets them, so Capella applies its own defaults otherwise.
type CreateBucketRequest struct {
	Name                     string `json:"name"`
	Type                     string `json:"type"`
	StorageBackend           string `json:"storageBackend,omitempty"`
	VBuckets                 *int   `json:"vbuckets,omitempty"`
	MemoryAllocationInMb     int    `json:"memoryAllocationInMb"`
	BucketConflictResolution string `json:"bucketConflictResolution"`
	DurabilityLevel          string `json:"durabilityLevel"`
	Replicas                 int    `json:"replicas"`
	Flush                    bool   `json:"flush"`
	FlushEnabled             *bool  `json:"flushEnabled,omitempty"`
	TimeToLiveInSeconds      int    `json:"timeToLiveInSeconds"`
	EvictionPolicy           string `json:"evictionPolicy,omitempty"`
	Priority                 *int   `json:"priority,omitempty"`
}

// BucketStats is bucket usage statistics.
type BucketStats struct {
	ItemCount       int `json:"itemCount"`
	OpsPerSecond    int `json:"opsPerSecond"`
	DiskUsedInMiB   int `json:"diskUsedInMib"`
	MemoryUsedInMiB int `json:"memoryUsedInMib"`
}

// Bucket is a Capella bucket.
type Bucket struct {
	ID                       string       `json:"id,omitempty"`
	Name                     string       `json:"name,omitempty"`
	Type                     string       `json:"type,omitempty"`
	StorageBackend           string       `json:"storageBackend,omitempty"`
	VBuckets                 int          `json:"vbuckets,omitempty"`
	MemoryAllocationInMb     int          `json:"memoryAllocationInMb,omitempty"`
	BucketConflictResolution string       `json:"bucketConflictResolution,omitempty"`
	DurabilityLevel          string       `json:"durabilityLevel,omitempty"`
	Replicas                 int          `json:"replicas,omitempty"`
	Flush                    *bool        `json:"flush,omitempty"`
	FlushEnabled             *bool        `json:"flushEnabled,omitempty"`
	TimeToLiveInSeconds      int          `json:"timeToLiveInSeconds,omitempty"`
	EvictionPolicy           string       `json:"evictionPolicy,omitempty"`
	Stats                    *BucketStats `json:"stats,omitempty"`
	Priority                 int          `json:"priority,omitempty"`

	EnableCrossClusterVersioning *bool `json:"enableCrossClusterVersioning,omitempty"`
}

// ResourceScope is credential access to one scope.
type ResourceScope struct {
	Name        string   `json:"name"`
	Collections []string `json:"collections,omitempty"`
}

// ResourceBucket is credential access to one bucket.
type ResourceBucket struct {
	Name   string          `json:"name"`
	Scopes []ResourceScope `json:"scopes,omitempty"`
}

// Resources is the resource tree a credential privilege applies to.
type Resources struct {
	Buckets []ResourceBucket `json:"buckets,omitempty"`
}

// AccessEntry is a credential access grant.
type AccessEntry struct {
	Privileges []string  `json:"privileges"`
	Resources  Resources `json:"resources"`
}

// Credential is a database credential.
type Credential struct {
	ID     string        `json:"id,omitempty"`
	Name   string        `json:"name,omitempty"`
	Access []AccessEntry `json:"access,omitempty"`
	Audit  *Audit        `json:"audit,omitempty"`
}

// CreateCredentialRequest is the POST /users body.
type CreateCredentialRequest struct {
	Name     string        `json:"name"`
	Password string        `json:"password"`
	Access   []AccessEntry `json:"access"`
}

// CreateCredentialResponse is the response to a credential create. Password is only set when
// Capella generated it.
type CreateCredentialResponse struct {
	ID       string `json:"id,omitempty"`
	Password string `json:"password,omitempty"`
}

// UpdateCredentialRequest is the PUT credential body.
type UpdateCredentialRequest struct {
	Password string        `json:"password,omitempty"`
	Access   []AccessEntry `json:"access,omitempty"`
}

// AllowedCIDR is an allowed CIDR entry.
type AllowedCIDR struct {
	ID        string `json:"id,omitempty"`
	CIDR      string `json:"cidr,omitempty"`
	Comment   string `json:"comment,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Status    string `json:"status,omitempty"`
	Type      string `json:"type,omitempty"`
	Audit     *Audit `json:"audit,omitempty"`
}

// CreateAllowedCIDRRequest is the POST /allowedcidrs body.
type CreateAllowedCIDRRequest struct {
	CIDR      string `json:"cidr"`
	Comment   string `json:"comment,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}
