package coverage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DocMapPath is the mapping of capellactl's documents to cbctl's, relative to the repo root.
const DocMapPath = "docs/capella/doc-map.yaml"

// DocMapping maps one capellactl document (a path relative to capellactl's docs/), or every
// document under a directory when Source ends in "/", to the cbctl document that carries its
// content. A document cbctl does not carry has Dropped set to the reason instead. Exactly one of
// Target and Dropped is set.
type DocMapping struct {
	Source  string `yaml:"source"`
	Target  string `yaml:"target,omitempty"`
	Dropped string `yaml:"dropped,omitempty"`
}

// docMapFile is the layout of doc-map.yaml. Sources lists every capellactl document at the
// snapshot the port was made from, so the check runs without a capellactl checkout.
type docMapFile struct {
	Sources  []string     `yaml:"sources"`
	Mappings []DocMapping `yaml:"mappings"`
}

// LoadDocMap reads doc-map.yaml under repoRoot.
func LoadDocMap(repoRoot string) (sources []string, mappings []DocMapping, err error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(DocMapPath)))
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", DocMapPath, err)
	}
	var f docMapFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", DocMapPath, err)
	}
	return f.Sources, f.Mappings, nil
}

// ListSourceDocs returns every .md file under a capellactl docs directory, relative and with
// forward slashes, sorted.
func ListSourceDocs(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	sort.Strings(out)
	return out, nil
}

// CheckDocCoverage reports every problem with mappings: a source document no mapping covers,
// one covered more than once, a mapping that covers nothing, a mapping with both or neither of
// Target and Dropped, and a Target that does not exist under repoRoot.
func CheckDocCoverage(sourceDocs []string, mappings []DocMapping, repoRoot string) []string {
	var problems []string
	used := make([]bool, len(mappings))
	for i, m := range mappings {
		switch {
		case m.Source == "":
			problems = append(problems, fmt.Sprintf("mapping %d has no source", i+1))
		case m.Target != "" && m.Dropped != "":
			problems = append(problems, fmt.Sprintf("%s: give a target or a dropped reason, not both", m.Source))
		case m.Target == "" && strings.TrimSpace(m.Dropped) == "":
			problems = append(problems, fmt.Sprintf("%s: needs a target or a dropped reason", m.Source))
		case m.Target != "":
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(m.Target))); errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, fmt.Sprintf("%s: target %s does not exist", m.Source, m.Target))
			} else if err != nil {
				problems = append(problems, fmt.Sprintf("%s: target %s: %v", m.Source, m.Target, err))
			}
		}
	}
	for _, doc := range sourceDocs {
		var hits []string
		for i, m := range mappings {
			if covers(m.Source, doc) {
				used[i] = true
				hits = append(hits, m.Source)
			}
		}
		switch len(hits) {
		case 0:
			problems = append(problems, fmt.Sprintf("%s: no mapping in %s; add a target or a dropped reason", doc, DocMapPath))
		case 1:
		default:
			problems = append(problems, fmt.Sprintf("%s: mapped more than once (%s)", doc, strings.Join(hits, ", ")))
		}
	}
	for i, m := range mappings {
		if !used[i] && m.Source != "" {
			problems = append(problems, fmt.Sprintf("%s: maps no capellactl document", m.Source))
		}
	}
	return problems
}

func covers(source, doc string) bool {
	if strings.HasSuffix(source, "/") {
		return strings.HasPrefix(doc, source)
	}
	return source == doc
}
