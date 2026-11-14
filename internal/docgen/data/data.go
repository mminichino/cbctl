// Package data holds embedded JSON/YAML dictionaries for the documentation generators.
package data

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed phases.yaml
var phasesYAML []byte

//go:embed commands.json
var commandsJSON []byte

//go:embed workflow.json
var workflowJSON []byte

//go:embed ops.json
var opsJSON []byte

//go:embed areas.json
var areasJSON []byte

//go:embed gaps.json
var gapsJSON []byte

//go:embed as_ops.json
var asOpsJSON []byte

//go:embed as_areas.json
var asAreasJSON []byte

// CommandEntry is one resource command and the stories that deliver it.
type CommandEntry struct {
	Stories []string `json:"stories"`
	Left    string   `json:"left"`
}

// WorkflowEntry is a workflow command (plan/apply/…) not one-to-one with an API op.
type WorkflowEntry struct {
	Name    string   `json:"name"`
	Stories []string `json:"stories"`
	Note    string   `json:"note"`
}

// OpEntry maps a METHOD+path to commands and story metadata.
type OpEntry struct {
	Commands   string   `json:"commands"`
	Note       string   `json:"note"`
	Left       string   `json:"left"`
	Status     *string  `json:"status"`
	Stories    []string `json:"stories"`
	Requires   []string `json:"requires"`
	PartialUse bool     `json:"partial_use"`
}

// AreaEntry is a Management API tag and whether it is in 1.0.0 scope.
type AreaEntry struct {
	Tag   string `json:"tag"`
	Label string `json:"label"`
	In10  bool   `json:"in_1_0"`
}

// ASOpEntry is one App Services operation.
type ASOpEntry struct {
	Command string `json:"command"`
	Note    string `json:"note"`
}

// ASAreaEntry is an App Services tag group.
type ASAreaEntry struct {
	Label string `json:"label"`
	Tag   string `json:"tag"`
}

// Phase is one delivery phase of a release.
type Phase struct {
	Name    string   `yaml:"name"`
	Outcome string   `yaml:"outcome"`
	IDs     []string `yaml:"ids"`
}

// ReleaseConfig holds phases and checklist for one release.
type ReleaseConfig struct {
	Architecture string   `yaml:"architecture"`
	Scope        string   `yaml:"scope"`
	Phases       []Phase  `yaml:"phases"`
	Checklist    []string `yaml:"checklist"`
}

// Bundle is the full extracted dictionary set.
type Bundle struct {
	Releases map[string]ReleaseConfig
	Commands map[string]CommandEntry
	Workflow []WorkflowEntry
	Ops      map[string]OpEntry
	Areas    []AreaEntry
	Gaps     []string
	ASOps    map[string]ASOpEntry
	ASAreas  []ASAreaEntry
}

// Load returns the embedded documentation generator data.
func Load() (Bundle, error) {
	var b Bundle
	var wrap struct {
		Releases map[string]ReleaseConfig `yaml:"releases"`
	}
	if err := yaml.Unmarshal(phasesYAML, &wrap); err != nil {
		return b, fmt.Errorf("phases.yaml: %w", err)
	}
	b.Releases = wrap.Releases
	if err := json.Unmarshal(commandsJSON, &b.Commands); err != nil {
		return b, fmt.Errorf("commands.json: %w", err)
	}
	if err := json.Unmarshal(workflowJSON, &b.Workflow); err != nil {
		return b, fmt.Errorf("workflow.json: %w", err)
	}
	if err := json.Unmarshal(opsJSON, &b.Ops); err != nil {
		return b, fmt.Errorf("ops.json: %w", err)
	}
	if err := json.Unmarshal(areasJSON, &b.Areas); err != nil {
		return b, fmt.Errorf("areas.json: %w", err)
	}
	if err := json.Unmarshal(gapsJSON, &b.Gaps); err != nil {
		return b, fmt.Errorf("gaps.json: %w", err)
	}
	if err := json.Unmarshal(asOpsJSON, &b.ASOps); err != nil {
		return b, fmt.Errorf("as_ops.json: %w", err)
	}
	if err := json.Unmarshal(asAreasJSON, &b.ASAreas); err != nil {
		return b, fmt.Errorf("as_areas.json: %w", err)
	}
	return b, nil
}
