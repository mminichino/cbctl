package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mminichino/cbctl/internal/secrets"
	"github.com/spf13/cobra"
)

// confirmNamedDelete prints what a bucket, scope or collection delete loses and asks for the
// resource name to be typed. The details are printed with --yes too, so a log shows what went.
// A wrong name cancels: the command is marked cancelled and nothing is deleted.
func (r *runtime) confirmNamedDelete(kind, key, name string, details []string) (bool, error) {
	if !r.JSON {
		for _, l := range details {
			fmt.Fprintf(r.errOut(), "  %s\n", l)
		}
	}
	ok, err := confirmTyped(r, fmt.Sprintf("Type the %s name to confirm deletion of %s", kind, key), name)
	if err != nil {
		return false, err
	}
	if !ok {
		r.markCancelled()
		r.report(statusDoc{Status: "cancelled"}, "cancelled; nothing was deleted")
	}
	return ok, nil
}

// objOfJSON converts an API struct into a generic object, for the resources helpers.
func objOfJSON(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// credentialsDirOf is the directory generated passwords are saved in for this command: the
// credentialsOutput of capella.yaml when there is one, otherwise the default under the cwd.
func credentialsDirOf(cmd *cobra.Command) string {
	base, err := cwdFrom(cmd)
	if err != nil {
		base = "."
	}
	configured := ""
	if disc, derr := discoverConfig(cmd); derr == nil && disc.Exists {
		base = filepath.Dir(disc.Path)
		configured = rawString(disc.Path, "credentialsOutput")
	}
	dir, _, _ := secrets.ResolveCredentialsDir(configured, base)
	return dir
}

// savedSecretPath is the local file of a generated password for the credential, or "".
func savedSecretPath(cmd *cobra.Command, cluster, name string) string {
	p := secrets.SecretPath(credentialsDirOf(cmd), cluster, name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
