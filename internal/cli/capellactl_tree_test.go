package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// capellactlTreeFile is capellactl's complete click tree (every command, hidden or not, and its
// long options), dumped from the last capellactl release. One line per command:
// VISIBILITY<TAB>PATH<TAB>FLAGS, and a final "G" line for the root options.
const capellactlTreeFile = "testdata/capellactl-tree.tsv"

// compatCommandExceptions are capellactl commands that do not resolve to a cbctl command of the
// same path under `cbctl capella`. Each names where the migration guide documents it.
var compatCommandExceptions = map[string]string{
	// Root-level in cbctl: `cbctl capella completion` exits 3 naming `cbctl completion`.
	"completion": "guide: Commands table, completion row",
}

// compatFlagExceptions are capellactl options that cbctl spells differently, keyed "PATH --flag".
var compatFlagExceptions = map[string]string{
	// `capella import` is the JSON Lines importer; its sync flags exit 3 with a hint to use sync.
	"import --cluster":    "guide: Commands table, import row",
	"import --cluster-id": "guide: Commands table, import row",
	"import --file":       "guide: Commands table, import row",
	"import --force":      "guide: Commands table, import row",
	"import --full":       "guide: Commands table, import row",
	"import --project":    "guide: Commands table, import row",
	"import --project-id": "guide: Commands table, import row",
	"import --stdout":     "guide: Commands table, import row",
}

type capellactlCommand struct {
	hidden bool
	path   []string
	flags  []string
}

func loadCapellactlTree(t *testing.T) (cmds []capellactlCommand, globals []string) {
	t.Helper()
	f, err := os.Open(filepath.FromSlash(capellactlTreeFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close %s: %v", capellactlTreeFile, err)
		}
	}()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) != 3 {
			t.Fatalf("bad line %q", sc.Text())
		}
		if parts[0] == "G" {
			globals = strings.Fields(parts[2])
			continue
		}
		cmds = append(cmds, capellactlCommand{hidden: parts[0] == "H", path: strings.Fields(parts[1]), flags: strings.Fields(parts[2])})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(cmds) < 90 || len(globals) == 0 {
		t.Fatalf("%s looks truncated: %d commands, %d root options", capellactlTreeFile, len(cmds), len(globals))
	}
	return cmds, globals
}

// hasFlag reports whether cmd accepts --name: its own flags, inherited persistent flags, or a
// --no-X negation installed for a boolean --X.
func hasFlag(cmd *cobra.Command, name string) bool {
	if cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil {
		return true
	}
	if name == "version" && cmd.Version != "" {
		return true
	}
	return false
}

// TestCapellactlCompat proves that every capellactl command path and long option exists in
// cbctl under `cbctl capella` (or is a documented exception), so a script ported by replacing
// `capellactl` with `cbctl capella` parses. Behaviour per row is covered by the tests named in
// docs/specs/capellactl-port-map.md.
func TestCapellactlCompat(t *testing.T) {
	root := NewRootCmd(Deps{Version: "test"})
	capella, _, err := root.Find([]string{"capella"})
	if err != nil {
		t.Fatal(err)
	}
	// Flags are registered lazily on some commands; initialise help/version like Execute does.
	root.InitDefaultHelpFlag()
	cmds, globals := loadCapellactlTree(t)
	usedCmd := map[string]bool{}
	usedFlag := map[string]bool{}

	for _, g := range globals {
		if !hasFlag(capella, strings.TrimPrefix(g, "--")) {
			t.Errorf("root option %s: `cbctl capella %s` is not accepted", g, g)
		}
	}
	for _, c := range cmds {
		key := strings.Join(c.path, " ")
		if _, ok := compatCommandExceptions[key]; ok {
			usedCmd[key] = true
			continue
		}
		found, rest, err := capella.Find(c.path)
		if err != nil || len(rest) != 0 || found.Name() != c.path[len(c.path)-1] {
			t.Errorf("capellactl %s: `cbctl capella %s` does not resolve", key, key)
			continue
		}
		for _, fl := range c.flags {
			fkey := key + " " + fl
			if _, ok := compatFlagExceptions[fkey]; ok {
				usedFlag[fkey] = true
				continue
			}
			if !hasFlag(found, strings.TrimPrefix(fl, "--")) {
				t.Errorf("capellactl %s %s: `cbctl capella %s` does not accept %s", key, fl, key, fl)
			}
		}
	}
	for k := range compatCommandExceptions {
		if !usedCmd[k] {
			t.Errorf("stale command exception %q: not in %s", k, capellactlTreeFile)
		}
	}
	for k := range compatFlagExceptions {
		if !usedFlag[k] {
			t.Errorf("stale flag exception %q: not in %s", k, capellactlTreeFile)
		}
	}
}
