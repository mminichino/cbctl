// Package yamlio reads and writes YAML the way Kubernetes tooling does: YAML is only a file
// format, and in code every document is JSON. Reading converts YAML to JSON and decodes it with
// encoding/json into structs with json tags (or into generic maps); writing goes the other way.
//
// The API mirrors sigs.k8s.io/yaml (Unmarshal, Marshal, YAMLToJSON, JSONToYAML) but is built on
// go.yaml.in/yaml/v3 instead of a YAML 1.1 parser, so:
//
//   - YAML 1.2 scalars apply: an unquoted off, on, yes or no stays a string.
//   - Errors carry the YAML line and column, also for unknown fields in strict mode.
//   - Mapping key order is kept in both directions.
//   - Duplicate keys are errors, aliases and merge keys are expanded (with a size cap).
//
// The package imports nothing from this module. Editors that must keep comments work on the
// *yaml.Node tree directly (see Document.Node); only values go through JSON.
package yamlio
