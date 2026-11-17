package cli

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/mminichino/cbctl/internal/presets"
	"github.com/spf13/cobra"
)

// ClusterSpec is what the cluster create wizard collected. Empty fields keep the builder's
// defaults; the builder (managedClusterCreate) validates the whole set.
type ClusterSpec struct {
	Name     string
	Provider string
	Region   string
	Size     string
	Groups   []string
	CIDR     string
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

func oneOf(choices []string) func(string) error {
	return func(v string) error {
		if !slices.Contains(choices, v) {
			return fmt.Errorf("choose one of %s", strings.Join(choices, ", "))
		}
		return nil
	}
}

// run asks for every field cmd's flags did not set. cidr is the suggested network.
func (w *clusterWizard) run(cmd *cobra.Command, cidr string) (ClusterSpec, error) {
	ch := cmd.Flags().Changed
	spec := ClusterSpec{}
	var err error
	fmt.Fprintln(w.out, "New Capella cluster (press Enter to accept a default; Ctrl-D cancels)")
	spec.Name, err = w.ask("Cluster name", "", func(v string) error {
		if !clusterNamePattern.MatchString(v) {
			return fmt.Errorf("give a name of letters, digits, spaces, '.', '_' or '-'")
		}
		return nil
	})
	if err != nil {
		return spec, err
	}
	free := flagBool(cmd, "free-tier")
	spec.Provider = flagString(cmd, "provider")
	if !ch("provider") {
		if spec.Provider, err = w.ask("Cloud provider ("+strings.Join(cloudProviders, "/")+")", "aws", oneOf(cloudProviders)); err != nil {
			return spec, err
		}
	}
	if !ch("region") {
		def := regionDefaults[spec.Provider]
		if free {
			def = freeTierRegions[spec.Provider][0]
		}
		if spec.Region, err = w.ask("Region", def, func(v string) error {
			if v == "" {
				return fmt.Errorf("a region is required")
			}
			return nil
		}); err != nil {
			return spec, err
		}
	}
	grouped := len(flagStrings(cmd, "group")) > 0
	if !free && !grouped && !ch("size") && !flagBool(cmd, "single-node") {
		sizes := append(presets.PresetNames(), "custom")
		if spec.Size, err = w.ask("Size ("+strings.Join(sizes, "/")+")", "small", oneOf(sizes)); err != nil {
			return spec, err
		}
		if spec.Size == "custom" {
			spec.Size = ""
			for {
				g, err := w.ask("Service group SERVICES:NODESxCPU/RAM (empty line to finish)", "", func(v string) error {
					if v == "" {
						return nil
					}
					_, err := parseGroupSpec(v, spec.Provider, diskChange{})
					return err
				})
				if err != nil {
					return spec, err
				}
				if g == "" {
					if len(spec.Groups) == 0 {
						fmt.Fprintln(w.out, "  add at least one service group")
						continue
					}
					break
				}
				spec.Groups = append(spec.Groups, g)
			}
		}
	}
	if !free && !ch("cidr") {
		if spec.CIDR, err = w.ask("Network CIDR", cidr, func(v string) error {
			if _, err := netip.ParsePrefix(v); err != nil {
				return fmt.Errorf("give a CIDR such as 10.0.0.0/24")
			}
			return nil
		}); err != nil {
			return spec, err
		}
	}
	return spec, nil
}

// apply writes the answers into cmd's flags, so the builder reads them like typed flags.
func (s ClusterSpec) apply(cmd *cobra.Command) error {
	set := func(name, v string) error {
		if v == "" {
			return nil
		}
		return cmd.Flags().Set(name, v)
	}
	for _, kv := range [][2]string{{"name", s.Name}, {"provider", s.Provider}, {"region", s.Region}, {"size", s.Size}, {"cidr", s.CIDR}} {
		if err := set(kv[0], kv[1]); err != nil {
			return err
		}
	}
	for _, g := range s.Groups {
		if err := cmd.Flags().Set("group", g); err != nil {
			return err
		}
	}
	return nil
}
