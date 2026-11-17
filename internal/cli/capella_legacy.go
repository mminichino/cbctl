package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// legacyCommand is a pre-port cbctl Capella command kept for compatibility. It is hidden from
// help and warns on every use, naming the command that replaces it.
type legacyCommand struct {
	Path        []string
	Replacement string
}

// legacyCommands are the deprecated commands under "cbctl capella". The legacy direct path of
// "cluster create" is listed too: the command itself stays visible, but its legacy flags warn.
var legacyCommands = []legacyCommand{
	{Path: []string{"cluster", "create"}, Replacement: "cbctl capella cluster create NAME [--size ...] [--group ...], then cbctl capella cidr create and cbctl capella credential create"},
	{Path: []string{"cluster", "exists"}, Replacement: "cbctl capella cluster get CLUSTER"},
	{Path: []string{"cluster", "destroy"}, Replacement: "cbctl capella cluster delete CLUSTER -y"},
	{Path: []string{"cluster", "test"}, Replacement: "cbctl capella credential create CLUSTER NAME ..., then cbctl cluster test --host CONNECTION-STRING --ssl --bucket BUCKET -u NAME -p ..."},
	{Path: []string{"bucket", "create"}, Replacement: "cbctl capella bucket add CLUSTER NAME [--memory MB] [--replicas N]"},
	{Path: []string{"bucket", "exists"}, Replacement: "cbctl capella bucket get CLUSTER NAME"},
}

// legacyFlagNames are the flags of the pre-port direct commands. Using one on "cluster create"
// selects the legacy path.
var legacyFlagNames = []string{"database", "database-id", "org", "project-id", "user-email", "user-id",
	"allow-cidr", "allow-any-ip", "username", "password", "password-env", "password-file"}

// replacementFor returns the replacement of a legacy command path such as "cluster exists".
func replacementFor(path string) string {
	for _, l := range legacyCommands {
		if strings.Join(l.Path, " ") == path {
			return l.Replacement
		}
	}
	return ""
}

// installLegacy hides and wraps the legacy commands under capella. A path that does not resolve
// is a programming error, caught at startup like installManagedModes.
func installLegacy(capella *cobra.Command) {
	for _, l := range legacyCommands {
		c, rest, err := capella.Find(l.Path)
		if err != nil || c == nil || len(rest) != 0 || c == capella {
			panic(fmt.Sprintf("legacy command %q not found", strings.Join(l.Path, " ")))
		}
		if strings.Join(l.Path, " ") == "cluster create" {
			// Stays visible: only its legacy flags are hidden and warn (see clusterCreateRoute).
			for _, name := range legacyFlagNames {
				if c.Flags().Lookup(name) != nil {
					_ = c.Flags().MarkHidden(name)
				}
			}
			continue
		}
		if strings.Join(l.Path, " ") == "bucket create" {
			// Stays visible for the capellactl arity (forwarded to "bucket add"); the legacy
			// form warns in its own RunE.
			for _, name := range legacyFlagNames {
				if c.Flags().Lookup(name) != nil {
					_ = c.Flags().MarkHidden(name)
				}
			}
			continue
		}
		deprecateLegacy(c, l.Replacement)
	}
}

// deprecateLegacy hides cmd and makes it print a deprecation warning on stderr before it runs.
// Cobra's own Deprecated field is not used: cobra prints it through OutOrStderr, which is stdout
// once Run sets the output, and that would corrupt the true/false stdout of "exists".
func deprecateLegacy(cmd *cobra.Command, replacement string) {
	cmd.Hidden = true
	cmd.Long = "Deprecated: use " + replacement + ".\n\n" + strings.TrimSpace(cmd.Long+"\n"+cmd.Short)
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		warnDeprecated(c.ErrOrStderr(), strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" "), replacement)
		return run(c, args)
	}
}

// warnDeprecated prints the one-line deprecation warning. It ignores --quiet on purpose: the
// warning is the only signal a script gets before the command is removed.
func warnDeprecated(w io.Writer, path, replacement string) {
	fmt.Fprintf(w, "Warning: '%s' is deprecated and will be removed; use %s\n", path, replacement)
}

// usesLegacyFlags says whether any pre-port legacy flag was given on cmd.
func usesLegacyFlags(cmd *cobra.Command) bool {
	for _, name := range legacyFlagNames {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			return true
		}
	}
	return false
}

// capellactlArity says whether args have the positional shape capellactl used for "KIND create":
// bucket CLUSTER NAME, scope CLUSTER BUCKET SCOPE, collection CLUSTER BUCKET SCOPE NAME.
func capellactlArity(kind string, args []string) bool {
	want := map[string]int{"bucket": 2, "scope": 3, "collection": 4}[kind]
	return want > 0 && len(args) == want
}

// shareAddFlags makes the flags of the sibling "add" command parse on create, so a capellactl
// "create" invocation can carry them. A flag create does not have is shared (the same flag), so
// its value and Changed state land on "add" directly; a clashing shorthand is dropped.
func shareAddFlags(create *cobra.Command) {
	add, _, err := create.Parent().Find([]string{"add"})
	if err != nil || add == nil || add == create.Parent() {
		panic(fmt.Sprintf("no 'add' command next to %s", create.CommandPath()))
	}
	add.LocalFlags().VisitAll(func(f *pflag.Flag) {
		fs := create.Flags()
		if fs.Lookup(f.Name) != nil {
			return
		}
		if f.Shorthand != "" && fs.ShorthandLookup(f.Shorthand) != nil {
			c := *f
			c.Shorthand = ""
			fs.AddFlag(&c)
			return
		}
		fs.AddFlag(f)
	})
}

// forwardToAdd runs the sibling "add" command of a "create" command with the same arguments and
// flags. capellactl named these verbs "create"; cbctl names them "add".
func forwardToAdd(cmd *cobra.Command, args []string) error {
	add, _, err := cmd.Parent().Find([]string{"add"})
	if err != nil || add == nil || add == cmd.Parent() {
		return fmt.Errorf("no 'add' command next to %s", cmd.CommandPath())
	}
	kind := cmd.Parent().Name()
	fmt.Fprintf(cmd.ErrOrStderr(), "Note: 'capella %s create' is 'capella %s add' in cbctl\n", kind, kind)
	add.InheritedFlags() // merges the persistent flags, so shared ones resolve to the same flag
	var problems []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		target := add.Flags().Lookup(f.Name)
		switch target {
		case f:
			// Shared with "add" (see shareAddFlags): the value is already in place.
		case nil:
			problems = append(problems, "--"+f.Name)
		default:
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				if tv, ok := target.Value.(pflag.SliceValue); ok && tv.Replace(sv.GetSlice()) == nil {
					target.Changed = true
					return
				}
			}
			if err := add.Flags().Set(f.Name, f.Value.String()); err != nil {
				problems = append(problems, "--"+f.Name)
			}
		}
	})
	if len(problems) > 0 {
		return usageError(fmt.Sprintf("run 'cbctl capella %s add --help' for its flags", kind),
			"'%s add' does not take %s", kind, strings.Join(problems, ", "))
	}
	add.SetContext(cmd.Context())
	if err := add.ValidateArgs(args); err != nil {
		return &CLIError{Code: ExitConfigInvalid, Message: err.Error()}
	}
	return add.RunE(add, args)
}

// negatableFlags are boolean flags capellactl also spelled as --no-NAME (click's on/off pairs).
var negatableFlags = []string{"disk-auto-expansion", "cost-optimized", "flush", "cross-cluster-versioning", "protect"}

// negation is the hidden --no-NAME flag: setting it sets NAME to the opposite value, so the
// command sees NAME as changed exactly as with --NAME=false.
type negation struct {
	fs     *pflag.FlagSet
	target string
}

func (n *negation) String() string { return "false" }
func (n *negation) Type() string   { return "bool" }
func (n *negation) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	return n.fs.Set(n.target, strconv.FormatBool(!v))
}

// installNegations adds a hidden --no-NAME to every command in the tree that has a bool flag
// NAME from negatableFlags and no --no-NAME yet.
func installNegations(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		fs := c.Flags()
		for _, name := range negatableFlags {
			fl := fs.Lookup(name)
			if fl == nil || fl.Value.Type() != "bool" || fs.Lookup("no-"+name) != nil {
				continue
			}
			fs.Var(&negation{fs: fs, target: name}, "no-"+name, "Same as --"+name+"=false")
			neg := fs.Lookup("no-" + name)
			neg.NoOptDefVal = "true"
			neg.Hidden = true
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
