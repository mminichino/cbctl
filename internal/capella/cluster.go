package capella

import (
	"fmt"
	"time"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/models"
	"github.com/mminichino/cbctl/internal/rest"
)

// ServiceGroupConfig builds Capella service group sizing.
type ServiceGroupConfig struct {
	CPU        int
	RAM        int
	Storage    int
	NumOfNodes int
	Services   []string
}

// NewServiceGroupConfig returns Capella defaults (3 nodes, 4 CPU, 16 GiB RAM, 256 GiB disk).
func NewServiceGroupConfig() *ServiceGroupConfig {
	return &ServiceGroupConfig{
		CPU:        4,
		RAM:        16,
		Storage:    256,
		NumOfNodes: 3,
		Services:   append([]string(nil), rest.DefaultCapellaServices...),
	}
}

func (s *ServiceGroupConfig) WithCPU(v int) *ServiceGroupConfig {
	s.CPU = v
	return s
}
func (s *ServiceGroupConfig) WithRAM(v int) *ServiceGroupConfig {
	s.RAM = v
	return s
}
func (s *ServiceGroupConfig) WithStorage(v int) *ServiceGroupConfig {
	s.Storage = v
	return s
}
func (s *ServiceGroupConfig) WithNumOfNodes(v int) *ServiceGroupConfig {
	s.NumOfNodes = v
	return s
}
func (s *ServiceGroupConfig) WithServices(services []string) *ServiceGroupConfig {
	s.Services = append([]string(nil), services...)
	return s
}

// ToRequest converts to API service group request.
func (s *ServiceGroupConfig) ToRequest(cloud CloudType) (ServiceGroupRequest, error) {
	disk, err := DiskConfigForCloud(cloud, s.Storage)
	if err != nil {
		return ServiceGroupRequest{}, err
	}
	return ServiceGroupRequest{
		NumOfNodes: s.NumOfNodes,
		Services:   append([]string(nil), s.Services...),
		Node: NodeConfig{
			Compute: &ComputeData{CPU: s.CPU, RAM: s.RAM},
			Disk:    &disk,
		},
	}, nil
}

// ClusterConfig builds Capella create-cluster requests.
type ClusterConfig struct {
	Description      string
	CloudType        CloudType
	CloudRegion      string
	CIDR             string
	Version          string
	AvailabilityType AvailabilityType
	SupportPlan      SupportPlanType
	TimeZone         TimeZoneType
	ServiceGroups    []*ServiceGroupConfig
}

// NewClusterConfig returns Capella defaults (AWS, multi-zone, developer pro, PT).
func NewClusterConfig() *ClusterConfig {
	return &ClusterConfig{
		Description:      "Automation Managed Cluster",
		CloudType:        CloudAWS,
		AvailabilityType: AvailabilityMultiZone,
		SupportPlan:      SupportDeveloper,
		TimeZone:         TimeZoneUSWest,
	}
}

func (c *ClusterConfig) WithDescription(v string) *ClusterConfig {
	c.Description = v
	return c
}
func (c *ClusterConfig) WithCloudType(v CloudType) *ClusterConfig {
	c.CloudType = v
	return c
}
func (c *ClusterConfig) WithCloudRegion(v string) *ClusterConfig {
	c.CloudRegion = v
	return c
}
func (c *ClusterConfig) WithCIDR(v string) *ClusterConfig {
	c.CIDR = v
	return c
}
func (c *ClusterConfig) WithVersion(v string) *ClusterConfig {
	c.Version = v
	return c
}
func (c *ClusterConfig) WithAvailability(v AvailabilityType) *ClusterConfig {
	c.AvailabilityType = v
	return c
}
func (c *ClusterConfig) WithSupportPlan(v SupportPlanType) *ClusterConfig {
	c.SupportPlan = v
	return c
}
func (c *ClusterConfig) WithTimeZone(v TimeZoneType) *ClusterConfig {
	c.TimeZone = v
	return c
}
func (c *ClusterConfig) AddServiceGroup(g *ServiceGroupConfig) *ClusterConfig {
	c.ServiceGroups = append(c.ServiceGroups, g)
	return c
}

// SingleNode applies single-zone + 1-node service group (storage 100), matching Python ClusterConfig.single_node.
func (c *ClusterConfig) SingleNode(services []string) *ClusterConfig {
	c.AvailabilityType = AvailabilitySingleZone
	group := NewServiceGroupConfig().WithNumOfNodes(1).WithStorage(100)
	if services != nil {
		group.WithServices(services)
	}
	return c.AddServiceGroup(group)
}

// Create builds the Capella create cluster request body.
func (c *ClusterConfig) Create(clusterName string) (CreateClusterRequest, error) {
	if len(c.ServiceGroups) == 0 {
		c.AddServiceGroup(NewServiceGroupConfig())
	}
	if c.CloudRegion == "" {
		switch c.CloudType {
		case CloudGCP:
			c.CloudRegion = "us-east4"
		case CloudAzure:
			c.CloudRegion = "eastus"
		default:
			c.CloudRegion = "us-east-2"
		}
	}
	groups := make([]ServiceGroupRequest, 0, len(c.ServiceGroups))
	for _, sg := range c.ServiceGroups {
		req, err := sg.ToRequest(c.CloudType)
		if err != nil {
			return CreateClusterRequest{}, err
		}
		groups = append(groups, req)
	}
	var server *CouchbaseServerData
	if c.Version != "" {
		server = &CouchbaseServerData{Version: c.Version}
	}
	return CreateClusterRequest{
		Name:        clusterName,
		Description: c.Description,
		CloudProvider: CloudProviderData{
			Type:   string(c.CloudType),
			Region: c.CloudRegion,
			CIDR:   c.CIDR,
		},
		CouchbaseServer: server,
		ServiceGroups:   groups,
		Availability:    AvailabilityData{Type: string(c.AvailabilityType)},
		Support: SupportData{
			Plan:     string(c.SupportPlan),
			Timezone: string(c.TimeZone),
		},
	}, nil
}

// BuildClusterConfig mirrors Python build_capella_cluster_config.
func BuildClusterConfig(nodes []models.CapellaNodeConfig) *ClusterConfig {
	cfg := NewClusterConfig()
	if len(nodes) == 0 {
		return cfg.SingleNode(rest.DefaultCapellaServices)
	}
	cfg.WithAvailability(AvailabilitySingleZone)
	for _, node := range nodes {
		services := node.Services
		if len(services) == 0 {
			services = rest.DefaultCapellaServices
		}
		cfg.AddServiceGroup(
			NewServiceGroupConfig().
				WithCPU(node.CPU).
				WithRAM(node.RAM).
				WithNumOfNodes(1).
				WithStorage(100).
				WithServices(services),
		)
	}
	return cfg
}

// Cluster manages Capella cluster resources.
type Cluster struct {
	Project     *Project
	Data        *ClusterData
	Buckets     *Buckets
	Credentials *Credentials
	AllowedCIDR *AllowedCIDRs
	Certificate *Certificate
}

// NewCluster constructs an unresolved cluster helper.
func NewCluster(project *Project) *Cluster {
	return &Cluster{Project: project}
}

// Endpoint returns clusters collection path.
func (c *Cluster) Endpoint() string {
	return fmt.Sprintf("%s/%s/clusters", c.Project.Endpoint(), c.Project.ID())
}

func (c *Cluster) client() *Client { return c.Project.Org.Client }

// Resolve loads cluster by configured database id/name.
func (c *Cluster) Resolve() error {
	client := c.client()
	var err error
	switch {
	case client.hasDatabaseID():
		c.Data, err = c.GetByID(client.DatabaseID)
	case client.hasDatabaseName():
		c.Data, err = c.GetByName(client.DatabaseName)
	default:
		return notFound("database name or id required")
	}
	if err != nil {
		return err
	}
	c.attachServices()
	return nil
}

// Add attaches an existing cluster by name.
func (c *Cluster) Add(clusterName string) error {
	data, err := c.GetByName(clusterName)
	if err != nil {
		return err
	}
	c.Data = data
	c.attachServices()
	c.populateCertificate()
	c.Project.RegisterCluster(c)
	return nil
}

// Wait polls cluster state.
func (c *Cluster) Wait(clusterID string, state State, op StateWaitOperation) State {
	path := c.Endpoint() + "/" + clusterID
	waitDestroyed := op == StateWaitNotEquals && state == StateDestroying
	for i := 0; i < 600; i++ {
		var data ClusterData
		err := c.client().GetJSON(path, &data)
		if err != nil {
			if IsNotFound(err) {
				return StateDestroyed
			}
			time.Sleep(time.Second)
			continue
		}
		current := State(data.CurrentState)
		if current == StateFailed {
			return StateFailed
		}
		if waitDestroyed {
			time.Sleep(time.Second)
			continue
		}
		if !op.evaluate(current == state) {
			time.Sleep(time.Second)
			continue
		}
		return StateHealthy
	}
	return StateUnknown
}

// List clusters in the project.
func (c *Cluster) List() ([]ClusterData, error) {
	items, err := c.client().GetPaged(c.Endpoint(), 50)
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, apiError(c.client().lastStatus, c.client().lastBody, "Cluster List Error", err)
	}
	return DecodePaged[ClusterData](items)
}

// FindByName returns a cluster from the list or nil.
func (c *Cluster) FindByName(name string) (*ClusterData, error) {
	list, err := c.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}
	return nil, nil
}

// Create creates a cluster or waits if it already exists.
func (c *Cluster) Create(clusterName string, cfg *ClusterConfig) error {
	existing, err := c.FindByName(clusterName)
	if err != nil {
		return err
	}
	if existing != nil {
		waitResult := c.Wait(existing.ID, StateHealthy, StateWaitEquals)
		if waitResult != StateHealthy {
			return fmt.Errorf("Cluster is not healthy: %s", waitResult)
		}
		data, err := c.GetByID(existing.ID)
		if err != nil {
			return fmt.Errorf("Cluster lookup failed: %w", err)
		}
		c.Data = data
		c.attachServices()
		c.populateCertificate()
		c.Project.RegisterCluster(c)
		return nil
	}

	params, err := cfg.Create(clusterName)
	if err != nil {
		return err
	}
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var idResp IDResponse
		if err := c.client().PostJSON(c.Endpoint(), params, &idResp); err != nil {
			return apiError(c.client().lastStatus, c.client().lastBody, "Cluster Create Error", err)
		}
		waitResult := c.Wait(idResp.ID, StateHealthy, StateWaitEquals)
		if waitResult == StateHealthy {
			data, err := c.GetByID(idResp.ID)
			if err != nil {
				return fmt.Errorf("Cluster creation failed: %w", err)
			}
			c.Data = data
			c.attachServices()
			c.populateCertificate()
			c.Project.RegisterCluster(c)
			return nil
		}
		if waitResult == StateFailed {
			c.deleteByID(idResp.ID)
			if attempt < maxAttempts {
				continue
			}
			return fmt.Errorf("Cluster creation failed after %d attempts", maxAttempts)
		}
		return fmt.Errorf("Cluster creation failed: %s", waitResult)
	}
	return fmt.Errorf("Cluster creation failed")
}

func (c *Cluster) deleteByID(clusterID string) {
	_ = c.client().Delete(c.Endpoint() + "/" + clusterID)
	_ = c.Wait(clusterID, StateDestroying, StateWaitNotEquals)
}

func (c *Cluster) attachServices() {
	if c.Data == nil || c.Data.ID == "" {
		return
	}
	c.Buckets = NewBuckets(c)
	c.Credentials = NewCredentials(c)
	c.AllowedCIDR = NewAllowedCIDRs(c)
	c.Certificate = NewCertificate(c)
}

func (c *Cluster) populateCertificate() {
	if c.Certificate == nil {
		return
	}
	pem, err := c.Certificate.GetClusterCertificate()
	if err != nil {
		return
	}
	c.Certificate.SetCertificate(pem)
}

// Delete destroys the cluster and waits until gone.
func (c *Cluster) Delete() error {
	if c.Data == nil || c.Data.ID == "" {
		return nil
	}
	id := c.Data.ID
	name := c.Data.Name
	if err := c.client().Delete(c.Endpoint() + "/" + id); err != nil {
		return apiError(c.client().lastStatus, c.client().lastBody, "Cluster Delete Error", err)
	}
	waitResult := c.Wait(id, StateDestroying, StateWaitNotEquals)
	if waitResult != StateDestroyed {
		return fmt.Errorf("Cluster deletion failed: %s", waitResult)
	}
	logging.Info("Capella cluster %s destroyed", name)
	c.Data = nil
	return nil
}

// GetByName finds a cluster by name.
func (c *Cluster) GetByName(clusterName string) (*ClusterData, error) {
	if c.Data != nil && c.Data.Name == clusterName {
		return c.Data, nil
	}
	if cached := c.Project.FindClusterByName(clusterName); cached != nil && cached.Data != nil {
		return cached.Data, nil
	}
	list, err := c.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == clusterName {
			return &list[i], nil
		}
	}
	return nil, notFound(fmt.Sprintf("Can not find cluster %s", clusterName))
}

// GetByID GETs a cluster by ID.
func (c *Cluster) GetByID(clusterID string) (*ClusterData, error) {
	if c.Data != nil && c.Data.ID == clusterID {
		return c.Data, nil
	}
	if cached := c.Project.FindClusterByID(clusterID); cached != nil && cached.Data != nil {
		return cached.Data, nil
	}
	var data ClusterData
	if err := c.client().GetJSON(c.Endpoint()+"/"+clusterID, &data); err != nil {
		if IsNotFound(err) {
			return nil, notFound("Cluster ID not found")
		}
		return nil, apiError(c.client().lastStatus, c.client().lastBody, "Cluster Get Error", err)
	}
	return &data, nil
}

// ConnectString returns the normalized couchbases:// connection string.
func (c *Cluster) ConnectString() (string, error) {
	if c.Data == nil || c.Data.ID == "" {
		return "", nil
	}
	data, err := c.GetByID(c.Data.ID)
	if err == nil {
		c.Data = data
	}
	if c.Data == nil || c.Data.ConnectionString == "" {
		return "", nil
	}
	return NormalizeConnectString(c.Data.ConnectionString), nil
}
