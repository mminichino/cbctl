package capella

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
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

// DefaultWaitTimeout bounds each legacy cluster state wait (create or delete). The pre-port loop
// polled 600 times about a second apart; Capella deploys usually finish well inside 20 minutes.
const DefaultWaitTimeout = 20 * time.Minute

// clusterGetter adapts the legacy client to engine.ClusterGetter so legacy waits share the
// managed path's backoff, failure states, timeout and cancellation.
type clusterGetter struct{ c *Cluster }

func (g clusterGetter) GetCluster(_ context.Context, _, clusterID string) (*engine.ClusterWithState, error) {
	var data ClusterData
	if err := g.c.client().GetJSON(g.c.Endpoint()+"/"+clusterID, &data); err != nil {
		if IsNotFound(err) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	return &engine.ClusterWithState{Name: data.Name, CurrentState: data.CurrentState}, nil
}

// wait polls clusterID until op succeeds. It returns *engine.WaitFailed for a failure state,
// *engine.WaitTimeout at the deadline, the API error for a failed read (401/403 end the wait
// at once), and ctx.Err() when the command is cancelled.
func (c *Cluster) wait(clusterID, name string, op engine.Op) error {
	client := c.client()
	clk := client.Clock
	if clk == nil {
		clk = engine.RealClock{}
	}
	timeout := client.WaitTimeout
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	_, err := engine.WaitForCluster(client.context(), clusterGetter{c}, c.Project.ID(), clusterID, op, timeout, clk, name, engine.WaitHooks{})
	return err
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
		if err := c.wait(existing.ID, clusterName, engine.OpReady); err != nil {
			return fmt.Errorf("waiting for existing cluster %s: %w", clusterName, err)
		}
		data, err := c.fetchByID(existing.ID)
		if err != nil {
			return fmt.Errorf("cluster lookup failed: %w", err)
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
		err := c.wait(idResp.ID, clusterName, engine.OpCreate)
		if err == nil {
			data, err := c.fetchByID(idResp.ID)
			if err != nil {
				return fmt.Errorf("cluster creation failed: %w", err)
			}
			c.Data = data
			c.attachServices()
			c.populateCertificate()
			c.Project.RegisterCluster(c)
			return nil
		}
		var failed *engine.WaitFailed
		if !errors.As(err, &failed) {
			return err // timeout, cancellation or an API error: the cluster may still be deploying
		}
		if derr := c.deleteByID(idResp.ID, clusterName); derr != nil {
			return errors.Join(err, fmt.Errorf("removing failed cluster %s: %w", idResp.ID, derr))
		}
		if attempt == maxAttempts {
			return fmt.Errorf("cluster creation failed after %d attempts: %w", maxAttempts, err)
		}
		logging.Warning("Cluster %s entered %s; retrying (attempt %d of %d)", clusterName, failed.State, attempt+1, maxAttempts)
	}
	return fmt.Errorf("cluster creation failed")
}

func (c *Cluster) deleteByID(clusterID, name string) error {
	if err := c.client().Delete(c.Endpoint() + "/" + clusterID); err != nil && !IsNotFound(err) {
		return err
	}
	return c.wait(clusterID, name, engine.OpDelete)
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
		if !IsNotFound(err) {
			return apiError(c.client().lastStatus, c.client().lastBody, "Cluster Delete Error", err)
		}
	}
	if err := c.wait(id, name, engine.OpDelete); err != nil {
		return fmt.Errorf("cluster deletion failed: %w", err)
	}
	logging.Success("Capella cluster %s destroyed", name)
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

// fetchByID GETs a cluster by ID, bypassing the cache (used after a wait, when cached data is stale).
func (c *Cluster) fetchByID(clusterID string) (*ClusterData, error) {
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
