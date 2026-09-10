package capella

import (
	"fmt"

	"github.com/mminichino/cbctl/internal/logging"
)

// Organization manages Capella organization resources.
type Organization struct {
	Client       *Client
	Organization *OrganizationData
	user         *User
	byID         map[string]*Project
	byName       map[string]*Project
}

// NewOrganization resolves the default organization for the client.
func NewOrganization(client *Client) (*Organization, error) {
	o := &Organization{
		Client: client,
		byID:   map[string]*Project{},
		byName: map[string]*Project{},
	}
	org, err := o.defaultOrg()
	if err != nil {
		return nil, err
	}
	o.Organization = org
	logging.Info("Organization ID: %s", org.ID)
	return o, nil
}

// Endpoint returns /v4/organizations.
func (o *Organization) Endpoint() string {
	return organizationsPath
}

// User returns (and caches) the configured Capella user.
func (o *Organization) User() (*User, error) {
	if o.user == nil {
		u, err := NewUser(o)
		if err != nil {
			return nil, err
		}
		o.user = u
	}
	return o.user, nil
}

// DefaultProject resolves the client's default project.
func (o *Organization) DefaultProject() (*Project, error) {
	name := configDefaultProject(o.Client)
	return o.GetProject(name)
}

func configDefaultProject(c *Client) string {
	if c != nil && c.ProjectName != "" {
		return c.ProjectName
	}
	return "default"
}

// GetProject resolves a project by name (creating via Project.resolve).
func (o *Organization) GetProject(projectName string) (*Project, error) {
	if cached := o.byName[projectName]; cached != nil {
		return cached, nil
	}
	p := NewProject(o, projectName, nil)
	if err := p.Resolve(); err != nil {
		return nil, err
	}
	o.RegisterProject(p)
	return p, nil
}

// GetProjectByID resolves a project by ID.
func (o *Organization) GetProjectByID(projectID string) (*Project, error) {
	if cached := o.byID[projectID]; cached != nil {
		return cached, nil
	}
	data, err := o.FetchProject(projectID)
	if err != nil {
		return nil, err
	}
	p := NewProject(o, data.Name, data)
	o.RegisterProject(p)
	return p, nil
}

// FetchProject GETs a project by ID.
func (o *Organization) FetchProject(projectID string) (*ProjectData, error) {
	path := fmt.Sprintf("%s/%s/projects/%s", o.Endpoint(), o.Organization.ID, projectID)
	var data ProjectData
	if err := o.Client.GetJSON(path, &data); err != nil {
		if IsNotFound(err) {
			return nil, notFound("Project ID not found")
		}
		return nil, apiError(o.Client.lastStatus, o.Client.lastBody, "Project Get Error", err)
	}
	return &data, nil
}

// RegisterProject caches a resolved project.
func (o *Organization) RegisterProject(p *Project) {
	if p == nil || p.Project == nil {
		return
	}
	o.byID[p.ID()] = p
	o.byName[p.Name()] = p
}

// FindProjectByID returns a cached project.
func (o *Organization) FindProjectByID(id string) *Project { return o.byID[id] }

// FindProjectByName returns a cached project.
func (o *Organization) FindProjectByName(name string) *Project { return o.byName[name] }

// List organizations.
func (o *Organization) List() ([]OrganizationData, error) {
	var wrap struct {
		Data []OrganizationData `json:"data"`
	}
	if err := o.Client.GetJSON(organizationsPath, &wrap); err != nil {
		return nil, apiError(o.Client.lastStatus, o.Client.lastBody, "Organization List Error", err)
	}
	return wrap.Data, nil
}

// GetByID returns an organization by ID.
func (o *Organization) GetByID(orgID string) (*OrganizationData, error) {
	if o.Organization != nil && o.Organization.ID == orgID {
		return o.Organization, nil
	}
	var data OrganizationData
	if err := o.Client.GetJSON(organizationsPath+"/"+orgID, &data); err != nil {
		if IsNotFound(err) {
			return nil, notFound("Organization ID not found")
		}
		return nil, apiError(o.Client.lastStatus, o.Client.lastBody, "Organization Get Error", err)
	}
	return &data, nil
}

// GetByName returns an organization by name.
func (o *Organization) GetByName(name string) (*OrganizationData, error) {
	if o.Organization != nil && o.Organization.Name == name {
		return o.Organization, nil
	}
	orgs, err := o.List()
	if err != nil {
		return nil, err
	}
	for i := range orgs {
		if orgs[i].Name == name {
			return &orgs[i], nil
		}
	}
	return nil, notFound(fmt.Sprintf("Can not find organization %s", name))
}

func (o *Organization) defaultOrg() (*OrganizationData, error) {
	if o.Client.hasOrganizationID() {
		return o.GetByID(o.Client.OrganizationID)
	}
	if o.Client.hasOrganizationName() {
		return o.GetByName(o.Client.OrganizationName)
	}
	orgs, err := o.List()
	if err != nil {
		return nil, fmt.Errorf("Can not find the Capella Organization: %w", err)
	}
	if len(orgs) == 0 {
		return nil, fmt.Errorf("Can not find the Capella Organization")
	}
	return &orgs[0], nil
}
