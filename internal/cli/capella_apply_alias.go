package cli

import (
	"strings"

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
	deprecateLegacy(cmd, "cbctl capella apply --target "+kind+"/NAME")
	return cmd
}

// newCapellaHelpAlias is capellactl's "help [TOPIC]", forwarded to "guide [TOPIC]".
func newCapellaHelpAlias() *cobra.Command {
	g := newCapellaGuideCmd()
	g.Use = "help [TOPIC]"
	g.Hidden = true
	g.Short = "Same as 'cbctl capella guide'"
	return g
}
