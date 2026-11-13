package cli

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

// maxAllowedCIDRs is how many allowed CIDRs Capella accepts per cluster.
const maxAllowedCIDRs = 75

func newCapellaCIDRCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cidr",
		Short: "Capella allowed CIDR operations",
		Long: `Manage the IP ranges allowed to connect to a cluster.

Capella cannot change an allowed CIDR in place: to change its comment or expiry, delete it and
create it again.`,
		Run: func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCapellaCIDRListCmd())
	cmd.AddCommand(newCapellaCIDRCreateCmd())
	cmd.AddCommand(newCapellaCIDRDeleteCmd())
	return cmd
}

// parseCIDR validates value as an IP range and returns its canonical form. A bare address becomes
// a single-host range (/32 or /128). Addresses with host bits set are rejected.
func parseCIDR(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", usageError("give an address and prefix length, for example 203.0.113.10/32", "CIDR must not be empty")
	}
	if !strings.Contains(text, "/") {
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return "", usageError("give an address and prefix length, for example 203.0.113.10/32", "%s is not a valid CIDR", value)
		}
		return netip.PrefixFrom(addr, addr.BitLen()).String(), nil
	}
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return "", usageError("give an address and prefix length, for example 203.0.113.10/32", "%s is not a valid CIDR", value)
	}
	if masked := prefix.Masked(); masked != prefix {
		return "", usageError(fmt.Sprintf("use %s instead: the bits after the prefix length must be zero", masked),
			"%s is not a network address (did you mean %s?)", value, masked)
	}
	return prefix.String(), nil
}

// canonicalCIDR normalizes s for comparison; text that does not parse is returned trimmed.
func canonicalCIDR(s string) string {
	if c, err := parseCIDR(s); err == nil {
		return c
	}
	return strings.TrimSpace(s)
}

func newCapellaCIDRListCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list CLUSTER",
		Short: "List the allowed CIDRs of a cluster",
		Args:  cobra.ExactArgs(1),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		pid, cid := t.ids()
		items, err := rt.Client.ListAllowedCIDRs(rt.Ctx, pid, cid)
		if err != nil {
			return fmt.Errorf("listing allowed CIDRs of %s: %w", t.Cluster.Name, err)
		}
		if rt.JSON {
			rt.Out.List(items)
			return nil
		}
		rows := make([][]string, 0, len(items))
		for _, c := range items {
			expires := c.ExpiresAt
			if expires == "" {
				expires = "never"
			}
			rows = append(rows, []string{c.CIDR, c.Type, c.Status, expires, c.Comment, c.ID})
		}
		rt.Out.Table([]string{"CIDR", "TYPE", "STATUS", "EXPIRES", "COMMENT", "ID"}, rows)
		return nil
	}
	return cmd
}

// cidrDoc is the JSON document of cidr create and delete.
type cidrDoc struct {
	ID     string `json:"id,omitempty"`
	CIDR   string `json:"cidr"`
	Status string `json:"status"`
}

func newCapellaCIDRCreateCmd() *cobra.Command {
	var (
		project, comment, expiresAt string
		allowAny                    bool
	)
	cmd := &cobra.Command{
		Use:   "create CLUSTER CIDR",
		Short: "Allow an IP range to connect to a cluster",
		Long: `Allow an IP range to connect to a cluster. A bare address is allowed as a single host.
An entry that is already allowed is left alone.

Examples:
  cbctl capella cidr create my-cluster 203.0.113.10/32 --comment "Office VPN"
  cbctl capella cidr create my-cluster 198.51.100.0/24 --expires-at 2027-12-31T23:59:59Z
  cbctl capella cidr create my-cluster 203.0.113.10`,
		Args: cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.Flags().StringVar(&comment, "comment", "", "Short description, for example who uses the range")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "Expiry time (RFC 3339, for example 2027-12-31T23:59:59Z); default permanent")
	cmd.Flags().BoolVar(&allowAny, "allow-any-ip", false, "Required to allow 0.0.0.0/0")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cidr, err := parseCIDR(args[1])
		if err != nil {
			return err
		}
		if (cidr == "0.0.0.0/0" || cidr == "::/0") && !allowAny {
			return usageError("pass --allow-any-ip if you really mean it", "%s would allow every IP address", cidr)
		}
		var expiry time.Time
		if expiresAt != "" {
			if expiry, err = time.Parse(time.RFC3339, expiresAt); err != nil {
				return usageError("use RFC 3339, for example 2027-12-31T23:59:59Z", "invalid --expires-at %q", expiresAt)
			}
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		if expiresAt != "" && !expiry.After(rt.Clock.Now()) {
			return usageError("pass a time in the future", "--expires-at %s is not in the future", expiresAt)
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		pid, cid := t.ids()

		existing, err := rt.Client.ListAllowedCIDRs(rt.Ctx, pid, cid)
		if err != nil {
			return fmt.Errorf("listing allowed CIDRs of %s: %w", t.Cluster.Name, err)
		}
		if e := findCIDR(existing, cidr); e != nil {
			rt.report(cidrDoc{ID: e.ID, CIDR: cidr, Status: "already allowed"}, "%s already allowed on %s", cidr, t.Cluster.Name)
			return nil
		}
		if len(existing) >= maxAllowedCIDRs {
			return Errorf(ExitBlocked, "delete an unused entry with 'cbctl capella cidr delete'",
				"cluster %s already has the maximum of %d allowed CIDRs", t.Cluster.Name, maxAllowedCIDRs)
		}

		id, err := rt.Client.CreateAllowedCIDR(rt.Ctx, pid, cid, cidr, comment, expiresAt)
		switch {
		case err == nil:
		case errors.Is(err, capellaapi.ErrConflict):
			rt.report(cidrDoc{CIDR: cidr, Status: "already allowed"}, "%s already allowed on %s", cidr, t.Cluster.Name)
			return nil
		case isAmbiguous(err):
			// Re-observe by CIDR once: the request may have been applied.
			after, lerr := rt.Client.ListAllowedCIDRs(rt.Ctx, pid, cid)
			e := findCIDR(after, cidr)
			if lerr != nil || e == nil {
				return err
			}
			id = e.ID
		default:
			return fmt.Errorf("allowing %s on %s: %w", cidr, t.Cluster.Name, err)
		}
		rt.report(cidrDoc{ID: id, CIDR: cidr, Status: "created"}, "allowed %s on %s", cidr, t.Cluster.Name)
		return nil
	}
	return cmd
}

// findCIDR returns the entry whose CIDR equals the canonical cidr.
func findCIDR(items []capellaapi.AllowedCIDR, cidr string) *capellaapi.AllowedCIDR {
	for i := range items {
		if canonicalCIDR(items[i].CIDR) == cidr {
			return &items[i]
		}
	}
	return nil
}

func newCapellaCIDRDeleteCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "delete CLUSTER CIDR",
		Short: "Stop allowing an IP range",
		Args:  cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cidr, err := parseCIDR(args[1])
		if err != nil {
			return err
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		pid, cid := t.ids()
		items, err := rt.Client.ListAllowedCIDRs(rt.Ctx, pid, cid)
		if err != nil {
			return fmt.Errorf("listing allowed CIDRs of %s: %w", t.Cluster.Name, err)
		}
		var found []capellaapi.AllowedCIDR
		for _, e := range items {
			if canonicalCIDR(e.CIDR) == cidr {
				found = append(found, e)
			}
		}
		if len(found) == 0 {
			logging.Warning("allowed CIDR %s on %s not found", cidr, t.Cluster.Name)
			rt.report(cidrDoc{CIDR: cidr, Status: "not found"}, "not found: nothing to delete")
			return nil
		}
		question := fmt.Sprintf("Connections from %s to %s will be refused. Delete it?", cidr, t.Cluster.Name)
		if ok, err := rt.confirmDelete(question); err != nil || !ok {
			return err
		}
		for _, e := range found {
			if err := rt.Client.DeleteAllowedCIDR(rt.Ctx, pid, cid, e.ID); err != nil && !errors.Is(err, capellaapi.ErrNotFound) {
				return fmt.Errorf("deleting allowed CIDR %s: %w", cidr, err)
			}
		}
		rt.report(cidrDoc{ID: found[0].ID, CIDR: cidr, Status: "deleted"}, "deleted allowed CIDR %s from %s", cidr, t.Cluster.Name)
		return nil
	}
	return cmd
}
