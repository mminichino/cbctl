package rest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ServerGroupsResponse is the GET /pools/default/serverGroups payload.
type ServerGroupsResponse struct {
	Groups []ServerGroup `json:"groups"`
	Rev    string        `json:"rev"` // may arrive as number; decoded via custom handling below
}

// ServerGroup is one server group entry.
type ServerGroup struct {
	Name  string            `json:"name"`
	URI   string            `json:"uri"`
	Nodes []ServerGroupNode `json:"nodes"`
}

// ServerGroupNode identifies a node inside a server group.
type ServerGroupNode struct {
	OTPNode string `json:"otpNode"`
}

type serverGroupsRaw struct {
	Groups []ServerGroup `json:"groups"`
	Rev    any           `json:"rev"`
}

// GetServerGroups fetches server group configuration and revision.
func (c *Client) GetServerGroups(endpoint Endpoint, username, password string) (*ServerGroupsResponse, error) {
	var raw serverGroupsRaw
	if _, err := c.GetJSON(endpoint, Ptr(username), Ptr(password), "/pools/default/serverGroups", &raw); err != nil {
		return nil, err
	}
	rev := ""
	switch v := raw.Rev.(type) {
	case nil:
		rev = ""
	case string:
		rev = v
	case float64:
		rev = strconv.FormatInt(int64(v), 10)
	default:
		rev = fmt.Sprint(v)
		if rev == "<nil>" {
			rev = ""
		}
	}
	return &ServerGroupsResponse{Groups: raw.Groups, Rev: rev}, nil
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
		path := "/pools/default/serverGroups?rev=" + groups.Rev
		if err := c.PutJSON(endpoint, Ptr(username), Ptr(password), path, payload); err != nil {
			last = err
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
	want := strings.ToLower(nodeHost)
	for _, g := range groups {
		for _, n := range g.Nodes {
			host := otpHost(n.OTPNode)
			if strings.EqualFold(host, want) {
				return n.OTPNode, g.URI, nil
			}
		}
	}
	return "", "", fmt.Errorf("node %s not found in server groups", nodeHost)
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
