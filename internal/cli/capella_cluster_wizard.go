package cli

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// ClusterSpec is what the cluster create wizard collected. Empty fields keep the builder's
// defaults; the builder (managedClusterCreate) validates the whole set.
type ClusterSpec struct {
	Name     string
	Free     bool
	Provider string
	Region   string
	Size     string
	Groups   []string
	CIDR     string
	Support  string
	// Nodes, CPU and RAM are the one-group custom layout.
	Nodes, CPU, RAM string
	// Disk is the disk chosen for the one-group custom layout.
	Disk *diskAnswer
}

// wizardTries is how often one question is asked before the wizard gives up.
const wizardTries = 3

var clusterNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,255}$`)

// errWizardCancelled is input ending before the wizard finished (EOF or Ctrl-D).
var errWizardCancelled = Errorf(ExitError, "", "Cancelled; nothing was changed.")

// clusterWizard asks for the cluster create fields the flags did not give, capellactl style.
type clusterWizard struct {
	in  *bufio.Reader
	out io.Writer
	// noFree skips the cluster type question: the organization's free-tier cluster is in use.
	noFree bool
}

func newClusterWizard(in io.Reader, out io.Writer) *clusterWizard {
	if b, ok := in.(*bufferedStdin); ok {
		return &clusterWizard{in: b.Reader, out: out}
	}
	return &clusterWizard{in: bufio.NewReader(in), out: out}
}

// bufferedStdin is stdin behind the wizard's read buffer. Later prompts (the plan confirmation)
// must read through the same buffer, or the answers the wizard read ahead are lost; Underlying
// keeps the terminal check working.
type bufferedStdin struct {
	*bufio.Reader
	src io.Reader
}

// Underlying returns the wrapped stdin.
func (b *bufferedStdin) Underlying() io.Reader { return b.src }

// shareStdin puts a buffered stdin on cmd, so every prompt of the invocation reads through one
// buffer, and returns it.
func shareStdin(cmd *cobra.Command) io.Reader {
	in := stdinFrom(cmd)
	if b, ok := in.(*bufferedStdin); ok {
		return b
	}
	b := &bufferedStdin{Reader: bufio.NewReader(in), src: in}
	cmd.SetIn(b)
	return b
}

// ask prompts until check accepts the answer (an empty answer is def), at most wizardTries.
func (w *clusterWizard) ask(prompt, def string, check func(string) error) (string, error) {
	for try := 0; try < wizardTries; try++ {
		label := prompt
		if def != "" {
			label += " [" + def + "]"
		}
		fmt.Fprintf(w.out, "%s: ", label)
		line, err := w.in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(w.out)
			return "", errWizardCancelled
		}
		v := strings.TrimSpace(line)
		if v == "" {
			v = def
		}
		if cerr := check(v); cerr != nil {
			fmt.Fprintf(w.out, "  %v\n", cerr)
			continue
		}
		return v, nil
	}
	return "", usageError("pass the values as flags instead", "%s: no valid answer after %d tries", prompt, wizardTries)
}

// matchChoice returns the choice answer names: an exact match, or the only choice that starts
// with it (case-insensitive), so "m" or "multiple" picks "multiple groups" (capellactl's
// PrefixChoice). An empty, unknown or ambiguous answer is an error.
func matchChoice(choices []string, answer string) (string, error) {
	text := strings.ToLower(strings.TrimSpace(answer))
	var starts []string
	for _, c := range choices {
		if strings.ToLower(c) == text {
			return c, nil
		}
		if text != "" && strings.HasPrefix(strings.ToLower(c), text) {
			starts = append(starts, c)
		}
	}
	if len(starts) == 1 {
		return starts[0], nil
	}
	if len(starts) > 1 {
		return "", fmt.Errorf("'%s' could be %s; type more of the answer", answer, strings.Join(starts, " or "))
	}
	return "", fmt.Errorf("choose one of %s", strings.Join(choices, ", "))
}

// oneOf accepts exactly one of choices.
func oneOf(choices []string) func(string) error {
	return func(v string) error {
		if !slices.Contains(choices, v) {
			return fmt.Errorf("choose one of %s", strings.Join(choices, ", "))
		}
		return nil
	}
}

// choose asks for one of choices (a unique start of one is enough) and returns the full choice.
func (w *clusterWizard) choose(prompt, def string, choices []string) (string, error) {
	v, err := w.ask(prompt+" ("+strings.Join(choices, ", ")+")", def, func(v string) error {
		_, err := matchChoice(choices, v)
		return err
	})
	if err != nil {
		return "", err
	}
	return matchChoice(choices, v)
}

// askInt asks for a whole number; check (optional) adds its own rule.
func (w *clusterWizard) askInt(prompt string, def int, check func(int) error) (int, error) {
	v, err := w.ask(prompt, strconv.Itoa(def), func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("give a whole number")
		}
		if check != nil {
			return check(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// yesNo asks a yes/no question; an empty answer is def.
func (w *clusterWizard) yesNo(prompt string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	v, err := w.ask(prompt+" ["+hint+"]", "", func(v string) error {
		if v == "" {
			return nil
		}
		_, err := matchChoice([]string{"yes", "no"}, v)
		return err
	})
	if err != nil || v == "" {
		return def, err
	}
	c, _ := matchChoice([]string{"yes", "no"}, v) // checked above
	return c == "yes", nil
}

// sizedByFlags reports whether cmd's flags already fix the cluster type and size, so the wizard
// asks neither (capellactl asks them only when nothing about the size was given).
func sizedByFlags(cmd *cobra.Command) bool {
	ch := cmd.Flags().Changed
	for _, f := range []string{"group", "size", "nodes", "cpu", "ram", "services", "single-node", "disk-type", "disk-size",
		"disk-iops", "availability", "support-plan", "support-timezone", "server-version", "zone", "cmek-id",
		"private-dns", "disk-auto-expansion"} {
		if cmd.Flags().Lookup(f) != nil && ch(f) {
			return true
		}
	}
	return false
}
