package manifest

import (
	"context"
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nameRe     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	keyspaceRe = regexp.MustCompile(`^[A-Za-z0-9_%-]{1,251}$`)
	bucketRe   = regexp.MustCompile(`^[A-Za-z0-9_%.-]{1,100}$`)
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
)

// DefaultKeyspace is the scope and collection every bucket has; naming it is a no-op.
const DefaultKeyspace = "_default"

// checkClusterName validates metadata.name.
func (c *collector) checkClusterName(name string) {
	switch {
	case name == "":
		c.add("/metadata/name", "name is required", "use kebab case, for example airline-ops")
	case len(name) > 63:
		c.addf("/metadata/name", "name %q is %d characters; the limit is 63", name, len(name))
	case !nameRe.MatchString(name):
		c.addf("/metadata/name", "name %q must be kebab case: lower case letters, digits and '-', starting and ending with a letter or digit", name)
	}
}

// bucketNameIssue returns the naming-rule violation of a bucket name, or "".
func bucketNameIssue(name string) string {
	switch {
	case name == "":
		return "bucket name is required"
	case !bucketRe.MatchString(name) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || strings.HasPrefix(name, "%"):
		return fmt.Sprintf("bucket name %q is not valid: use 1–100 characters of A-Z a-z 0-9 _ - . %%, not starting with . _ or %%", clip(name))
	}
	return ""
}

// keyspaceIssue returns the naming-rule violation of a scope or collection name, or "".
func keyspaceIssue(kind, name string) string {
	if name == DefaultKeyspace {
		return ""
	}
	if name == "" {
		return kind + " name is required"
	}
	if keyspaceRe.MatchString(name) && !strings.ContainsAny(name[:1], "_%") {
		return ""
	}
	return fmt.Sprintf("%s name %q is not valid: use 1–251 characters of A-Z a-z 0-9 _ - %%, not starting with _ or %%", kind, clip(name))
}

func clip(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// splitHostPort splits "host", "host:port", "[v6]:port" or a bare IPv6 address.
func splitHostPort(v string) (host, port string, ok bool) {
	if strings.HasPrefix(v, "[") {
		h, rest, found := strings.Cut(v[1:], "]")
		if !found {
			return "", "", false
		}
		if rest == "" {
			return h, "", true
		}
		return h, strings.TrimPrefix(rest, ":"), strings.HasPrefix(rest, ":")
	}
	if strings.Count(v, ":") > 1 {
		return v, "", true
	}
	h, p, found := strings.Cut(v, ":")
	if !found {
		return v, "", true
	}
	return h, p, true
}

// hostIssue returns what is wrong with a node address (IP or host name, optional port), or "".
func hostIssue(v string) string {
	bad := fmt.Sprintf("%q is not an IP address or host name", clip(v))
	if v == "" || strings.TrimSpace(v) != v || strings.ContainsAny(v, " \t/@?#") {
		return bad
	}
	h, port, ok := splitHostPort(v)
	if !ok {
		return bad
	}
	if port != "" {
		n := 0
		for _, r := range port {
			if r < '0' || r > '9' || n > 65535 {
				n = -1
				break
			}
			n = n*10 + int(r-'0')
		}
		if n <= 0 || n > 65535 {
			return fmt.Sprintf("%q has an invalid port", clip(v))
		}
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return ""
	}
	if len(h) > 253 || !hostRe.MatchString(h) {
		return bad
	}
	return ""
}

// anyIPCIDRs are the networks that allow every address.
var anyIPCIDRs = map[string]bool{"0.0.0.0/0": true, "::/0": true}

// NormalizeCIDR returns the canonical form of a network: a bare IP becomes /32 (or /128). It
// fails when the value is not valid or has host bits set. The rules match capellafile's, which
// this package may not import.
func NormalizeCIDR(value string) (string, error) {
	text := strings.TrimSpace(value)
	if !strings.Contains(text, "/") {
		if strings.Contains(text, ":") {
			text += "/128"
		} else {
			text += "/32"
		}
	}
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return "", fmt.Errorf("%q is not a CIDR or IP address", value)
	}
	if prefix.Masked() != prefix {
		return "", fmt.Errorf("%s has host bits set (did you mean %s?)", value, prefix.Masked())
	}
	return prefix.String(), nil
}

// ImportTarget returns the scope and collection of an entry with the _default defaults applied.
func (i Import) ImportTarget() (scope, collection string) {
	scope, collection = i.Scope, i.Collection
	if scope == "" {
		scope = DefaultKeyspace
	}
	if collection == "" {
		collection = DefaultKeyspace
	}
	return scope, collection
}

// importRules checks one bucket's import list: exactly one of file or data, names, uniqueness
// of the target, and the keyField rules (the file or the inline documents are read here). It
// leaves the kind-specific credential field to the caller.
func (c *collector) importRules(ctx context.Context, baseDir, bucketPtr string, imports []Import) {
	seen := map[[2]string]int{}
	for i, imp := range imports {
		ip := ptr(bucketPtr, "import", i)
		hasFile, hasData := imp.File != "", imp.Data != nil
		switch {
		case hasFile && hasData:
			c.add(ip, "set exactly one of file or data, not both")
		case !hasFile && !hasData:
			c.add(ip, "set exactly one of file or data")
		}
		if imp.Scope != "" {
			if msg := keyspaceIssue("scope", imp.Scope); msg != "" {
				c.add(ptr(ip, "scope"), msg)
			}
		}
		if imp.Collection != "" {
			if msg := keyspaceIssue("collection", imp.Collection); msg != "" {
				c.add(ptr(ip, "collection"), msg)
			}
		}
		s, col := imp.ImportTarget()
		target := [2]string{s, col}
		if first, dup := seen[target]; dup {
			c.addf(ip, "%s.%s is already imported by import[%d]; each scope and collection pair may appear once", s, col, first)
		} else {
			seen[target] = i
		}
		if imp.KeyField != "" && strings.TrimSpace(imp.KeyField) != imp.KeyField {
			c.add(ptr(ip, "keyField"), "keyField must not start or end with white space")
		}
		switch {
		case hasData && !hasFile:
			c.checkInlineData(ptr(ip, "data"), imp.Data)
			if imp.KeyField != "" {
				c.checkInlineKeys(ptr(ip, "data"), imp.Data, imp.KeyField)
			}
		case hasFile && !hasData:
			c.checkImportFile(ctx, ip, imp.File, ImportPath(baseDir, imp.File), imp.KeyField)
		}
	}
}

// ImportPath returns the absolute path of an import file: relative paths resolve from baseDir,
// the manifest's directory (the working directory with -f -).
func ImportPath(baseDir, file string) string {
	if filepath.IsAbs(file) {
		return filepath.Clean(file)
	}
	return filepath.Join(baseDir, filepath.FromSlash(file))
}

// hostKey is the identity of a node address for uniqueness checks: case-insensitive. A port is
// part of the identity, so 10.0.0.1 and 10.0.0.1:8091 differ.
func hostKey(v string) string { return strings.ToLower(v) }
