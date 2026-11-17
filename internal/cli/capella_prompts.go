package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// capellactl asked for a missing argument or option when a terminal was attached, and failed
// with exit 3 ("missing X: there is no terminal to ask for it") otherwise. installPrompts gives
// the commands that did so the same behavior; the answers become ordinary arguments and flags,
// so the command's own validation and both its direct and file-managed paths run unchanged.
func installPrompts(capella *cobra.Command) {
	find := func(path ...string) *cobra.Command {
		c, rest, err := capella.Find(path)
		if err != nil || len(rest) != 0 || c == capella {
			panic(fmt.Sprintf("prompts: no command %q", strings.Join(path, " ")))
		}
		return c
	}
	clone := find("cluster", "clone")
	clone.Use = "clone [SOURCE] [NEW_NAME]"
	clone.Args = cobra.RangeArgs(0, 2)
	wrapPrompt(clone, promptClone)

	wrapPrompt(find("project", "create"), func(cmd *cobra.Command, args []string) ([]string, error) {
		if cmd.Flags().Changed("description") || !canPrompt(cmd) {
			return args, nil // the description is optional
		}
		v, err := promptOf(cmd).ask("Description (optional)", "", func(string) error { return nil })
		if err != nil {
			return nil, err
		}
		return args, setFlag(cmd, "description", v, v != "")
	})
	wrapPrompt(find("project", "update"), func(cmd *cobra.Command, args []string) ([]string, error) {
		if cmd.Flags().Changed("description") || !canPrompt(cmd) {
			return args, nil
		}
		v, err := promptOf(cmd).ask("Description", "", nonEmpty("a description is required"))
		if err != nil {
			return nil, err
		}
		return args, setFlag(cmd, "description", v, true)
	})
	wrapPrompt(find("bucket", "add"), func(cmd *cobra.Command, args []string) ([]string, error) {
		if cmd.Flags().Changed("memory") || !canPrompt(cmd) {
			return args, nil // capellactl asked; without a terminal the default applies
		}
		def := cmd.Flags().Lookup("memory").DefValue
		v, err := promptOf(cmd).ask("Memory quota in MB", def, func(v string) error {
			if n, err := strconv.Atoi(v); err != nil || n <= 0 {
				return fmt.Errorf("give a whole number of MB, for example %s", def)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return args, setFlag(cmd, "memory", v, true)
	})
	wrapPrompt(find("cluster", "group", "add"), func(cmd *cobra.Command, args []string) ([]string, error) {
		if cmd.Flags().Changed("services") {
			return args, nil
		}
		if !canPrompt(cmd) {
			return nil, usageError("pass --services, for example --services search", "missing --services: there is no terminal to ask for it")
		}
		v, err := promptOf(cmd).ask("Services of the new group", "", nonEmpty("give comma-separated services, for example search"))
		if err != nil {
			return nil, err
		}
		return args, setFlag(cmd, "services", v, true)
	})
	wrapPrompt(find("bucket", "update"), promptBucketUpdate)
}

// bucketQuestion is one prompt of promptBucketUpdate and the flag its answer sets.
type bucketQuestion struct {
	flag, prompt, def string
	check             func(string) error
}

// bucketUpdateSettings are the bucket update flags that change something.
var bucketUpdateSettings = []string{"memory", "replicas", "durability", "ttl", "flush", "no-flush", "priority",
	"cross-cluster-versioning", "no-cross-cluster-versioning"}

// promptBucketUpdate asks, like capellactl, for the new settings of a bucket when none was
// given and a terminal is attached: memory, and on a provisioned cluster also replicas,
// durability and TTL (Capella chooses those on the free tier). Enter keeps the current value.
func promptBucketUpdate(cmd *cobra.Command, args []string) ([]string, error) {
	for _, n := range bucketUpdateSettings {
		if cmd.Flags().Changed(n) {
			return args, nil
		}
	}
	if !canPrompt(cmd) || len(args) != 2 {
		return args, nil // the command reports "nothing to update"
	}
	rt, err := newRuntime(cmd, cmdAPIFlags(cmd))
	if err != nil {
		return nil, err
	}
	t, err := rt.locateCluster(args[0], flagString(cmd, "project"))
	if err != nil {
		return nil, err
	}
	cur, _, err := rt.readBucket(t, args[1])
	if err != nil {
		return nil, err
	}
	w := promptOf(cmd)
	fmt.Fprintf(w.out, "Press Enter to keep the current value of %s.\n", args[1])
	whole := func(min, max int) func(string) error {
		return func(v string) error {
			if n, err := strconv.Atoi(v); err != nil || n < min || (max >= 0 && n > max) {
				return fmt.Errorf("give a whole number from %d", min)
			}
			return nil
		}
	}
	questions := []bucketQuestion{{"memory", "Memory (MB)", strconv.Itoa(cur.MemoryAllocationInMb), whole(1, -1)}}
	if !t.freeTier() {
		questions = append(questions,
			bucketQuestion{"replicas", "Replicas", strconv.Itoa(cur.Replicas), whole(0, 3)},
			bucketQuestion{"durability", "Durability", cur.DurabilityLevel, nonEmpty("give a durability level")},
			bucketQuestion{"ttl", "TTL seconds (0: none)", strconv.Itoa(cur.TimeToLiveInSeconds), whole(0, -1)})
	}
	for _, q := range questions {
		v, err := w.ask(q.prompt, q.def, q.check)
		if err != nil {
			return nil, err
		}
		if err := setFlag(cmd, q.flag, v, true); err != nil {
			return nil, err
		}
	}
	return args, nil
}

// wrapPrompt runs fill before cmd's RunE; fill returns the (completed) arguments.
func wrapPrompt(cmd *cobra.Command, fill func(*cobra.Command, []string) ([]string, error)) {
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		args, err := fill(c, args)
		if err != nil {
			return err
		}
		return run(c, args)
	}
}

func canPrompt(cmd *cobra.Command) bool { return ttyCheck(shareStdin(cmd)) }

func promptOf(cmd *cobra.Command) *clusterWizard {
	return newClusterWizard(shareStdin(cmd), stderrFrom(cmd))
}

func setFlag(cmd *cobra.Command, name, value string, set bool) error {
	if !set {
		return nil
	}
	return cmd.Flags().Set(name, value)
}

func nonEmpty(msg string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s", msg)
		}
		return nil
	}
}

// cmdAPIFlags reads the --token/--org-id/--api-host values of cmd.
func cmdAPIFlags(cmd *cobra.Command) *apiFlags {
	return &apiFlags{token: flagString(cmd, "token"), orgID: flagString(cmd, "org-id"), apiHost: flagString(cmd, "api-host")}
}

// promptClone completes "cluster clone [SOURCE] [NEW_NAME]" like capellactl: the source is
// picked from the organization's clusters, then the region, the name and the target project are
// asked. Without a terminal, missing values exit 3.
func promptClone(cmd *cobra.Command, args []string) ([]string, error) {
	region := strings.TrimSpace(flagString(cmd, "region"))
	if len(args) == 2 && region != "" {
		return args, nil
	}
	if !canPrompt(cmd) {
		what := "--region"
		switch len(args) {
		case 0:
			what = "SOURCE"
		case 1:
			what = "NEW_NAME"
		}
		return nil, usageError("give the cluster to copy: 'cbctl capella cluster clone SOURCE NEW_NAME --region REGION'",
			"missing %s: there is no terminal to ask for it", what)
	}
	w := promptOf(cmd)
	full := len(args) == 0 // capellactl asks for the target project only in the full wizard
	if len(args) == 0 {
		rt, err := newRuntime(cmd, cmdAPIFlags(cmd))
		if err != nil {
			return nil, err
		}
		proj, cl, err := pickCloneSource(rt, w, flagString(cmd, "project"))
		if err != nil {
			return nil, err
		}
		if err := cmd.Flags().Set("project", proj.Name); err != nil {
			return nil, err
		}
		args = []string{cl.Name}
		if !cmd.Flags().Changed("region") && cl.CloudProvider != nil {
			fmt.Fprintf(w.out, "%s is on %s in %s.\n", cl.Name, cl.CloudProvider.Type, cl.CloudProvider.Region)
		}
		if full && !cmd.Flags().Changed("to-project") {
			to, err := w.ask("Project of the clone", proj.Name, nonEmpty("a project is required"))
			if err != nil {
				return nil, err
			}
			if to != proj.Name {
				if err := cmd.Flags().Set("to-project", to); err != nil {
					return nil, err
				}
			}
		}
	}
	if region == "" {
		v, err := w.ask("Region for the clone", "", nonEmpty("a region is required"))
		if err != nil {
			return nil, err
		}
		if err := cmd.Flags().Set("region", v); err != nil {
			return nil, err
		}
		region = v
	}
	if len(args) == 1 {
		v, err := w.ask("Name of the clone", args[0]+"-"+region, func(v string) error {
			if !clusterNamePattern.MatchString(v) {
				return fmt.Errorf("give a name of letters, digits, spaces, '.', '_' or '-'")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	return args, nil
}

// pickCloneSource lists the clusters (of project, when given) and asks which one to copy.
func pickCloneSource(rt *runtime, w *clusterWizard, project string) (capellaapi.Project, capellaapi.Cluster, error) {
	projects, err := rt.Client.ListProjects(rt.Ctx)
	if err != nil {
		return capellaapi.Project{}, capellaapi.Cluster{}, fmt.Errorf("listing projects: %w", err)
	}
	type choice struct {
		p capellaapi.Project
		c capellaapi.Cluster
	}
	var choices []choice
	for _, p := range projects {
		if project != "" && p.Name != project {
			continue
		}
		clusters, err := rt.Client.ListClusters(rt.Ctx, p.ID)
		if err != nil {
			return capellaapi.Project{}, capellaapi.Cluster{}, fmt.Errorf("listing clusters in project %s: %w", p.Name, err)
		}
		for _, c := range clusters {
			choices = append(choices, choice{p, c})
		}
	}
	if len(choices) == 0 {
		return capellaapi.Project{}, capellaapi.Cluster{}, Errorf(ExitError, "create a cluster first with 'cbctl capella cluster create NAME'",
			"this organization has no clusters to clone")
	}
	fmt.Fprintln(w.out, "Clusters:")
	for i, ch := range choices {
		where := ""
		if ch.c.CloudProvider != nil {
			where = fmt.Sprintf("  %s %s", ch.c.CloudProvider.Type, ch.c.CloudProvider.Region)
		}
		fmt.Fprintf(w.out, "  %d. %s / %s%s  %s\n", i+1, ch.p.Name, ch.c.Name, where, ch.c.CurrentState)
	}
	v, err := w.ask("Source cluster", "", func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > len(choices) {
			return fmt.Errorf("give a number from 1 to %d", len(choices))
		}
		return nil
	})
	if err != nil {
		return capellaapi.Project{}, capellaapi.Cluster{}, err
	}
	n, _ := strconv.Atoi(v) // validated above
	return choices[n-1].p, choices[n-1].c, nil
}
