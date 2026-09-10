package capella

import (
	"fmt"

	"github.com/mminichino/cbctl/internal/logging"
)

// Project manages Capella project resources.
type Project struct {
	Org         *Organization
	Project     *ProjectData
	projectName string
	user        *User
	byID        map[string]*Cluster
	byName      map[string]*Cluster
}

// NewProject constructs a project helper (unresolved unless data is provided).
func NewProject(org *Organization, projectName string, data *ProjectData) *Project {
	p := &Project{
		Org:    org,
		byID:   map[string]*Cluster{},
		byName: map[string]*Cluster{},
	}
	if data != nil {
		p.Project = data
		if data.Name != "" {
			p.projectName = data.Name
		} else {
			p.projectName = projectName
		}
	} else {
		if projectName == "" && org != nil && org.Client != nil {
			projectName = org.Client.ProjectName
		}
		if projectName == "" {
			projectName = "default"
		}
		p.projectName = projectName
	}
	return p
}

// Endpoint returns projects collection path.
func (p *Project) Endpoint() string {
	return fmt.Sprintf("%s/%s/projects", p.Org.Endpoint(), p.Org.Organization.ID)
}

// ID returns the resolved project ID.
func (p *Project) ID() string {
	if p.Project == nil {
		return ""
	}
	return p.Project.ID
}

// Name returns the project name.
func (p *Project) Name() string {
	if p.Project != nil && p.Project.Name != "" {
		return p.Project.Name
	}
	return p.projectName
}

// Resolve loads or creates the project and requires a configured user.
func (p *Project) Resolve() error {
	u, err := p.Org.User()
	if err != nil {
		var unc *UserNotConfiguredError
		if AsUserNotConfigured(err, &unc) {
			return fmt.Errorf("Capella user not configured. Please set capella.user.email or capella.user.id")
		}
		return err
	}
	p.user = u
	if _, err := p.GetProject(""); err != nil {
		return err
	}
	logging.Info("Project ID: %s", p.ID())
	return nil
}

// AsUserNotConfigured extracts UserNotConfiguredError.
func AsUserNotConfigured(err error, target **UserNotConfiguredError) bool {
	if err == nil || target == nil {
		return false
	}
	for err != nil {
		if e, ok := err.(*UserNotConfiguredError); ok {
			*target = e
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// GetDefaultCluster resolves the configured database cluster if present.
func (p *Project) GetDefaultCluster() (*Cluster, error) {
	c := NewCluster(p)
	if err := c.Resolve(); err != nil {
		var nf *CapellaNotFoundError
		if AsCapellaNotFound(err, &nf) {
			return c, nil
		}
		label := p.Org.Client.DatabaseName
		if p.Org.Client.hasDatabaseID() {
			label = p.Org.Client.DatabaseID
		}
		return nil, fmt.Errorf("Can not find cluster %s: %w", label, err)
	}
	if c.Data != nil {
		p.RegisterCluster(c)
	}
	return c, nil
}

// CreateCluster creates or reuses a Capella cluster.
func (p *Project) CreateCluster(clusterName string, cfg *ClusterConfig) (*Cluster, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cluster_config is required")
	}
	if clusterName == "" {
		if p.Org.Client.hasDatabaseName() {
			clusterName = p.Org.Client.DatabaseName
		} else {
			clusterName = RandomName()
		}
	}
	if cached := p.FindClusterByName(clusterName); cached != nil {
		if err := cached.Create(clusterName, cfg); err != nil {
			return nil, fmt.Errorf("Can not create cluster %s: %w", clusterName, err)
		}
		return cached, nil
	}
	c := NewCluster(p)
	if err := c.Create(clusterName, cfg); err != nil {
		return nil, fmt.Errorf("Can not create cluster %s: %w", clusterName, err)
	}
	p.RegisterCluster(c)
	return c, nil
}

// AddCluster attaches an existing cluster by name.
func (p *Project) AddCluster(clusterName string) (*Cluster, error) {
	if cached := p.FindClusterByName(clusterName); cached != nil {
		return cached, nil
	}
	c := NewCluster(p)
	if err := c.Add(clusterName); err != nil {
		return nil, fmt.Errorf("Can not add cluster %s: %w", clusterName, err)
	}
	return c, nil
}

// RegisterCluster caches a cluster.
func (p *Project) RegisterCluster(c *Cluster) {
	if c == nil || c.Data == nil || c.Data.ID == "" || c.Data.Name == "" {
		return
	}
	p.byID[c.Data.ID] = c
	p.byName[c.Data.Name] = c
}

// FindClusterByID returns a cached cluster.
func (p *Project) FindClusterByID(id string) *Cluster { return p.byID[id] }

// FindClusterByName returns a cached cluster.
func (p *Project) FindClusterByName(name string) *Cluster { return p.byName[name] }

// ListProjects lists projects in the organization.
func (p *Project) ListProjects() ([]ProjectData, error) {
	items, err := p.Org.Client.GetPaged(p.Endpoint(), 50)
	if err != nil {
		return nil, apiError(p.Org.Client.lastStatus, p.Org.Client.lastBody, "Project List Error", err)
	}
	return DecodePaged[ProjectData](items)
}

// GetProject resolves by ID or creates/looks up by name when projectID is empty.
func (p *Project) GetProject(projectID string) (*ProjectData, error) {
	if projectID == "" {
		return p.resolveOrCreate()
	}
	if p.Project != nil && p.Project.ID == projectID {
		return p.Project, nil
	}
	if cached := p.Org.FindProjectByID(projectID); cached != nil && cached.Project != nil {
		return cached.Project, nil
	}
	return p.Org.FetchProject(projectID)
}

func (p *Project) resolveOrCreate() (*ProjectData, error) {
	if p.Org.Client.hasProjectID() {
		data, err := p.GetProject(p.Org.Client.ProjectID)
		if err != nil {
			return nil, err
		}
		p.Project = data
		return p.Project, nil
	}
	projects, err := p.getByEmail()
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if p.projectName == projects[i].Name {
			p.Project = &projects[i]
			return p.Project, nil
		}
	}
	return p.CreateProject()
}

// CreateProject creates a project and grants projectOwner to the user.
func (p *Project) CreateProject() (*ProjectData, error) {
	if p.user == nil {
		u, err := p.Org.User()
		if err != nil {
			return nil, err
		}
		p.user = u
	}
	req := CreateProjectRequest{
		Name:        p.projectName,
		Description: "Automatically Created Project",
	}
	var idResp IDResponse
	if err := p.Org.Client.PostJSON(p.Endpoint(), req, &idResp); err != nil {
		return nil, apiError(p.Org.Client.lastStatus, p.Org.Client.lastBody, "Project Create Error", err)
	}
	if err := p.user.SetProjectOwnership(idResp.ID); err != nil {
		return nil, err
	}
	data, err := p.GetProject(idResp.ID)
	if err != nil {
		return nil, err
	}
	p.Project = data
	p.Org.RegisterProject(p)
	return p.Project, nil
}

func (p *Project) getByEmail() ([]ProjectData, error) {
	if p.user == nil {
		u, err := p.Org.User()
		if err != nil {
			return nil, err
		}
		p.user = u
	}
	ids := p.user.ProjectIDs()
	out := make([]ProjectData, 0, len(ids))
	for _, id := range ids {
		data, err := p.GetProject(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *data)
	}
	return out, nil
}
