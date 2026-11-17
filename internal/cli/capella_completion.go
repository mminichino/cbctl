package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// writeCompletionToCmdOut rebinds the bash, zsh, fish and powershell completion commands to write
// to the command's output when they run. Cobra binds them to the writer the root has when the
// completion command is created, which is the process stdout before Run sets Deps.Stdout.
func writeCompletionToCmdOut(root *cobra.Command) {
	var completion *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			completion = c
		}
	}
	if completion == nil {
		return
	}
	for _, sub := range completion.Commands() {
		shell := sub.Name()
		sub.RunE = func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			desc := true
			if v, err := cmd.Flags().GetBool("no-descriptions"); err == nil {
				desc = !v
			}
			switch shell {
			case "bash":
				return cmd.Root().GenBashCompletionV2(out, desc)
			case "zsh":
				if desc {
					return cmd.Root().GenZshCompletion(out)
				}
				return cmd.Root().GenZshCompletionNoDesc(out)
			case "fish":
				return cmd.Root().GenFishCompletion(out, desc)
			case "powershell":
				if desc {
					return cmd.Root().GenPowerShellCompletionWithDesc(out)
				}
				return cmd.Root().GenPowerShellCompletion(out)
			}
			return fmt.Errorf("unsupported shell %q", shell)
		}
		sub.Run = nil
	}
}

// installClusterCompletion makes shell completion offer the cluster names of capella.yaml for the
// first argument of every Capella command that takes a CLUSTER (or SOURCE) there.
func installClusterCompletion(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if fields := strings.Fields(c.Use); len(fields) > 1 && c.ValidArgsFunction == nil {
			if arg := strings.Trim(fields[1], "[]"); arg == "CLUSTER" || arg == "SOURCE" {
				c.ValidArgsFunction = completeClusterNames
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// completeClusterNames completes the first argument from the clusters declared in capella.yaml.
// It works offline; without a readable file it offers nothing (and no file names).
func completeClusterNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	disc, err := discoverConfig(cmd)
	if err != nil || !disc.Exists {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	text, err := os.ReadFile(disc.Path)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, name := range declaredClusterNames(text) {
		if strings.HasPrefix(name, toComplete) {
			out = append(out, name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// declaredClusterNames returns the sorted, unique cluster names of a capella.yaml in either
// form (projects[].clusters[] or the single-project clusters[]). Unparsable text yields none.
func declaredClusterNames(text []byte) []string {
	type cluster struct {
		Name string `yaml:"name"`
	}
	var doc struct {
		Clusters []cluster `yaml:"clusters"`
		Projects []struct {
			Clusters []cluster `yaml:"clusters"`
		} `yaml:"projects"`
	}
	if err := yaml.Unmarshal(text, &doc); err != nil {
		return nil
	}
	seen := map[string]bool{}
	add := func(cs []cluster) {
		for _, c := range cs {
			if c.Name != "" {
				seen[c.Name] = true
			}
		}
	}
	add(doc.Clusters)
	for _, p := range doc.Projects {
		add(p.Clusters)
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
