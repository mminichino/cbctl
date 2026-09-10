package rest

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoint is a Couchbase management/query REST target.
type Endpoint struct {
	Host      string
	AdminPort int
	QueryPort int
	UseSSL    bool
}

// ForServer builds an endpoint for Couchbase Server.
// host may be "hostname" or "hostname:port" (admin/management port).
func ForServer(host string, useSSL bool) Endpoint {
	defaultAdmin := 8091
	defaultQuery := 8093
	if useSSL {
		defaultAdmin = 18091
		defaultQuery = 18093
	}
	h, adminPort := ParseHostPort(host, defaultAdmin)
	return Endpoint{Host: h, AdminPort: adminPort, QueryPort: defaultQuery, UseSSL: useSSL}
}

// ForCapella builds an endpoint for Capella cluster admin ports.
func ForCapella(host string) Endpoint {
	h, _ := ParseHostPort(host, 18091)
	return Endpoint{Host: h, AdminPort: 18091, QueryPort: 18093, UseSSL: true}
}

func (e Endpoint) scheme() string {
	if e.UseSSL {
		return "https"
	}
	return "http"
}

func (e Endpoint) adminURL(path string) string {
	return fmt.Sprintf("%s://%s:%d%s", e.scheme(), e.Host, e.AdminPort, path)
}

func (e Endpoint) queryURL(path string) string {
	return fmt.Sprintf("%s://%s:%d%s", e.scheme(), e.Host, e.QueryPort, path)
}

// Client performs form/JSON REST calls against a cluster endpoint.
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// NewClient returns a REST client that skips TLS verification (Server default).
func NewClient() *Client {
	return &Client{
		Timeout: 30 * time.Second,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
	}
}

// DoForm posts or puts application/x-www-form-urlencoded data.
func (c *Client) DoForm(method string, endpoint Endpoint, username, password *string, path string, fields map[string]string) ([]byte, int, error) {
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req, err := http.NewRequest(method, endpoint.adminURL(path), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if username != nil {
		req.SetBasicAuth(*username, "")
		if password != nil {
			req.SetBasicAuth(*username, *password)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, resp.StatusCode, nil
}

// PostForm posts form fields to the admin port.
func (c *Client) PostForm(endpoint Endpoint, username, password *string, path string, fields map[string]string) error {
	_, _, err := c.DoForm(http.MethodPost, endpoint, username, password, path, fields)
	return err
}

// PutForm puts form fields to the admin port.
func (c *Client) PutForm(endpoint Endpoint, username, password *string, path string, fields map[string]string) error {
	_, _, err := c.DoForm(http.MethodPut, endpoint, username, password, path, fields)
	return err
}

// DoJSON sends a JSON body with the given HTTP method.
func (c *Client) DoJSON(method string, endpoint Endpoint, username, password *string, path string, payload any) ([]byte, int, error) {
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = strings.NewReader(string(data))
	}
	req, err := http.NewRequest(method, endpoint.adminURL(path), bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if username != nil {
		pass := ""
		if password != nil {
			pass = *password
		}
		req.SetBasicAuth(*username, pass)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, resp.StatusCode, nil
}

// PutJSON puts a JSON payload to the admin port.
func (c *Client) PutJSON(endpoint Endpoint, username, password *string, path string, payload any) error {
	_, _, err := c.DoJSON(http.MethodPut, endpoint, username, password, path, payload)
	return err
}

// GetJSON GETs a path and decodes JSON.
func (c *Client) GetJSON(endpoint Endpoint, username, password *string, path string, dest any) (int, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint.adminURL(path), nil)
	if err != nil {
		return 0, err
	}
	if username != nil {
		pass := ""
		if password != nil {
			pass = *password
		}
		req.SetBasicAuth(*username, pass)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if dest == nil {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

// PostQueryForm posts a N1QL statement to the query service.
func (c *Client) PostQueryForm(endpoint Endpoint, username, password *string, statement string) error {
	form := url.Values{}
	form.Set("statement", statement)
	req, err := http.NewRequest(http.MethodPost, endpoint.queryURL("/query/service"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if username != nil {
		pass := ""
		if password != nil {
			pass = *password
		}
		req.SetBasicAuth(*username, pass)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Ptr returns a pointer to s.
func Ptr(s string) *string { return &s }
