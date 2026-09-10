package capella

import "fmt"

// Credentials manages Capella database credentials.
type Credentials struct {
	Cluster  *Cluster
	User     *CredentialData
	Username string
	Password string
}

// NewCredentials constructs a credentials helper.
func NewCredentials(cluster *Cluster) *Credentials {
	return &Credentials{Cluster: cluster}
}

func (c *Credentials) endpoint() string {
	return fmt.Sprintf("%s/%s/users", c.Cluster.Endpoint(), c.Cluster.Data.ID)
}

func (c *Credentials) client() *Client { return c.Cluster.client() }

func defaultAccess() []DatabaseAccessEntry {
	return []DatabaseAccessEntry{{
		Privileges: []string{"data_reader", "data_writer"},
		Resources: DatabaseResourceData{
			Buckets: []DatabaseResourceBucketData{{Name: "*"}},
		},
	}}
}

// List credentials.
func (c *Credentials) List() ([]CredentialData, error) {
	items, err := c.client().GetPaged(c.endpoint(), 50)
	if err != nil {
		return nil, apiError(c.client().lastStatus, c.client().lastBody, "Credentials List Error", err)
	}
	return DecodePaged[CredentialData](items)
}

// FindByName returns a credential or nil.
func (c *Credentials) FindByName(username string) (*CredentialData, error) {
	list, err := c.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == username {
			return &list[i], nil
		}
	}
	return nil, nil
}

// CreateCredential creates a database user if missing.
func (c *Credentials) CreateCredential(username, password string, access []DatabaseAccessEntry) (*CreateDatabaseCredentialResponse, error) {
	c.Username = username
	c.Password = password
	if existing, err := c.FindByName(username); err != nil {
		return nil, err
	} else if existing != nil {
		c.User = existing
		return &CreateDatabaseCredentialResponse{ID: existing.ID}, nil
	}
	if len(access) == 0 {
		access = defaultAccess()
	}
	req := CreateDatabaseCredentialRequest{
		Name:     username,
		Password: password,
		Access:   access,
	}
	var resp CreateDatabaseCredentialResponse
	if err := c.client().PostJSON(c.endpoint(), req, &resp); err != nil {
		return nil, apiError(c.client().lastStatus, c.client().lastBody, "Credentials Create Error", err)
	}
	user, err := c.GetByID(resp.ID)
	if err != nil {
		return nil, fmt.Errorf("Database credential creation failed: %w", err)
	}
	c.User = user
	return &resp, nil
}

// AddCredentials loads an existing credential by username.
func (c *Credentials) AddCredentials(username, password string) (*CredentialData, error) {
	c.Username = username
	c.Password = password
	user, err := c.GetByName(username)
	if err != nil {
		return nil, err
	}
	c.User = user
	return user, nil
}

// GetByName finds a credential by name.
func (c *Credentials) GetByName(username string) (*CredentialData, error) {
	if c.User != nil && c.User.Name == username {
		return c.User, nil
	}
	found, err := c.FindByName(username)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, notFound(fmt.Sprintf("Can not find user %s", username))
	}
	return found, nil
}

// GetByID GETs a credential by ID.
func (c *Credentials) GetByID(id string) (*CredentialData, error) {
	if c.User != nil && c.User.ID == id {
		return c.User, nil
	}
	var data CredentialData
	if err := c.client().GetJSON(c.endpoint()+"/"+id, &data); err != nil {
		if IsNotFound(err) {
			return nil, notFound("Database credential not found")
		}
		return nil, apiError(c.client().lastStatus, c.client().lastBody, "Credentials Get Error", err)
	}
	return &data, nil
}

// Delete deletes the current credential.
func (c *Credentials) Delete() error {
	if c.User == nil || c.User.ID == "" {
		return nil
	}
	if err := c.client().Delete(c.endpoint() + "/" + c.User.ID); err != nil {
		return apiError(c.client().lastStatus, c.client().lastBody, "Credentials Delete Error", err)
	}
	c.User = nil
	return nil
}
