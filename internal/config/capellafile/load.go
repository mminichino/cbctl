package capellafile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// API versions a file may declare. Files written by cbctl say APIVersion; files written by
// capellactl are accepted unchanged.
const (
	APIVersion       = "cbctl/v1"
	LegacyAPIVersion = "capellactl/v1"
)

// SupportedAPIVersions lists every accepted apiVersion value.
var SupportedAPIVersions = []string{APIVersion, LegacyAPIVersion}

// DefaultFileName is the file cbctl looks for in the working directory.
const DefaultFileName = "capella.yaml"

// Load reads and validates the file at path. env resolves ${VAR} references (nil: none are set).
// Problems with the content are returned as *ConfigError; a file that cannot be read is
// returned as a *ConfigError too, so that callers map every config failure to the same exit
// code.
func Load(path string, env func(string) string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	text, err := os.ReadFile(abs)
	if err != nil {
		source := filepath.Base(abs)
		msg := fmt.Sprintf("cannot read %s: %v", abs, err)
		fix := ""
		if os.IsNotExist(err) {
			msg = fmt.Sprintf("%s not found", abs)
			fix = "run 'cbctl capella sync' (or 'cbctl capella init') to create it"
		}
		return nil, NewConfigError(source, []Issue{{Path: "(root)", Where: source, Message: msg, Fix: fix}})
	}
	cfg, err := LoadText(string(text), filepath.Base(abs), filepath.Dir(abs), env)
	if cfg != nil {
		cfg.Path = abs
	}
	return cfg, err
}

// LoadText validates text. source is the display name used in messages; baseDir is the
// directory that relative ${file:PATH} references and credentialsOutput resolve against.
//
// The pipeline runs in stages. Stages 1-3 (YAML, apiVersion, secret references) stop at the
// first stage that fails. The schema, defaults and cross-reference stages follow; the
// cross-reference stage assumes a schema-valid document, so it only runs when the schema stage
// found nothing. All failures end as one *ConfigError sorted by line.
func LoadText(text, source, baseDir string, env func(string) string) (*Config, error) {
	if env == nil {
		env = func(string) string { return "" }
	}
	if source == "" {
		source = DefaultFileName
	}
	fail := func(issues []Issue, raw Obj) error {
		fillWhere(issues, raw)
		return NewConfigError(source, issues)
	}

	// 1. YAML.
	root, issues := parseYAML(text, source)
	if len(issues) > 0 {
		return nil, fail(issues, nil)
	}
	data, _ := toObj(root).(Obj)
	lines := indexLines(root)

	// 2. apiVersion.
	if issues = checkAPIVersion(data, lines); len(issues) > 0 {
		return nil, fail(issues, data)
	}

	// 3. Secret references.
	if issues = resolveSecrets(data, baseDir, lines, env); len(issues) > 0 {
		return nil, fail(issues, data)
	}

	// 4. Schema.
	issues, err := validateSchema(data, lines)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, fail(issues, data)
	}

	// 5. Defaults (in memory only).
	hints := ApplyDefaults(data)

	cfg := &Config{
		Source:  source,
		Raw:     data,
		Text:    text,
		Hints:   hints,
		baseDir: baseDir,
		lines:   lines,
	}
	if org, ok := data["organizationId"].(string); ok {
		cfg.OrgID = strings.TrimSpace(org)
	}

	// 6. Cross-references.
	if issues = CheckCrossReferences(cfg); len(issues) > 0 {
		return nil, fail(issues, data)
	}
	return cfg, nil
}

func checkAPIVersion(data Obj, lines lineIndex) []Issue {
	v, present := data["apiVersion"]
	line := lines.of(Path{"apiVersion"})
	if !present || v == nil {
		return []Issue{{Path: "apiVersion", Message: "missing required field 'apiVersion'", Line: 1, Topic: "config",
			Fix: "add apiVersion: " + APIVersion}}
	}
	s, _ := v.(string)
	for _, ok := range SupportedAPIVersions {
		if s == ok {
			return nil
		}
	}
	return []Issue{{
		Path:    "apiVersion",
		Message: fmt.Sprintf("unsupported apiVersion %v; supported values: %s", v, strings.Join(SupportedAPIVersions, ", ")),
		Line:    line,
		Topic:   "config",
		Fix:     "set apiVersion: " + APIVersion,
	}}
}

// DeclaredOrgID returns the file's organizationId with ${VAR} references expanded, without
// validating the file. It returns "" when the key is absent or cannot be resolved (the
// organization then comes from the profile or environment). A file that cannot be read or is not
// valid YAML is an error.
func DeclaredOrgID(path string, env func(string) string) (string, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if env == nil {
		env = func(string) string { return "" }
	}
	root, issues := parseYAML(string(text), filepath.Base(path))
	if len(issues) > 0 {
		return "", fmt.Errorf("%s: %s", filepath.Base(path), issues[0].Message)
	}
	data, _ := toObj(root).(Obj)
	value, ok := data["organizationId"].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", nil
	}
	expanded, ok := expandRefs(value, env)
	if !ok {
		return "", nil
	}
	return strings.TrimSpace(expanded), nil
}
