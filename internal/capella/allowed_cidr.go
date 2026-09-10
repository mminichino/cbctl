package capella

import "fmt"

// AllowedCIDRs manages Capella allowed CIDR entries.
type AllowedCIDRs struct {
	Cluster *Cluster
	CIDR    *AllowedCIDRData
}

// NewAllowedCIDRs constructs an allowed-CIDR helper.
func NewAllowedCIDRs(cluster *Cluster) *AllowedCIDRs {
	return &AllowedCIDRs{Cluster: cluster}
}

func (a *AllowedCIDRs) endpoint() string {
	return fmt.Sprintf("%s/%s/allowedcidrs", a.Cluster.Endpoint(), a.Cluster.Data.ID)
}

func (a *AllowedCIDRs) client() *Client { return a.Cluster.client() }

// List allowed CIDRs.
func (a *AllowedCIDRs) List() ([]AllowedCIDRData, error) {
	items, err := a.client().GetPaged(a.endpoint(), 50)
	if err != nil {
		return nil, apiError(a.client().lastStatus, a.client().lastBody, "Allowed CIDR List Error", err)
	}
	return DecodePaged[AllowedCIDRData](items)
}

// FindByCIDR returns an entry or nil.
func (a *AllowedCIDRs) FindByCIDR(network string) (*AllowedCIDRData, error) {
	list, err := a.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].CIDR == network {
			return &list[i], nil
		}
	}
	return nil, nil
}

// CreateAllowedCIDR allows a network if not already present.
func (a *AllowedCIDRs) CreateAllowedCIDR(network string) (*AllowedCIDRData, error) {
	if existing, err := a.FindByCIDR(network); err != nil {
		return nil, err
	} else if existing != nil {
		a.CIDR = existing
		return existing, nil
	}
	req := CreateAllowedCIDRRequest{
		CIDR:    network,
		Comment: "Automatically Created Allowed CIDR Block",
	}
	var idResp IDResponse
	if err := a.client().PostJSON(a.endpoint(), req, &idResp); err != nil {
		return nil, apiError(a.client().lastStatus, a.client().lastBody, "Allowed CIDR Create Error", err)
	}
	data, err := a.GetByID(idResp.ID)
	if err != nil {
		return nil, fmt.Errorf("Allowed CIDR creation failed: %w", err)
	}
	a.CIDR = data
	return data, nil
}

// GetByID GETs an allowed CIDR by ID.
func (a *AllowedCIDRs) GetByID(id string) (*AllowedCIDRData, error) {
	if a.CIDR != nil && a.CIDR.ID == id {
		return a.CIDR, nil
	}
	var data AllowedCIDRData
	if err := a.client().GetJSON(a.endpoint()+"/"+id, &data); err != nil {
		if IsNotFound(err) {
			return nil, notFound("CIDR not found")
		}
		return nil, apiError(a.client().lastStatus, a.client().lastBody, "Allowed CIDR Get Error", err)
	}
	return &data, nil
}

// Delete deletes the current CIDR entry.
func (a *AllowedCIDRs) Delete() error {
	if a.CIDR == nil || a.CIDR.ID == "" {
		return nil
	}
	if err := a.client().Delete(a.endpoint() + "/" + a.CIDR.ID); err != nil {
		return apiError(a.client().lastStatus, a.client().lastBody, "Allowed CIDR Delete Error", err)
	}
	a.CIDR = nil
	return nil
}
