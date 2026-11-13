package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	goruntime "runtime"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/secrets"
	"github.com/spf13/cobra"
)

// generatedPasswordLength is the length of passwords cbctl generates for credentials.
const generatedPasswordLength = 32

// privilegeSets maps --privileges values to Capella privilege names.
var privilegeSets = map[string][]string{
	"read":       {"data_reader"},
	"write":      {"data_writer"},
	"read+write": {"data_reader", "data_writer"},
}

func newCapellaCredentialCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "credential",
		Short: "Capella database credential operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCapellaCredentialListCmd())
	cmd.AddCommand(newCapellaCredentialGetCmd())
	cmd.AddCommand(newCapellaCredentialCreateCmd())
	cmd.AddCommand(newCapellaCredentialUpdateCmd())
	cmd.AddCommand(newCapellaCredentialDeleteCmd())
	return cmd
}

// accessFlags are the flags that describe one access grant.
type accessFlags struct {
	privileges  string
	bucket      string
	scope       string
	collections []string
	allBuckets  bool
}

// passwordFlags are the flags that choose where a password comes from.
type passwordFlags struct {
	env  string
	file string
}

const passwordRulesHelp = "Password rules: at least 8 characters with an upper-case letter, a lower-case letter, a digit and a " +
	"special character, and none of < > ; . * & | £. Without --password-env or --password-file cbctl generates a " +
	"32-character password and prints it once."

func (a *accessFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&a.privileges, "privileges", "", "Access level: read, write or read+write")
	cmd.Flags().StringVar(&a.bucket, "bucket", "", "Bucket the access applies to (BUCKET.*.*)")
	cmd.Flags().StringVar(&a.scope, "scope", "", "Scope within --bucket (BUCKET.SCOPE.*)")
	cmd.Flags().StringArrayVar(&a.collections, "collection", nil, "Collection within --scope (repeatable)")
	cmd.Flags().BoolVar(&a.allBuckets, "all-buckets", false, "Grant the access on every bucket of the cluster")
}

func (p *passwordFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&p.env, "password-env", "", "Read the password from this environment variable")
	cmd.Flags().StringVar(&p.file, "password-file", "", "Read the password from this file")
}

// given reports whether any access flag was used.
func (a *accessFlags) given() bool {
	return a.privileges != "" || a.bucket != "" || a.scope != "" || len(a.collections) > 0 || a.allBuckets
}

// grant builds the single access entry the flags describe.
func (a *accessFlags) grant() (capellaapi.AccessEntry, error) {
	privs, ok := privilegeSets[a.privileges]
	if !ok {
		if a.privileges == "" {
			return capellaapi.AccessEntry{}, usageError("pass --privileges read, write or read+write", "--privileges is required")
		}
		return capellaapi.AccessEntry{}, usageError("use read, write or read+write", "invalid --privileges %q", a.privileges)
	}
	switch {
	case a.scope != "" && a.bucket == "":
		return capellaapi.AccessEntry{}, usageError("add --bucket with the bucket the scope is in", "--scope was given without --bucket")
	case len(a.collections) > 0 && a.scope == "":
		return capellaapi.AccessEntry{}, usageError("add --scope with the scope the collection is in", "--collection was given without --scope")
	case a.bucket != "" && a.allBuckets:
		return capellaapi.AccessEntry{}, usageError("keep one of them", "both --bucket and --all-buckets were given")
	case a.bucket == "" && !a.allBuckets:
		return capellaapi.AccessEntry{}, usageError("add --bucket NAME (least privilege) or --all-buckets", "no bucket was given for the access")
	}
	entry := capellaapi.AccessEntry{Privileges: append([]string(nil), privs...)}
	if a.bucket != "" {
		b := capellaapi.ResourceBucket{Name: a.bucket}
		if a.scope != "" {
			b.Scopes = []capellaapi.ResourceScope{{Name: a.scope, Collections: append([]string(nil), a.collections...)}}
		}
		entry.Resources = capellaapi.Resources{Buckets: []capellaapi.ResourceBucket{b}}
	}
	return entry, nil
}

// supplied returns the password given with --password-env or --password-file, or "" when neither
// was used. A supplied password must meet Capella's rules.
func (p *passwordFlags) supplied() (string, error) {
	if p.env != "" && p.file != "" {
		return "", usageError("keep one of them", "both --password-env and --password-file were given")
	}
	var pw string
	switch {
	case p.env != "":
		pw = os.Getenv(p.env)
		if pw == "" {
			return "", usageError("export the variable before running the command", "environment variable %s is empty", p.env)
		}
	case p.file != "":
		raw, err := os.ReadFile(p.file)
		if err != nil {
			return "", usageError("check the path", "reading --password-file: %v", err)
		}
		if goruntime.GOOS != "windows" {
			if info, err := os.Stat(p.file); err == nil && info.Mode().Perm()&0o077 != 0 {
				logging.Warning("password file %s is readable by other users; run chmod 600 on it", p.file)
			}
		}
		pw = strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		if pw == "" {
			return "", usageError("write the password into the file", "password file %s is empty", p.file)
		}
	default:
		return "", nil
	}
	if !secrets.MeetsRules(pw) {
		return "", usageError(secrets.Rules, "the password does not meet Capella's rules")
	}
	return pw, nil
}

// privilegeLabel shortens a privilege set to read, write or read+write; other names are kept.
func privilegeLabel(privs []string) string {
	var read, write bool
	var other []string
	for _, p := range privs {
		switch p {
		case "data_reader":
			read = true
		case "data_writer":
			write = true
		default:
			other = append(other, p)
		}
	}
	var parts []string
	switch {
	case read && write:
		parts = append(parts, "read+write")
	case read:
		parts = append(parts, "read")
	case write:
		parts = append(parts, "write")
	}
	sort.Strings(other)
	return strings.Join(append(parts, other...), "+")
}

// resourceText renders the resources of one grant: "*" for every bucket, otherwise
// bucket.* / bucket.scope.* / bucket.scope.collection entries joined with commas.
func resourceText(r capellaapi.Resources) string {
	if len(r.Buckets) == 0 {
		return "*"
	}
	var out []string
	for _, b := range r.Buckets {
		if len(b.Scopes) == 0 {
			out = append(out, b.Name+".*")
			continue
		}
		for _, s := range b.Scopes {
			if len(s.Collections) == 0 {
				out = append(out, b.Name+"."+s.Name+".*")
				continue
			}
			for _, c := range s.Collections {
				out = append(out, b.Name+"."+s.Name+"."+c)
			}
		}
	}
	return strings.Join(out, ",")
}

// accessSummary renders credential access as "read:travel-sample.*; write:*".
func accessSummary(access []capellaapi.AccessEntry) string {
	parts := make([]string, 0, len(access))
	for _, e := range access {
		parts = append(parts, privilegeLabel(e.Privileges)+":"+resourceText(e.Resources))
	}
	return strings.Join(parts, "; ")
}

// accessKey is a canonical string for one grant, for comparing access lists.
func accessKey(e capellaapi.AccessEntry) string {
	privs := append([]string(nil), e.Privileges...)
	sort.Strings(privs)
	e.Privileges = privs
	raw, _ := json.Marshal(e) // plain structs of strings: cannot fail
	return string(raw)
}

// sameAccess reports whether two access lists hold the same grants, ignoring order.
func sameAccess(a, b []capellaapi.AccessEntry) bool {
	if len(a) != len(b) {
		return false
	}
	keys := make(map[string]int, len(a))
	for _, e := range a {
		keys[accessKey(e)]++
	}
	for _, e := range b {
		keys[accessKey(e)]--
	}
	for _, n := range keys {
		if n != 0 {
			return false
		}
	}
	return true
}

// findCredential returns the credential with this exact name from the cluster.
func (r *runtime) findCredential(t *clusterTarget, name string) (*capellaapi.Credential, error) {
	pid, cid := t.ids()
	items, err := r.Client.ListCredentials(r.Ctx, pid, cid)
	if err != nil {
		return nil, fmt.Errorf("listing credentials of %s: %w", t.Cluster.Name, err)
	}
	for i := range items {
		if items[i].Name == name {
			return &items[i], nil
		}
	}
	return nil, nil
}

// requireCredential is findCredential that fails with a usage error when the name is unknown.
func (r *runtime) requireCredential(t *clusterTarget, name string) (*capellaapi.Credential, error) {
	c, err := r.findCredential(t, name)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, usageError(fmt.Sprintf("check the name with 'cbctl capella credential list %s'", t.Cluster.Name),
			"credential %s not found", name)
	}
	return c, nil
}

// credentialDoc is the JSON document of credential commands. The password is added separately by
// printSecretOnce and never sits in this struct.
type credentialDoc struct {
	ID     string                   `json:"id,omitempty"`
	Name   string                   `json:"name"`
	Status string                   `json:"status,omitempty"`
	Access []capellaapi.AccessEntry `json:"access,omitempty"`
}

func newCapellaCredentialListCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list CLUSTER",
		Short: "List the database credentials of a cluster",
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
		items, err := rt.Client.ListCredentials(rt.Ctx, pid, cid)
		if err != nil {
			return fmt.Errorf("listing credentials of %s: %w", t.Cluster.Name, err)
		}
		if rt.JSON {
			rt.Out.List(items)
			return nil
		}
		rows := make([][]string, 0, len(items))
		for _, c := range items {
			rows = append(rows, []string{c.Name, c.ID, accessSummary(c.Access)})
		}
		rt.Out.Table([]string{"NAME", "ID", "ACCESS"}, rows)
		return nil
	}
	return cmd
}

func newCapellaCredentialGetCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "get CLUSTER NAME",
		Short: "Show one database credential (the password is never shown)",
		Args:  cobra.ExactArgs(2),
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
		found, err := rt.requireCredential(t, args[1])
		if err != nil {
			return err
		}
		pid, cid := t.ids()
		cred, _, err := rt.Client.GetCredential(rt.Ctx, pid, cid, found.ID)
		if err != nil {
			return fmt.Errorf("reading credential %s: %w", args[1], err)
		}
		rt.Out.Object(cred)
		return nil
	}
	return cmd
}

func newCapellaCredentialCreateCmd() *cobra.Command {
	var (
		project string
		access  accessFlags
		pw      passwordFlags
	)
	cmd := &cobra.Command{
		Use:   "create CLUSTER NAME",
		Short: "Create a database credential",
		Long: `Create a database credential.

Access: --bucket B grants B.*.*, add --scope S for B.S.*, add --collection C (repeatable) for
B.S.C. --all-buckets grants the access on every bucket.

` + passwordRulesHelp + `

Examples:
  cbctl capella credential create my-cluster app_ro --privileges read --bucket travel-sample
  cbctl capella credential create my-cluster app_rw --privileges read+write --bucket travel-sample --scope inventory --collection airline --collection route
  cbctl capella credential create my-cluster dba --privileges read+write --all-buckets
  cbctl capella credential create my-cluster app_ro --privileges read --bucket orders --password-env APP_PW`,
		Args: cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	access.add(cmd)
	pw.add(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		grant, err := access.grant()
		if err != nil {
			return err
		}
		password, err := pw.supplied()
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
		name := args[1]
		pid, cid := t.ids()
		wanted := []capellaapi.AccessEntry{grant}

		existing, err := rt.findCredential(t, name)
		if err != nil {
			return err
		}
		if existing != nil {
			return rt.credentialExists(t, existing, wanted)
		}

		generated := password == ""
		if generated {
			password = secrets.GeneratePassword(generatedPasswordLength)
		}
		id, _, err := rt.Client.CreateCredential(rt.Ctx, pid, cid, capellaapi.CreateCredentialRequest{
			Name: name, Password: password, Access: wanted,
		})
		switch {
		case err == nil:
		case errors.Is(err, capellaapi.ErrConflict):
			rt.report(credentialDoc{Name: name, Status: "already exists"}, "credential %s already exists", name)
			return nil
		case isAmbiguous(err):
			// Re-observe by name: the request may have been applied, but then the password is lost.
			again, lerr := rt.findCredential(t, name)
			if lerr != nil || again == nil {
				return err
			}
			logging.Warning("credential %s exists but its password was not received; set a new one with 'cbctl capella credential update %s %s --rotate-password'",
				name, t.Cluster.Name, name)
			rt.report(credentialDoc{ID: again.ID, Name: name, Status: "exists"}, "credential %s exists", name)
			return nil
		default:
			return fmt.Errorf("creating credential %s: %w", name, err)
		}

		doc := credentialDoc{ID: id, Name: name, Status: "created", Access: wanted}
		if !generated {
			rt.report(doc, "created credential %s (%s)", name, id)
			return nil
		}
		rt.showGenerated(doc, password, "created credential %s (%s)", name, id)
		return nil
	}
	return cmd
}

// credentialExists answers a create for a name that is already used: the same access is a no-op,
// different access is an error that points at update.
func (r *runtime) credentialExists(t *clusterTarget, existing *capellaapi.Credential, wanted []capellaapi.AccessEntry) error {
	if !sameAccess(existing.Access, wanted) {
		return Errorf(ExitError, fmt.Sprintf("change it with 'cbctl capella credential update %s %s'", t.Cluster.Name, existing.Name),
			"credential %s already exists with different access", existing.Name)
	}
	r.report(credentialDoc{ID: existing.ID, Name: existing.Name, Status: "already exists"}, "credential %s already exists", existing.Name)
	return nil
}

// showGenerated prints the result of a command that generated a password: the result line (text)
// or document (JSON), plus the password exactly once.
func (r *runtime) showGenerated(doc credentialDoc, password, format string, a ...any) {
	if r.JSON {
		printSecretOnce(r, doc, "password", password)
		return
	}
	r.Out.Line(format, a...)
	printSecretOnce(r, nil, "password", password)
}

func newCapellaCredentialUpdateCmd() *cobra.Command {
	var (
		project string
		access  accessFlags
		pw      passwordFlags
		rotate  bool
	)
	cmd := &cobra.Command{
		Use:   "update CLUSTER NAME",
		Short: "Change a database credential's access or password",
		Long: `Change a database credential's access or password.

Access flags replace the credential's access with one grant. --rotate-password sets a new password:
the one from --password-env or --password-file, otherwise a generated one that is printed once.

` + passwordRulesHelp + `

Examples:
  cbctl capella credential update my-cluster app_ro --privileges read --bucket orders
  cbctl capella credential update my-cluster app_ro --rotate-password`,
		Args: cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	access.add(cmd)
	pw.add(cmd)
	cmd.Flags().BoolVar(&rotate, "rotate-password", false, "Set a new password")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		password, err := pw.supplied()
		if err != nil {
			return err
		}
		changePassword := rotate || password != ""
		if !access.given() && !changePassword {
			return usageError("pass access flags, --rotate-password, --password-env or --password-file", "nothing to update")
		}
		var grant *capellaapi.AccessEntry
		if access.given() {
			g, err := access.grant()
			if err != nil {
				return err
			}
			grant = &g
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		name := args[1]
		found, err := rt.requireCredential(t, name)
		if err != nil {
			return err
		}
		generated := false
		if changePassword && password == "" {
			password, generated = secrets.GeneratePassword(generatedPasswordLength), true
		}
		pid, cid := t.ids()

		var final []capellaapi.AccessEntry
		for attempt := 0; ; attempt++ {
			cur, etag, err := rt.Client.GetCredential(rt.Ctx, pid, cid, found.ID)
			if err != nil {
				return fmt.Errorf("reading credential %s: %w", name, err)
			}
			// Capella replaces the whole credential, so unchanged access is sent back as it is.
			final = cur.Access
			if grant != nil {
				final = []capellaapi.AccessEntry{*grant}
			}
			err = rt.Client.UpdateCredential(rt.Ctx, pid, cid, found.ID,
				capellaapi.UpdateCredentialRequest{Password: password, Access: final}, etag)
			if errors.Is(err, capellaapi.ErrPreconditionFailed) && attempt == 0 {
				logging.Debug("credential %s changed while updating; retrying", name)
				continue
			}
			if err != nil {
				return fmt.Errorf("updating credential %s: %w", name, err)
			}
			break
		}

		doc := credentialDoc{ID: found.ID, Name: name, Status: "updated", Access: final}
		if !generated {
			rt.report(doc, "updated credential %s", name)
			return nil
		}
		rt.showGenerated(doc, password, "updated credential %s", name)
		return nil
	}
	return cmd
}

func newCapellaCredentialDeleteCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "delete CLUSTER NAME",
		Short: "Delete a database credential",
		Args:  cobra.ExactArgs(2),
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
		name := args[1]
		found, err := rt.requireCredential(t, name)
		if err != nil {
			return err
		}
		question := fmt.Sprintf("Applications using %s will lose access to %s. Delete it?", name, t.Cluster.Name)
		if ok, err := rt.confirmDelete(question); err != nil || !ok {
			return err
		}
		pid, cid := t.ids()
		if err := rt.Client.DeleteCredential(rt.Ctx, pid, cid, found.ID); err != nil {
			return fmt.Errorf("deleting credential %s: %w", name, err)
		}
		rt.report(credentialDoc{ID: found.ID, Name: name, Status: "deleted"}, "deleted credential %s", name)
		return nil
	}
	return cmd
}
