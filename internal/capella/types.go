package capella

import "strconv"

// AvailabilityType is Capella availability topology.
type AvailabilityType string

const (
	AvailabilitySingleZone AvailabilityType = "single"
	AvailabilityMultiZone  AvailabilityType = "multi"
)

// CloudType is Capella cloud provider.
type CloudType string

const (
	CloudAWS   CloudType = "aws"
	CloudGCP   CloudType = "gcp"
	CloudAzure CloudType = "azure"
)

// State is Capella cluster lifecycle state.
type State string

const (
	StateHealthy    State = "healthy"
	StateDeploying  State = "deploying"
	StateDestroying State = "destroying"
	StateDestroyed  State = "destroyed"
	StateFailed     State = "deploymentFailed"
	StateUnknown    State = "unknown"
)

// SupportPlanType is Capella support plan.
type SupportPlanType string

const (
	SupportBasic      SupportPlanType = "basic"
	SupportDeveloper  SupportPlanType = "developer pro"
	SupportEnterprise SupportPlanType = "enterprise"
)

// TimeZoneType is Capella support timezone.
type TimeZoneType string

const (
	TimeZoneUSEast TimeZoneType = "ET"
	TimeZoneUSWest TimeZoneType = "PT"
	TimeZoneEurope TimeZoneType = "GMT"
	TimeZoneAsia   TimeZoneType = "IST"
)

// StateWaitOperation controls cluster wait comparisons.
type StateWaitOperation string

const (
	StateWaitEquals    StateWaitOperation = "equals"
	StateWaitNotEquals StateWaitOperation = "not_equals"
)

func (op StateWaitOperation) evaluate(value bool) bool {
	if op == StateWaitEquals {
		return value
	}
	return !value
}

// AuditData is Capella audit metadata.
type AuditData struct {
	CreatedBy  string `json:"createdBy,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
	ModifiedBy string `json:"modifiedBy,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	Version    int    `json:"version,omitempty"`
}

// OrganizationPreferences is org session preferences.
type OrganizationPreferences struct {
	SessionDuration int `json:"sessionDuration"`
}

// OrganizationData is a Capella organization.
type OrganizationData struct {
	ID          string                   `json:"id,omitempty"`
	Name        string                   `json:"name,omitempty"`
	Description string                   `json:"description,omitempty"`
	Preferences *OrganizationPreferences `json:"preferences,omitempty"`
	Audit       *AuditData               `json:"audit,omitempty"`
}

// ProjectData is a Capella project.
type ProjectData struct {
	ID          string     `json:"id,omitempty"`
	Description string     `json:"description,omitempty"`
	Name        string     `json:"name,omitempty"`
	Audit       *AuditData `json:"audit,omitempty"`
}

// CloudProviderData is cluster cloud provider settings.
type CloudProviderData struct {
	Type   string `json:"type,omitempty"`
	Region string `json:"region,omitempty"`
	CIDR   string `json:"cidr,omitempty"`
}

// CouchbaseServerData is server version settings.
type CouchbaseServerData struct {
	Version string `json:"version,omitempty"`
}

// AvailabilityData is availability settings.
type AvailabilityData struct {
	Type string `json:"type,omitempty"`
}

// SupportData is support plan settings.
type SupportData struct {
	Plan     string `json:"plan,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// ComputeData is node compute sizing.
type ComputeData struct {
	CPU int `json:"cpu"`
	RAM int `json:"ram"`
}

// DiskConfig is node disk sizing.
type DiskConfig struct {
	Type          string `json:"type,omitempty"`
	Storage       *int   `json:"storage,omitempty"`
	IOPS          *int   `json:"iops,omitempty"`
	AutoExpansion *bool  `json:"autoExpansion,omitempty"`
}

// DiskConfigAWS builds an AWS gp3 disk config for the given storage GiB.
func DiskConfigAWS(storage int) (DiskConfig, error) {
	matrix := []struct {
		threshold int
		iops      int
	}{
		{99, 3000},
		{199, 4370},
		{299, 5740},
		{399, 7110},
		{499, 8480},
		{599, 9850},
		{699, 11220},
		{799, 12590},
		{899, 13960},
		{999, 15330},
		{16384, 16000},
	}
	for _, row := range matrix {
		if row.threshold >= storage {
			s, i := storage, row.iops
			return DiskConfig{Type: "gp3", Storage: &s, IOPS: &i}, nil
		}
	}
	return DiskConfig{}, fmtInvalidStorage(storage)
}

// DiskConfigGCP builds a GCP pd-ssd disk config.
func DiskConfigGCP(storage int) DiskConfig {
	s := storage
	return DiskConfig{Type: "pd-ssd", Storage: &s}
}

// DiskConfigAzure builds an Azure managed disk config.
func DiskConfigAzure(storage int) (DiskConfig, error) {
	matrix := []struct {
		threshold int
		diskType  string
	}{
		{64, "P6"},
		{128, "P10"},
		{256, "P15"},
		{512, "P20"},
		{1024, "P30"},
		{2048, "P40"},
		{4096, "P50"},
		{8192, "P60"},
	}
	for _, row := range matrix {
		if row.threshold >= storage {
			auto := true
			return DiskConfig{Type: row.diskType, AutoExpansion: &auto}, nil
		}
	}
	return DiskConfig{}, fmtInvalidStorage(storage)
}

// DiskConfigForCloud selects disk config for a cloud provider.
func DiskConfigForCloud(cloud CloudType, storage int) (DiskConfig, error) {
	switch cloud {
	case CloudAWS:
		return DiskConfigAWS(storage)
	case CloudGCP:
		return DiskConfigGCP(storage), nil
	case CloudAzure:
		return DiskConfigAzure(storage)
	default:
		return DiskConfig{}, fmtUnsupportedCloud(cloud)
	}
}

// NodeConfig is compute+disk for a service group.
type NodeConfig struct {
	Compute *ComputeData `json:"compute,omitempty"`
	Disk    *DiskConfig  `json:"disk,omitempty"`
}

// ServiceGroupRequest is a create-cluster service group.
type ServiceGroupRequest struct {
	NumOfNodes int        `json:"numOfNodes"`
	Services   []string   `json:"services"`
	Node       NodeConfig `json:"node"`
}

// ServiceGroupData is a listed service group.
type ServiceGroupData struct {
	NumOfNodes int         `json:"numOfNodes"`
	Services   []string    `json:"services,omitempty"`
	Node       *NodeConfig `json:"node,omitempty"`
}

// CreateClusterRequest is POST /clusters body.
type CreateClusterRequest struct {
	Name            string                `json:"name"`
	Description     string                `json:"description,omitempty"`
	CloudProvider   CloudProviderData     `json:"cloudProvider"`
	CouchbaseServer *CouchbaseServerData  `json:"couchbaseServer,omitempty"`
	ServiceGroups   []ServiceGroupRequest `json:"serviceGroups"`
	Availability    AvailabilityData      `json:"availability"`
	Support         SupportData           `json:"support"`
}

// CreateBucketRequest is POST /buckets body.
type CreateBucketRequest struct {
	Name                     string `json:"name"`
	Type                     string `json:"type"`
	StorageBackend           string `json:"storageBackend"`
	MemoryAllocationInMb     int    `json:"memoryAllocationInMb"`
	BucketConflictResolution string `json:"bucketConflictResolution"`
	DurabilityLevel          string `json:"durabilityLevel"`
	Replicas                 int    `json:"replicas"`
	Flush                    bool   `json:"flush"`
	TimeToLiveInSeconds      int    `json:"timeToLiveInSeconds"`
}

// CreateProjectRequest is POST /projects body.
type CreateProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CreateAllowedCIDRRequest is POST /allowedcidrs body.
type CreateAllowedCIDRRequest struct {
	CIDR    string `json:"cidr"`
	Comment string `json:"comment,omitempty"`
}

// BucketStatsData is bucket usage stats.
type BucketStatsData struct {
	ItemCount       int `json:"itemCount"`
	OpsPerSecond    int `json:"opsPerSecond"`
	DiskUsedInMiB   int `json:"diskUsedInMib"`
	MemoryUsedInMiB int `json:"memoryUsedInMib"`
}

// CapellaBucketData is a Capella bucket.
type CapellaBucketData struct {
	ID                       string           `json:"id,omitempty"`
	Name                     string           `json:"name,omitempty"`
	Type                     string           `json:"type,omitempty"`
	StorageBackend           string           `json:"storageBackend,omitempty"`
	MemoryAllocationInMb     int              `json:"memoryAllocationInMb,omitempty"`
	BucketConflictResolution string           `json:"bucketConflictResolution,omitempty"`
	DurabilityLevel          string           `json:"durabilityLevel,omitempty"`
	Replicas                 int              `json:"replicas,omitempty"`
	Flush                    *bool            `json:"flush,omitempty"`
	FlushEnabled             *bool            `json:"flushEnabled,omitempty"`
	TimeToLiveInSeconds      int              `json:"timeToLiveInSeconds,omitempty"`
	EvictionPolicy           string           `json:"evictionPolicy,omitempty"`
	Stats                    *BucketStatsData `json:"stats,omitempty"`
	Priority                 int              `json:"priority,omitempty"`
}

// DatabaseResourceScopeData is credential scope access.
type DatabaseResourceScopeData struct {
	Name        string   `json:"name"`
	Collections []string `json:"collections,omitempty"`
}

// DatabaseResourceBucketData is credential bucket access.
type DatabaseResourceBucketData struct {
	Name   string                      `json:"name"`
	Scopes []DatabaseResourceScopeData `json:"scopes,omitempty"`
}

// DatabaseResourceData is credential resource tree.
type DatabaseResourceData struct {
	Buckets []DatabaseResourceBucketData `json:"buckets,omitempty"`
}

// DatabaseAccessEntry is a credential access grant.
type DatabaseAccessEntry struct {
	Privileges []string             `json:"privileges"`
	Resources  DatabaseResourceData `json:"resources"`
}

// CredentialData is a database credential.
type CredentialData struct {
	ID     string                `json:"id,omitempty"`
	Name   string                `json:"name,omitempty"`
	Access []DatabaseAccessEntry `json:"access,omitempty"`
	Audit  *AuditData            `json:"audit,omitempty"`
}

// CreateDatabaseCredentialRequest is POST /users body.
type CreateDatabaseCredentialRequest struct {
	Name     string                `json:"name"`
	Password string                `json:"password"`
	Access   []DatabaseAccessEntry `json:"access"`
}

// CreateDatabaseCredentialResponse is create credential response.
type CreateDatabaseCredentialResponse struct {
	ID       string `json:"id,omitempty"`
	Password string `json:"password,omitempty"`
}

// UpdateDatabaseCredentialRequest is PUT credential body.
type UpdateDatabaseCredentialRequest struct {
	Password string                `json:"password,omitempty"`
	Access   []DatabaseAccessEntry `json:"access,omitempty"`
}

// AllowedCIDRData is an allowed CIDR entry.
type AllowedCIDRData struct {
	ID        string     `json:"id,omitempty"`
	CIDR      string     `json:"cidr,omitempty"`
	Comment   string     `json:"comment,omitempty"`
	ExpiresAt string     `json:"expiresAt,omitempty"`
	Status    string     `json:"status,omitempty"`
	Type      string     `json:"type,omitempty"`
	Audit     *AuditData `json:"audit,omitempty"`
}

// CertificateResponse is GET /certificates body.
type CertificateResponse struct {
	Certificate string `json:"certificate"`
}

// IDResponse is a create response with id.
type IDResponse struct {
	ID string `json:"id"`
}

// ResourcesData is a user resource grant.
type ResourcesData struct {
	Type  string   `json:"type,omitempty"`
	ID    string   `json:"id,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

// CapellaOrgUserData is an organization user.
type CapellaOrgUserData struct {
	ID                  string          `json:"id,omitempty"`
	Name                string          `json:"name,omitempty"`
	Email               string          `json:"email,omitempty"`
	Status              string          `json:"status,omitempty"`
	Inactive            bool            `json:"inactive"`
	OrganizationID      string          `json:"organizationId,omitempty"`
	OrganizationRoles   []string        `json:"organizationRoles,omitempty"`
	LastLogin           string          `json:"lastLogin,omitempty"`
	Region              string          `json:"region,omitempty"`
	TimeZone            string          `json:"timeZone,omitempty"`
	EnableNotifications *bool           `json:"enableNotifications,omitempty"`
	ExpiresAt           string          `json:"expiresAt,omitempty"`
	Resources           []ResourcesData `json:"resources,omitempty"`
	Audit               *AuditData      `json:"audit,omitempty"`
}

// ClusterData is a Capella cluster.
type ClusterData struct {
	ID                         string               `json:"id,omitempty"`
	AppServiceID               string               `json:"appServiceId,omitempty"`
	Name                       string               `json:"name,omitempty"`
	Description                string               `json:"description,omitempty"`
	ConfigurationType          string               `json:"configurationType,omitempty"`
	ConnectionString           string               `json:"connectionString,omitempty"`
	CloudProvider              *CloudProviderData   `json:"cloudProvider,omitempty"`
	CouchbaseServer            *CouchbaseServerData `json:"couchbaseServer,omitempty"`
	ServiceGroups              []ServiceGroupData   `json:"serviceGroups,omitempty"`
	Availability               *AvailabilityData    `json:"availability,omitempty"`
	Support                    *SupportData         `json:"support,omitempty"`
	CurrentState               string               `json:"currentState,omitempty"`
	Audit                      *AuditData           `json:"audit,omitempty"`
	CMEKID                     string               `json:"cmekId,omitempty"`
	EnablePrivateDNSResolution *bool                `json:"enablePrivateDNSResolution,omitempty"`
}

// JSONPatchOp is a JSON Patch operation for user updates.
type JSONPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

func fmtInvalidStorage(storage int) error {
	return &CapellaAPIError{Message: "invalid storage value", Body: strconv.Itoa(storage)}
}

func fmtUnsupportedCloud(cloud CloudType) error {
	return &CapellaAPIError{Message: "unsupported cloud type", Body: string(cloud)}
}
