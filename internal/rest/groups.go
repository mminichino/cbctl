package rest

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ServerGroupsResponse is the GET /pools/default/serverGroups payload.
// Couchbase puts the configuration revision in the top-level uri
// ("/pools/default/serverGroups?rev=…"), not a rev field.
type ServerGroupsResponse struct {
	Groups []ServerGroup `json:"groups"`
	Rev    string        `json:"rev,omitempty"`
	URI    string        `json:"uri,omitempty"`
}

// ServerGroup is one server group entry.
type ServerGroup struct {
	Name  string            `json:"name"`
	URI   string            `json:"uri"`
	Nodes []ServerGroupNode `json:"nodes"`
}

// ServerGroupNode identifies a node inside a server group.
type ServerGroupNode struct {
	OTPNode  string `json:"otpNode"`
	Hostname string `json:"hostname,omitempty"`
}

type serverGroupsRaw struct {
	Groups []ServerGroup `json:"groups"`
	Rev    any           `json:"rev"`
	URI    string        `json:"uri"`
}

// GetServerGroups fetches server group configuration and revision.
func (c *Client) GetServerGroups(endpoint Endpoint, username, password string) (*ServerGroupsResponse, error) {
	var raw serverGroupsRaw
	if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default/serverGroups", &raw); err != nil {
		return nil, err
	}
	rev := serverGroupRevision(raw.Rev, raw.URI)
	return &ServerGroupsResponse{Groups: raw.Groups, Rev: rev, URI: raw.URI}, nil
}

func formatRev(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		rev := strings.TrimSpace(fmt.Sprint(v))
		if rev == "<nil>" {
			return ""
		}
		return rev
	}
}

// serverGroupRevision reads the revision from a rev field or from the uri query.
func serverGroupRevision(rev any, uri string) string {
	if s := formatRev(rev); s != "" {
		return s
	}
	u, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("rev"))
}

// serverGroupsUpdatePath is the PUT path for a membership change.
// Prefer the uri returned by GET, which already includes the current rev.
func serverGroupsUpdatePath(uri, rev string) string {
	if path := strings.TrimSpace(uri); path != "" {
		u, err := url.Parse(path)
		if err == nil && strings.TrimSpace(u.Query().Get("rev")) != "" {
			p := u.EscapedPath()
			if p == "" {
				p = u.Path
			}
			if p == "" {
				p = "/pools/default/serverGroups"
			}
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			return p + "?" + u.RawQuery
		}
	}
	rev = strings.TrimSpace(rev)
	if rev == "" {
		return ""
	}
	return "/pools/default/serverGroups?rev=" + url.QueryEscape(rev)
}

// CreateServerGroup creates a named server group.
func (c *Client) CreateServerGroup(endpoint Endpoint, username, password, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	return c.PostForm(endpoint, Ptr(username), Ptr(password), "/pools/default/serverGroups", map[string]string{
		"name": name,
	})
}

// EnsureServerGroup creates the group when it does not already exist.
func (c *Client) EnsureServerGroup(endpoint Endpoint, username, password, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	groups, err := c.GetServerGroups(endpoint, username, password)
	if err != nil {
		return err
	}
	for _, g := range groups.Groups {
		if g.Name == name {
			return nil
		}
	}
	if err := c.CreateServerGroup(endpoint, username, password, name); err != nil {
		// Concurrent create or already exists.
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			return nil
		}
		return err
	}
	return nil
}

// AssignNodeServerGroup moves nodeHost into serverGroup, creating the group if needed.
// No-op when serverGroup is empty. Retries on revision conflicts.
func (c *Client) AssignNodeServerGroup(endpoint Endpoint, username, password, nodeHost, serverGroup string, retries int) error {
	serverGroup = strings.TrimSpace(serverGroup)
	if serverGroup == "" {
		return nil
	}
	if retries <= 0 {
		retries = 10
	}
	nodeHost, _ = ParseHostPort(nodeHost, 8091)

	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		if err := c.EnsureServerGroup(endpoint, username, password, serverGroup); err != nil {
			return fmt.Errorf("ensure server group %q: %w", serverGroup, err)
		}
		groups, err := c.GetServerGroups(endpoint, username, password)
		if err != nil {
			return err
		}
		otp, fromURI, err := findNodeInGroups(groups.Groups, nodeHost)
		if err != nil {
			last = err
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		if groupNameByURI(groups.Groups, fromURI) == serverGroup {
			return nil
		}
		toURI := ""
		for _, g := range groups.Groups {
			if g.Name == serverGroup {
				toURI = g.URI
				break
			}
		}
		if toURI == "" {
			last = fmt.Errorf("server group %q not found after create", serverGroup)
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}

		payload := map[string]any{"groups": rebuildGroupsMovingNode(groups.Groups, otp, fromURI, toURI)}
		path := serverGroupsUpdatePath(groups.URI, groups.Rev)
		if path == "" {
			last = fmt.Errorf("server group revision missing")
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		if err := c.PutJSON(endpoint, Ptr(username), Ptr(password), path, payload); err != nil {
			last = err
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		confirmed, err := c.GetServerGroups(endpoint, username, password)
		if err != nil {
			last = err
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		_, confirmedURI, err := findNodeInGroups(confirmed.Groups, nodeHost)
		if err != nil {
			last = err
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		if groupNameByURI(confirmed.Groups, confirmedURI) != serverGroup {
			last = fmt.Errorf("node %s still in group %q", nodeHost, groupNameByURI(confirmed.Groups, confirmedURI))
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		return nil
	}
	if last == nil {
		last = fmt.Errorf("failed to assign server group")
	}
	return fmt.Errorf("assign server group %q: %w", serverGroup, last)
}

func findNodeInGroups(groups []ServerGroup, nodeHost string) (otpNode, groupURI string, err error) {
	want := strings.ToLower(strings.TrimSpace(nodeHost))
	for _, g := range groups {
		for _, n := range g.Nodes {
			if nodeMatchesHost(n, want) {
				return n.OTPNode, g.URI, nil
			}
		}
	}
	return "", "", fmt.Errorf("node %s not found in server groups", nodeHost)
}

func nodeMatchesHost(n ServerGroupNode, want string) bool {
	if want == "" {
		return false
	}
	for _, candidate := range []string{n.OTPNode, n.Hostname} {
		host := otpHost(candidate)
		if host != "" && strings.EqualFold(host, want) {
			return true
		}
	}
	return false
}

func groupNameByURI(groups []ServerGroup, uri string) string {
	for _, g := range groups {
		if g.URI == uri {
			return g.Name
		}
	}
	return ""
}

func otpHost(otp string) string {
	otp = strings.TrimSpace(otp)
	if otp == "" {
		return ""
	}
	if _, host, ok := strings.Cut(otp, "@"); ok {
		h, _ := ParseHostPort(host, 8091)
		return h
	}
	h, _ := ParseHostPort(otp, 8091)
	return h
}

func rebuildGroupsMovingNode(groups []ServerGroup, otpNode, fromURI, toURI string) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		nodes := make([]map[string]string, 0, len(g.Nodes))
		for _, n := range g.Nodes {
			if n.OTPNode == otpNode && g.URI == fromURI {
				continue
			}
			if n.OTPNode == otpNode {
				continue
			}
			nodes = append(nodes, map[string]string{"otpNode": n.OTPNode})
		}
		if g.URI == toURI {
			nodes = append(nodes, map[string]string{"otpNode": otpNode})
		}
		out = append(out, map[string]any{
			"name":  g.Name,
			"uri":   g.URI,
			"nodes": nodes,
		})
	}
	return out
}
