# 0.7.0 Feature 1: YAML through JSON (Phase P1, `mm--yaml-json-codec`)

Goal: YAML is only a file format. In code, every document is JSON. YAML is converted to JSON and decoded with `encoding/json` into structs with `json` tags (or into `capellafile.Obj`). Encoding goes the other way: struct → JSON → YAML. This is the `sigs.k8s.io/yaml` pattern.

## 1. Inventory: where YAML is read or written today

All of these use `gopkg.in/yaml.v3 v3.0.1`.

| Location | Reads/Writes | How | Comments/order matter? |
|---|---|---|---|
| `internal/config/capellafile/yamlnodes.go` `parseYAML`, `toObj`, `indexLines` | read `capella.yaml` | `yaml.Node` → generic `Obj`, plus a path→line index. Duplicate keys are found with `doc.Decode(&discard)`. | Lines: yes (every `Issue` has `Line`). |
| `capellafile/load.go` `Load`, `LoadText`, `DeclaredOrgID` | read | `parseYAML` → apiVersion → secrets → JSON Schema (`santhosh-tekuri/jsonschema/v6`, `schema.json`) → defaults → crossref | Lines. |
| `capellafile/editor.go` `Editor` (`OpenEditor`, `Set`, `PutKey`, `replaceNode`, `Save` + `.bak`) | read and write `capella.yaml` for managed edits | Surgical `yaml.Node` edits that keep Head/Line/Foot comments and key order. Anchors and aliases are refused (`AnchorError`). | Yes: `TestEditorRoundTripOfCbctlWrittenFilesIsByteIdentical`, `TestEditorKeepsCommentsAndKeyOrderOfHandEditedFiles`. |
| `capellafile/render.go` `ToNode`, `EncodeNode`, `Dump` | write (sync, init, `cluster create` into the file) | Canonical key order (`orderedKeys`), flow style for small leaves (`FlowKeys`), quoting of ambiguous strings. | Order and style: yes. |
| `internal/importer/render.go` `Render`, `applyMarkers` | write (`capella sync`) | `ToNode` + marker keys turned into comments + a header comment. | Yes. |
| `internal/importer/importer.go` (around lines 404 and 430) | write a comment block | `yaml.Marshal([]any{Obj{...}})` | Order: `name` first. |
| `internal/cli/capella_sync.go` `readOldFile`, new-text check | read | `yaml.Unmarshal` → `map[string]any` → `NormalizeNumbers` | No. |
| `internal/cli/capella_completion.go` `declaredClusterNames` | read | `yaml:"…"` struct tags | No. |
| `internal/cli/capella_doctor.go` `rawString` | read | `map[string]any` | No. |
| `internal/settings/profiles.go` `ProfileStore.Load` and `Save` | read and write `profiles.yaml` | `yaml` and `json` tags on `Profile`; `profilesFile{yaml:"profiles"}` | No (0600, atomic rename). |
| `internal/settings/legacy.go` `LoadLegacyProfiles` | read capellactl `profiles.yaml` | `map[string]map[string]any`; unknown keys become warnings | No. |
| `internal/settings/migrate.go` `deleteLegacyProfiles` | rewrite capellactl `profiles.yaml` | `map[string]any` round trip. **Today this already loses comments and key order** in capellactl's file. | Better if kept. |
| `internal/presets/presets.go` | read embedded `presets.yaml`, `compute.yaml`, `disks.yaml` | `Size.UnmarshalYAML(*yaml.Node)`; `mappingKeys` keeps file order of sizes | Order: yes (wizard lists sizes in file order). |
| Dev only: `internal/docgen/*`, `cmd/cbdev/docs_extract.go` | docs/backlog YAML | `yaml.Unmarshal`/`Marshal` | Out of scope for the struct migration. Only the import path changes. |

The `capellafile` package doc says on purpose that *"the desired state is held as generic maps rather than typed structs: the engine only compares fields that are present in the file, which needs presence semantics."* The plan (`internal/plan`) and all reconcilers (`internal/resources`) work on `Obj`. **This plan keeps `Obj` as the in-memory model for `capella.yaml`** and puts JSON in the middle of the pipeline (YAML → JSON bytes → `Obj` via `json.Decoder.UseNumber`). Converting the engine to typed structs would rewrite about 6k lines of reconcilers. It brings no user value, and the JSON Schema already gives typed validation. New code (manifests, profiles, presets, completion, doctor) uses typed `json`-tagged structs.

## 2. Library choice: Decided (D2)

**Decided 2026-11-17:** `go.yaml.in/yaml/v3`, behind the internal `sigs.k8s.io/yaml`-style wrapper `internal/yamlio`. `sigs.k8s.io/yaml` is not a dependency. The rationale for each library:

| Library | Verdict |
|---|---|
| `sigs.k8s.io/yaml` v1.6.0 (latest; go.mod requires `go.yaml.in/yaml/v2 v2.4.2`, `go.yaml.in/yaml/v3 v3.0.3`) | **Rejected.** The source confirms (`yaml.go` imports `go.yaml.in/yaml/v2`) that `YAMLToJSON` parses with yaml v2, i.e. YAML 1.1. Unquoted `on`/`off`/`yes`/`no`/`y`/`n` become booleans when decoded into `interface{}`, so `weekend: off` would become `false`. capella.yaml relies on these words (see the CAP-020 AC10 comment in the fixtures). It also loses line numbers: unknown-field errors carry no position. It drops all comments. `JSONToYAML` decodes into a map, so output keys come out sorted, not in canonical or struct order. |
| `gopkg.in/yaml.v3` v3.0.1 (current) | Unmaintained: the upstream repo is archived and ownership moved to the YAML org. |
| **`go.yaml.in/yaml/v3` v3.0.6** (latest at the time of writing; re-check before pinning) | **Chosen.** Same API as `gopkg.in/yaml.v3`, so the switch is an import-path change. YAML 1.2 core schema: `off` stays a string in `interface{}`. Keeps `Node` comments and line numbers. It is already the yaml v3 that `sigs.k8s.io/yaml` itself uses. |

Design: a small internal package `internal/yamlio` exposes a **`sigs.k8s.io/yaml`-compatible API** built on `go.yaml.in/yaml/v3`. Call sites read like Kubernetes code. Behaviour is YAML 1.2, with line numbers and with the existing comment-preserving editor. No new third-party dependency (it replaces one). `THIRD_PARTY_NOTICES.md` needs updating: the license is the same (MIT + Apache-2.0), the module path is different.

## 3. File and module structure

```
internal/yamlio/                 (new; imports go.yaml.in/yaml/v3 + stdlib only)
  doc.go                         package doc: YAML is a file format, code is JSON
  convert.go                     YAMLToJSON, JSONToYAML, node <-> JSON value, number normalization
  decode.go                      Unmarshal, UnmarshalStrict, Decoder (multi-document), Options
  encode.go                      Marshal, MarshalOrdered (struct/JSON key order kept)
  lines.go                       LineIndex (JSON pointer -> line/column)
  errors.go                      *SyntaxError, *FieldError (Path, Line, Column, Msg)
  *_test.go, testdata/golden/    goldens: *.yaml -> *.json -> *.yaml
internal/config/capellafile/
  yamlnodes.go                   parseYAML keeps yaml.Node (lines/editor); toObj replaced by
                                 yamlio.NodeToJSON + json.Decoder(UseNumber) -> Obj
  render.go                      ToNode accepts json.RawMessage / ordered JSON; EncodeNode unchanged
  editor.go                      unchanged algorithm; import path swap; Decode(node) via yamlio
internal/settings/profiles.go    json tags only; Load = lenient yamlio.Unmarshal + unknown-key warning (§6)
internal/settings/legacy.go      yamlio.Unmarshal into struct{Profiles map[string]map[string]any `json:"profiles"`}
internal/settings/migrate.go     deleteLegacyProfiles edits the yaml.Node (keeps comments) via yamlio.EditNode
internal/presets/presets.go      json tags; order of sizes from yamlio.MappingKeys
internal/cli/capella_completion.go, capella_doctor.go, capella_sync.go   yamlio.Unmarshal
internal/importer/importer.go    comment blocks via yamlio.MarshalOrdered
go.mod                           - gopkg.in/yaml.v3; + go.yaml.in/yaml/v3 v3.0.6
```

Dependency direction: `yamlio` ← `capellafile`, `settings`, `presets`, `importer`, `manifest` (P3), `cli`. `yamlio` imports nothing internal.

## 4. Interfaces and signatures

```go
package yamlio

// YAMLToJSON converts one YAML document to JSON. Mapping keys must be strings; aliases are
// expanded, merge keys (<<) applied; !!binary and non-string keys are errors.
func YAMLToJSON(y []byte) ([]byte, error)

// JSONToYAML converts JSON to block-style YAML, keeping the JSON object key order.
func JSONToYAML(j []byte) ([]byte, error)

// Unmarshal converts YAML to JSON and decodes it with encoding/json into v (json tags apply).
func Unmarshal(y []byte, v any, opts ...Option) error

// UnmarshalStrict is Unmarshal with unknown fields and duplicate keys rejected. Errors are
// *FieldError/*SyntaxError carrying the YAML line and column.
func UnmarshalStrict(y []byte, v any, opts ...Option) error

// Marshal encodes v with encoding/json and converts to YAML (struct field order is kept).
func Marshal(v any) ([]byte, error)

// Document is one parsed YAML document: the JSON form plus positions.
type Document struct {
	JSON  []byte     // canonical JSON of the document
	Lines LineIndex  // JSON pointer ("/spec/nodes/0/ipAddress") -> position
	Node  *yaml.Node // the parsed node, for editors (nil when not requested)
	Index int        // 0-based position in a multi-document stream
}

// Decoder reads a stream of '---'-separated documents.
type Decoder struct{ /* ... */ }
func NewDecoder(r io.Reader, opts ...Option) *Decoder
func (d *Decoder) Next() (*Document, error) // io.EOF at end; empty documents skipped
func (d *Document) Decode(v any, strict bool) error

type Position struct{ Line, Column int }
type LineIndex map[string]Position
func (l LineIndex) Of(pointer string) Position // nearest ancestor with a position

type Option func(*options)
func WithTimestamps(mode TimestampMode) Option // KeepText (capella.yaml) | RFC3339 (manifest data)
func WithKeepNode() Option                     // keep *yaml.Node in Document (editor)
func WithMaxDocuments(n int) Option            // default 64

type SyntaxError struct{ Source string; Line, Column int; Msg string }
type FieldError  struct{ Source, Pointer string; Line, Column int; Msg string; Suggest string }
func (e *FieldError) Error() string // "capella.yaml:12:5: spec.nodes[0].ipadress: unknown field (did you mean ipAddress?)"
```

How strict decoding finds unknown fields with lines: `encoding/json`'s `DisallowUnknownFields` error has no path. `UnmarshalStrict` therefore first walks the JSON value against the target type's `json` tags with reflection (`knownFields(reflect.Type)`, cached per type; `map` and `json.RawMessage` fields accept anything). This reports every unknown key with its pointer, its line and a Levenshtein suggestion (reusing the `closestKey` logic, which moves out of `capellafile/schema.go` into `yamlio`). It then decodes with `DisallowUnknownFields` as a backstop. A `*json.UnmarshalTypeError` keeps the full dotted `Field` path, which maps to a pointer and a line.

## 5. Data models and compatibility rules

- **Numbers**: integral values become `int64`, others `float64` (same as `NormalizeNumbers` today). Values outside ±2^53 keep their JSON text (`json.Number`), so `12345678901234567890` is never rounded.
- **Timestamps**: `KeepText` for `capella.yaml` keeps today's behaviour (e.g. `expiresAt` is written unquoted and stays the string as typed). `RFC3339` (manifest `import[].data`, as the spec requires) re-formats YAML timestamps with `time.RFC3339Nano`.
- **NaN/Inf**: stay the original text (as `scalarValue` does today). JSON cannot carry them.
- **`${VAR}` / `${file:PATH}`**: these are plain strings, untouched by conversion. Resolution stays in `capellafile/secrets.go`, after conversion, on the JSON value, at password paths only. In-file text that has no YAML significance survives YAML → JSON → YAML unchanged. Golden cases cover `${A}`, `${file:./p w}` and `"${X}"`.
- **`apiVersion`**: both `cbctl/v1` and `capellactl/v1` are still accepted (`SupportedAPIVersions`). `sync` keeps the file's value, and new files use `cbctl/v1` unless `--api-version` says otherwise. The codec is version-blind. A manifest (`kind:` present) is a different document type that also uses `cbctl/v1` (see 02 §8).
- **Struct tags**: every `yaml:"…"` tag is removed and the `json:"…"` tag becomes the only contract. For `settings.Profile` the two tags already agree (`organizationId,omitempty`, …). `profilesFile` gets `json:"profiles"`. `presets.Size` loses `UnmarshalYAML(*yaml.Node)` and gains `UnmarshalJSON`. Lint guard: a `cbdev` check, or a `go vet`-style test (`TestNoYAMLTags`), fails on any `yaml:"` struct tag in `internal/` outside `yamlio`.

### Comment and key-order preservation (managed edits)

- Managed edits (`Editor`) keep operating on the `yaml.Node` tree. The Kubernetes pattern applies to **values**, not to the editing model. New values inserted by `Set`/`PutKey`/`Append` are converted JSON → `yaml.Node` through `ToNode`, so the canonical order and flow style are unchanged. Existing nodes, comments, key order, quoting style and blank lines stay byte-identical. This is required, and the existing editor tests are the gate.
- `Editor.Decode(node)` (read a sub-tree as `Obj`) becomes `yamlio.NodeToJSON` → `json.Decoder.UseNumber` → `Obj`.
- `.bak` files: `Save` copies the previous bytes verbatim. There is no codec involvement and no change.
- `deleteLegacyProfiles` changes from a map round trip, which lost comments, to a node edit (`yamlio.DeleteMappingKeys(node, "profiles", names)`). This fixes a small existing regression for capellactl users.

## 6. Error handling

| Failure | Detected by | Surfaced as | Exit |
|---|---|---|---|
| YAML syntax error (tabs, bad indent) | yaml.v3 parse | `Issue{Line, Message:"invalid YAML: …", Fix}`, same text as today (`yamlIssue`) | 3 |
| Duplicate mapping key | node walk in `YAMLToJSON` (replaces the `doc.Decode(&discard)` trick) | `"… key X already defined at line N"`, fix "remove or rename the duplicate key" | 3 |
| Non-string key, `!!binary` | `YAMLToJSON` | `*FieldError` with line | 3 |
| Unknown field (strict) | reflection walk | `*FieldError` with suggestion. For `capella.yaml`, unknown fields are still reported by the JSON Schema (same messages as today, no change) | 3 |
| Type mismatch | `json.UnmarshalTypeError.Field` → pointer → line | `*FieldError` | 3 |
| `profiles.yaml` unreadable or invalid | `ProfileStore.Load` | `parse profiles PATH: line N: …` (`%w`) | 1, unchanged |
| Unknown key in cbctl's `profiles.yaml` | lenient `Unmarshal` + walk | one `[warning]`. **Not** strict, so a newer cbctl's file still loads in an older one (downgrade safety) | 0 |
| Too many documents / file > 10 MiB | `Decoder` | `SyntaxError` | 3 |

## 7. Edge cases

- Empty file, `---` only, or comments only: the existing "is empty" issue (`capella.yaml`). An empty `profiles.yaml` means no profiles.
- BOM prefix: stripped, as today.
- Anchors and aliases: expanded on read. The editor still refuses them (`AnchorError`). Billion-laughs protection: alias expansion is capped (1e6 nodes) and the error reports the line.
- Windows CRLF: the line index counts lines the same way. Goldens include one CRLF file.
- `on`/`off` unquoted in a hibernation day: stays a string with yaml v3. That is a behaviour improvement over a v2 codec. Existing `normalizeHibernationBooleans` still handles `true`/`false`.
- Keys that are quoted numbers (`"8.0"`) stay strings. Unquoted `version: 8.0` is a number, as today. The schema error keeps the existing hint.
- Concurrency: `yamlio` has no globals except a `sync.Map` type cache. It is race-safe.

## 8. Acceptance criteria (P1)

1. `go.mod` has no `gopkg.in/yaml.v3`. `go.yaml.in/yaml/v3` is pinned. `go mod tidy` is clean. `rg 'gopkg.in/yaml'` finds nothing.
2. No `yaml:"` struct tags outside `internal/yamlio` (enforced by a test).
3. All existing `capellafile`, `importer`, `settings`, `presets`, `cli` port and scenario tests pass unchanged. In particular the editor byte-identity tests, `TestDumpedFixturesLoadBackIdentically`, the sync goldens and the capellactl example fixtures.
4. New goldens in `internal/yamlio/testdata/golden/`. Each `NAME.yaml` has `NAME.json` (expected conversion) and `NAME.roundtrip.yaml` (expected `JSONToYAML(YAMLToJSON(x))`). They cover numbers, big ints, timestamps (both modes), `${…}` refs, unicode, CRLF, anchors and merges, empty values and multi-document streams. They regenerate with `go test ./internal/yamlio -update`.
5. Every file in `capellafile/testdata/valid` gives the same `Obj` before and after the migration (a one-off equivalence test against a frozen JSON snapshot taken on `main`).
6. A strict-decode error names file, line, column, path and suggestion (table test).
7. `configure migrate --remove-legacy` keeps comments in capellactl's `profiles.yaml` (new test).
8. `make check` is green.
