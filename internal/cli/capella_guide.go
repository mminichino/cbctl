package cli

import (
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/guide"
	"github.com/spf13/cobra"
)

const guideOverview = `Getting started:
  1. cbctl capella configure      save your API Key Token (OS keychain) and organization
  2. cbctl capella init           describe a new environment (or: cbctl capella sync --project NAME)
  3. cbctl capella validate       check capella.yaml offline
  4. cbctl capella plan           see what would change
  5. cbctl capella apply          make Capella match the file (run it again: "No changes")
`

func newCapellaGuideCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guide [TOPIC|topics|config FIELD]",
		Short: "Built-in guides: getting started, config, scaling, backups, CI and more (offline)",
		Long: "Print an embedded guide topic. Works offline and needs no API key.\n\n" +
			"  cbctl capella guide                  overview and the list of topics\n" +
			"  cbctl capella guide topics           the list of topics\n" +
			"  cbctl capella guide TOPIC            one topic\n" +
			"  cbctl capella guide config FIELD     what one capella.yaml field means (for example cloudProvider.region)\n\n" +
			"Topics:\n" + guide.TopicList(),
		Example: "  cbctl capella guide getting-started\n  cbctl capella guide config cloudProvider.region",
		Args:    cobra.MaximumNArgs(2),
		RunE:    runGuide,
	}
}

func runGuide(cmd *cobra.Command, args []string) error {
	w := cmd.OutOrStdout()
	if len(args) == 0 {
		fmt.Fprint(w, guideOverview)
		fmt.Fprintf(w, "\nTopics (cbctl capella guide TOPIC):\n%s\n", guide.TopicList())
		return nil
	}
	name := args[0]
	if name == "topics" && len(args) == 1 {
		fmt.Fprintln(w, guide.TopicList())
		return nil
	}
	if !guide.Exists(name) {
		return Errorf(ExitConfigInvalid, "Pick one of: "+strings.Join(guide.Names(), ", ")+".",
			"unknown guide topic %q", name)
	}
	if len(args) == 2 && name != "config" {
		return Errorf(ExitConfigInvalid, "Only 'guide config' takes a field path.",
			"guide %s takes no field", name)
	}
	if name == "config" && len(args) == 2 {
		text, err := guide.Field(args[1])
		if err != nil {
			return Errorf(ExitConfigInvalid, "Run 'cbctl capella guide config' to see the fields.", "%v", err)
		}
		fmt.Fprintln(w, strings.TrimRight(text, "\n"))
		return nil
	}
	text, err := guide.Render(name)
	if err != nil {
		return err
	}
	fmt.Fprint(w, strings.TrimRight(text, "\n")+"\n")
	if name == "config" {
		fmt.Fprintf(w, "\nFields (cbctl capella guide config FIELD):\n%s\n", guide.Fields())
	}
	return nil
}
