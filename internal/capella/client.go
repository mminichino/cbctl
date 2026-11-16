package capella

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config"
)

const organizationsPath = "/v4/organizations"

// Client is a Capella Management API v4 client (Bearer token, HTTPS :443).
type Client struct {
	Token            string
	APIHost          string
	BaseURL          string // if set, wins over https://APIHost
	OrganizationName string
	OrganizationID   string
	ProjectName      string
	ProjectID        string
	DatabaseName     string
	DatabaseID       string
	AccountEmail     string
	AccountID        string
	HTTP             *http.Client
	lastStatus       int
	lastBody         string

	apiOnce sync.Once
	api     *capellaapi.Client
	apiErr  error
}

// NewClient constructs a Capella API client.
func NewClient(token, apiHost string) *Client {
	if apiHost == "" {
		apiHost = config.DefaultCapellaAPIHost
	}
	return &Client{
		Token:       token,
		APIHost:     apiHost,
		ProjectName: config.DefaultCapellaProjectName,
		HTTP:        &http.Client{},
	}
}

// NewClientFromConfig builds a client from config properties.
func NewClientFromConfig(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	token := cfg.Prop(config.PropCapellaToken)
	if token == "" {
		return nil, errors.New("Capella API key not set; use --token, set CAPELLA_API_KEY (or put it in .env), or run 'cbctl configure'")
	}
	c := NewClient(token, cfg.Prop(config.PropCapellaAPIHost))
	c.OrganizationName = cfg.Prop(config.PropCapellaOrgName)
	c.OrganizationID = cfg.Prop(config.PropCapellaOrgID)
	if name := cfg.Prop(config.PropCapellaProject); name != "" {
		c.ProjectName = name
	}
	c.ProjectID = cfg.Prop(config.PropCapellaProjectID)
	c.DatabaseName = cfg.Prop(config.PropCapellaDatabase)
	c.DatabaseID = cfg.Prop(config.PropCapellaDatabaseID)
	c.AccountEmail = cfg.Prop(config.PropCapellaUserEmail)
	c.AccountID = cfg.Prop(config.PropCapellaUserID)
	return c, nil
}

func (c *Client) hasOrganizationID() bool   { return c != nil && c.OrganizationID != "" }
func (c *Client) hasOrganizationName() bool { return c != nil && c.OrganizationName != "" }
func (c *Client) hasProjectID() bool        { return c != nil && c.ProjectID != "" }
func (c *Client) hasDatabaseID() bool       { return c != nil && c.DatabaseID != "" }
func (c *Client) hasDatabaseName() bool     { return c != nil && c.DatabaseName != "" }
func (c *Client) hasAccountEmail() bool     { return c != nil && c.AccountEmail != "" }
func (c *Client) hasAccountID() bool        { return c != nil && c.AccountID != "" }

func (c *Client) apiClient() (*capellaapi.Client, error) {
	c.apiOnce.Do(func() {
		opts := []capellaapi.Option{}
		if c.BaseURL != "" {
			opts = append(opts, capellaapi.WithBaseURL(c.BaseURL))
		} else if c.APIHost != "" {
			opts = append(opts, capellaapi.WithBaseURL(c.APIHost))
		}
		if c.OrganizationID != "" {
			opts = append(opts, capellaapi.WithOrgID(c.OrganizationID))
		}
		if c.HTTP != nil {
			opts = append(opts, capellaapi.WithHTTPClient(c.HTTP))
		}
		c.api, c.apiErr = capellaapi.New(c.Token, opts...)
	})
	return c.api, c.apiErr
}

func (c *Client) doJSON(method, path string, body any, dest any) error {
	api, err := c.apiClient()
	if err != nil {
		return err
	}
	if c.OrganizationID != "" && api.OrgID() == "" {
		api.SetOrgID(c.OrganizationID)
	}
	opt := capellaapi.RequestOptions{Body: body}
	resp, err := api.Do(context.Background(), method, path, opt, dest)
	if resp != nil {
		c.lastStatus = resp.Status
		c.lastBody = string(resp.Body)
	}
	if err != nil {
		var apiErr *capellaapi.APIError
		if errors.As(err, &apiErr) {
			msg := "Capella API error"
			if apiErr.Status == http.StatusNotFound {
				msg = "not found"
			}
			return apiError(apiErr.Status, apiErr.Body, msg, err)
		}
		return err
	}
	return nil
}

// GetJSON performs GET and decodes JSON into dest.
func (c *Client) GetJSON(path string, dest any) error {
	return c.doJSON(http.MethodGet, path, nil, dest)
}

// PostJSON performs POST with a JSON body.
func (c *Client) PostJSON(path string, body, dest any) error {
	return c.doJSON(http.MethodPost, path, body, dest)
}

// PutJSON performs PUT with a JSON body.
func (c *Client) PutJSON(path string, body, dest any) error {
	return c.doJSON(http.MethodPut, path, body, dest)
}

// PatchJSON performs PATCH with a JSON body.
func (c *Client) PatchJSON(path string, body, dest any) error {
	return c.doJSON(http.MethodPatch, path, body, dest)
}

// Delete performs DELETE.
func (c *Client) Delete(path string) error {
	return c.doJSON(http.MethodDelete, path, nil, nil)
}

// IsNotFound reports whether err is an HTTP 404 CapellaAPIError.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, capellaapi.ErrNotFound) {
		return true
	}
	var api *CapellaAPIError
	if AsCapellaAPI(err, &api) {
		return api.Code == http.StatusNotFound
	}
	var nf *CapellaNotFoundError
	return AsCapellaNotFound(err, &nf)
}

// AsCapellaAPI extracts *CapellaAPIError.
func AsCapellaAPI(err error, target **CapellaAPIError) bool {
	if err == nil || target == nil {
		return false
	}
	return errors.As(err, target)
}

// AsCapellaNotFound extracts *CapellaNotFoundError.
func AsCapellaNotFound(err error, target **CapellaNotFoundError) bool {
	if err == nil || target == nil {
		return false
	}
	return errors.As(err, target)
}

type pagedResponse struct {
	Data   []json.RawMessage `json:"data"`
	Cursor *struct {
		Pages *struct {
			Page       int `json:"page"`
			PerPage    int `json:"perPage"`
			Last       int `json:"last"`
			TotalItems int `json:"totalItems"`
			Next       any `json:"next"`
		} `json:"pages"`
	} `json:"cursor"`
}

// GetPaged GETs all pages of a Capella list endpoint and returns raw data items.
// A 404 still returns nil, nil (legacy callers rely on this).
func (c *Client) GetPaged(path string, perPage int) ([]json.RawMessage, error) {
	if perPage <= 0 {
		perPage = 50
	}
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	items, err := capellaapi.Paginate[json.RawMessage](context.Background(), api, path, "data", q)
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		// Fall back to previous page/last semantics if Paginate fails on shape — keep
		// compatible path via manual loop using doJSON for older response shapes.
		return c.getPagedLegacy(path, perPage)
	}
	_ = perPage
	return items, nil
}

func (c *Client) getPagedLegacy(path string, perPage int) ([]json.RawMessage, error) {
	var all []json.RawMessage
	page := 1
	for {
		u, err := url.Parse(path)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("page", strconv.Itoa(page))
		q.Set("perPage", strconv.Itoa(perPage))
		u.RawQuery = q.Encode()

		var resp pagedResponse
		if err := c.GetJSON(u.String(), &resp); err != nil {
			if IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		all = append(all, resp.Data...)
		if resp.Cursor == nil || resp.Cursor.Pages == nil {
			break
		}
		pages := resp.Cursor.Pages
		last := pages.Last
		if last <= 0 {
			if pages.TotalItems <= 0 || len(resp.Data) == 0 {
				break
			}
			last = (pages.TotalItems + perPage - 1) / perPage
		}
		if page >= last || len(resp.Data) == 0 {
			break
		}
		page++
	}
	return all, nil
}

// DecodePaged unmarshals paged items into dest slice pointer.
func DecodePaged[T any](items []json.RawMessage) ([]T, error) {
	out := make([]T, 0, len(items))
	for _, raw := range items {
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
