package capella

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/config"
)

const organizationsPath = "/v4/organizations"

// Client is a Capella Management API v4 client (Bearer token, HTTPS :443).
type Client struct {
	Token            string
	APIHost          string
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
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// NewClientFromConfig builds a client from config properties.
func NewClientFromConfig(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	token := cfg.Prop(config.PropCapellaToken)
	if token == "" {
		return nil, fmt.Errorf("please set property %s to provide the API v4 token", config.PropCapellaToken)
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
func (c *Client) hasProjectName() bool      { return c != nil && c.ProjectName != "" }
func (c *Client) hasDatabaseID() bool       { return c != nil && c.DatabaseID != "" }
func (c *Client) hasDatabaseName() bool     { return c != nil && c.DatabaseName != "" }
func (c *Client) hasAccountEmail() bool     { return c != nil && c.AccountEmail != "" }
func (c *Client) hasAccountID() bool        { return c != nil && c.AccountID != "" }

func (c *Client) baseURL(path string) string {
	path = "/" + strings.TrimPrefix(path, "/")
	return fmt.Sprintf("https://%s%s", c.APIHost, path)
}

func (c *Client) doJSON(method, path string, body any, dest any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, c.baseURL(path), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	c.lastStatus = resp.StatusCode
	c.lastBody = string(raw)
	if resp.StatusCode == http.StatusNotFound {
		return apiError(resp.StatusCode, strings.TrimSpace(string(raw)), "not found", nil)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, strings.TrimSpace(string(raw)), "Capella API error", nil)
	}
	if dest == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return apiError(resp.StatusCode, strings.TrimSpace(string(raw)), "Capella JSON decode error", err)
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
	var api *CapellaAPIError
	if err == nil {
		return false
	}
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
	for err != nil {
		if e, ok := err.(*CapellaAPIError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// AsCapellaNotFound extracts *CapellaNotFoundError.
func AsCapellaNotFound(err error, target **CapellaNotFoundError) bool {
	if err == nil || target == nil {
		return false
	}
	for err != nil {
		if e, ok := err.(*CapellaNotFoundError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

type pagedResponse struct {
	Data   []json.RawMessage `json:"data"`
	Cursor *struct {
		Pages *struct {
			Page       int `json:"page"`
			PerPage    int `json:"perPage"`
			Last       int `json:"last"`
			TotalItems int `json:"totalItems"`
		} `json:"pages"`
	} `json:"cursor"`
}

// GetPaged GETs all pages of a Capella list endpoint and returns raw data items.
func (c *Client) GetPaged(path string, perPage int) ([]json.RawMessage, error) {
	if perPage <= 0 {
		perPage = 50
	}
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
