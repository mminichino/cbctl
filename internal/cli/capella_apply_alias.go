package cli

import (
	"strings"

	"github.com/mminichino/cbctl/internal/guide"
	"github.com/spf13/cobra"
)

// noDescendantsAnnotation on an apply command plans the targets without their children.
const noDescendantsAnnotation = "cbctl/apply-no-descendants"

// newTargetedApplyCmd is "apply --target KIND/NAME" under a resource group: capellactl spelled
// it "KIND apply NAME". It takes the flags of apply (except --target) and runs it.
func newTargetedApplyCmd(kind, short string) *cobra.Command {
	cmd := newCapellaApplyCmd()
	cmd.Use = "apply NAME"
	cmd.Short = short
	cmd.Example = "  cbctl capella " + strings.ReplaceAll(kind, "schedule", "cluster schedule") + " apply NAME --yes"
	cmd.Long = "Same as 'cbctl capella apply --target " + kind + "/NAME'."
	cmd.Args = cobra.ExactArgs(1)
	_ = cmd.Flags().MarkHidden("target")
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if c.Flags().Changed("target") {
			return usageError("drop --target; NAME selects the "+kind, "--target cannot be combined with NAME")
		}
		if err := c.Flags().Set("target", kind+"/"+args[0]); err != nil {
			return err
		}
		return run(c, nil)
	}
	return cmd
}

// newScheduleApplyCmd is "cluster schedule apply NAME", the same as apply --target schedule/NAME.
func newScheduleApplyCmd() *cobra.Command {
	return newTargetedApplyCmd("schedule", "Apply the on/off schedule of cluster NAME from capella.yaml")
}

// newHiddenApplyAlias is a hidden, deprecated "cluster apply NAME" / "project apply NAME".
func newHiddenApplyAlias(kind string) *cobra.Command {
	cmd := newTargetedApplyCmd(kind, "Apply "+kind+" NAME from capella.yaml")
	if kind == "project" {
		// capellactl's "project apply" applied the project's own settings, not its clusters.
		cmd.Annotations = map[string]string{noDescendantsAnnotation: "true"}
	}
	if kind == "cluster" {
		// capellactl's "cluster apply NAME --project P" picked the cluster when its name repeats;
		// the target spelling for that is cluster/P/NAME.
		var project string
		cmd.Flags().StringVar(&project, "project", "", "Project of the cluster (only needed when the name repeats)")
		run := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if project != "" && len(args) == 1 {
				args = []string{project + "/" + args[0]}
			}
			return run(c, args)
		}
	}
	deprecateLegacy(cmd, "cbctl capella apply --target "+kind+"/NAME")
	return cmd
}

// newCapellaHelpAlias is capellactl's "help [TOPIC|COMMAND...]": a guide topic is forwarded to
// "guide [TOPIC]", and a command path prints the same text as "COMMAND... --help".
func newCapellaHelpAlias() *cobra.Command {
	g := newCapellaGuideCmd()
	g.Use = "help [TOPIC|COMMAND...]"
	g.Hidden = true
	g.Short = "Same as 'cbctl capella guide'"
	g.Args = cobra.ArbitraryArgs
	g.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && args[0] != "topics" && !guide.Exists(args[0]) && cmd.Parent() != nil {
			if target, rest, err := cmd.Parent().Find(args); err == nil && target != cmd.Parent() && len(rest) == 0 {
				target.InitDefaultHelpFlag() // cobra adds -h only on the --help path; list it the same way
				return target.Help()
			}
		}
		if len(args) > 2 {
			return usageError("Run 'cbctl capella guide' to see the topics.", "help takes a topic or a command, got %q", strings.Join(args, " "))
		}
		return runGuide(cmd, args)
	}
	return g
}
