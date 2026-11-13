package capellafile

import (
	"bytes"
	_ "embed" // schema.json
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:embed schema.json
var schemaJSON []byte

const schemaURL = "https://capellactl.dev/schema/capellactl-v1.json"

var (
	schemaOnce sync.Once
	compiled   *jsonschema.Schema
	schemaDoc  map[string]any
	schemaErr  error
)

func loadSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		if err := json.Unmarshal(schemaJSON, &schemaDoc); err != nil {
			schemaErr = fmt.Errorf("parsing embedded schema: %w", err)
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
		if err != nil {
			schemaErr = fmt.Errorf("parsing embedded schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schemaURL, doc); err != nil {
			schemaErr = fmt.Errorf("adding embedded schema: %w", err)
			return
		}
		compiled, schemaErr = c.Compile(schemaURL)
		if schemaErr != nil {
			schemaErr = fmt.Errorf("compiling embedded schema: %w", schemaErr)
		}
	})
	return compiled, schemaErr
}

// Schema returns the embedded JSON Schema as a generic document. The result is shared and must
// be treated as read-only.
func Schema() (map[string]any, error) {
	if _, err := loadSchema(); err != nil {
		return nil, err
	}
	return schemaDoc, nil
}

// SchemaNode returns the schema that describes the value at p, following $ref, properties and
// items. ok is false when the schema has no such field.
func SchemaNode(p Path) (node map[string]any, ok bool) {
	root, err := Schema()
	if err != nil {
		return nil, false
	}
	n := deref(root, root)
	for _, el := range p {
		if n == nil {
			return nil, false
		}
		switch k := el.(type) {
		case string:
			props, _ := n["properties"].(map[string]any)
			next, _ := props[k].(map[string]any)
			n = deref(root, next)
		case int:
			next, _ := n["items"].(map[string]any)
			n = deref(root, next)
		}
	}
	return n, n != nil
}

func deref(root, n map[string]any) map[string]any {
	for n != nil {
		ref, ok := n["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/") {
			return n
		}
		var cur any = root
		for _, part := range strings.Split(ref[2:], "/") {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[part]
		}
		next, _ := cur.(map[string]any)
		n = next
	}
	return nil
}

// validateSchema validates data against the embedded schema. data must already be normalized.
func validateSchema(data Obj, lines lineIndex) ([]Issue, error) {
	sch, err := loadSchema()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encoding document for validation: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("decoding document for validation: %w", err)
	}
	verr := sch.Validate(inst)
	if verr == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return nil, verr
	}
	var leaves []*jsonschema.ValidationError
	collectLeaves(ve, &leaves)

	printer := message.NewPrinter(language.English)
	seen := map[string]bool{}
	var issues []Issue
	for _, leaf := range leaves {
		p := instancePath(data, leaf.InstanceLocation)
		for _, m := range schemaMessages(leaf, p, data, printer) {
			key := FormatPath(m.path) + "\x00" + m.msg
			if seen[key] {
				continue
			}
			seen[key] = true
			topic := "config"
			if strings.Contains(FormatPath(m.path), "hibernation") {
				topic = "hibernation"
			}
			issues = append(issues, Issue{Path: FormatPath(m.path), Message: m.msg, Line: lines.of(m.path), Topic: topic, Fix: m.fix})
		}
	}
	return issues, nil
}

func collectLeaves(e *jsonschema.ValidationError, out *[]*jsonschema.ValidationError) {
	switch e.ErrorKind.(type) {
	case *kind.Group, *kind.Schema, *kind.Reference:
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				collectLeaves(c, out)
			}
			return
		}
	}
	*out = append(*out, e)
}

// instancePath converts the validator's string location into a Path, turning list indexes into
// ints by walking data.
func instancePath(data any, loc []string) Path {
	p := make(Path, 0, len(loc))
	cur := data
	for _, tok := range loc {
		if list, ok := cur.([]any); ok {
			if i, err := strconv.Atoi(tok); err == nil {
				p = append(p, i)
				if i >= 0 && i < len(list) {
					cur = list[i]
				} else {
					cur = nil
				}
				continue
			}
		}
		p = append(p, tok)
		if m, ok := cur.(Obj); ok {
			cur = m[tok]
		} else {
			cur = nil
		}
	}
	return p
}

type schemaMessage struct {
	path Path
	msg  string
	fix  string
}

// schemaMessages rewrites one validation failure into user-facing messages, like capellactl's
// _schema_message: terse, in the file's own vocabulary.
func schemaMessages(e *jsonschema.ValidationError, p Path, data Obj, printer *message.Printer) []schemaMessage {
	node, _ := SchemaNode(p)
	one := func(msg string) []schemaMessage { return []schemaMessage{{path: p, msg: msg}} }
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		out := make([]schemaMessage, 0, len(k.Missing))
		for _, m := range k.Missing {
			sm := schemaMessage{path: p, msg: fmt.Sprintf("missing required field '%s'", m)}
			if len(p) == 0 && m == "apiVersion" {
				sm.fix = "add apiVersion: cbctl/v1"
			}
			out = append(out, sm)
		}
		return out
	case *kind.AdditionalProperties:
		props, _ := node["properties"].(map[string]any)
		allowed := make([]string, 0, len(props))
		for name := range props {
			allowed = append(allowed, name)
		}
		sort.Strings(allowed)
		extras := append([]string(nil), k.Properties...)
		sort.Strings(extras)
		msgs := make([]string, 0, len(extras))
		for _, x := range extras {
			m := fmt.Sprintf("unknown field '%s'", x)
			if close := closestKey(x, allowed); close != "" {
				m += fmt.Sprintf(" (did you mean '%s'?)", close)
			}
			msgs = append(msgs, m)
		}
		path := p
		if len(extras) == 1 {
			path = append(append(Path{}, p...), extras[0])
		}
		return []schemaMessage{{path: path, msg: strings.Join(msgs, "; ")}}
	case *kind.MinLength:
		if k.Want == 1 {
			return one("must not be empty")
		}
		return one(fmt.Sprintf("must be at least %d characters", k.Want))
	case *kind.MaxLength:
		return one(fmt.Sprintf("must be at most %d characters", k.Want))
	case *kind.Minimum, *kind.Maximum:
		lo, hasLo := node["minimum"]
		hi, hasHi := node["maximum"]
		switch {
		case hasLo && hasHi:
			return one(fmt.Sprintf("must be between %s and %s", numText(lo), numText(hi)))
		case hasLo:
			return one(fmt.Sprintf("must be >= %s", numText(lo)))
		case hasHi:
			return one(fmt.Sprintf("must be <= %s", numText(hi)))
		}
		if mn, ok := k.(*kind.Minimum); ok {
			return one("must be >= " + ratText(mn.Want))
		}
		return one("must be <= " + ratText(k.(*kind.Maximum).Want))
	case *kind.Enum:
		if containsString(p, "databaseCredentials") && containsString(p, "privileges") {
			return one("basic credentials support read/write only; advanced credentials are not managed by cbctl")
		}
		want := make([]string, 0, len(k.Want))
		for _, w := range k.Want {
			want = append(want, fmt.Sprint(w))
		}
		return one("must be one of: " + strings.Join(want, ", "))
	case *kind.Type:
		want := strings.Join(k.Want, " or ")
		article := "a"
		if want != "" && strings.ContainsRune("aeiou", rune(want[0])) {
			article = "an"
		}
		return one(fmt.Sprintf("must be %s %s", article, want))
	case *kind.Const:
		return one(fmt.Sprintf("must be %v", k.Want))
	case *kind.MinItems:
		if k.Want == 1 {
			return one("must have at least 1 entry")
		}
		return one(fmt.Sprintf("must have at least %d entries", k.Want))
	case *kind.MaxItems:
		return one(fmt.Sprintf("must have at most %d entries", k.Want))
	case *kind.UniqueItems:
		return one("must not contain duplicates")
	case *kind.Pattern:
		if last, _ := lastKey(p); last == "startTime" {
			return one("must be a time of day as HH:MM (24-hour)")
		}
		return one(fmt.Sprintf("is not in the expected format (pattern %s)", k.Want))
	case *kind.OneOf:
		if last, ok := lastKey(p); ok && containsKey(DayKeys, last) {
			return one("must be 'on', 'off' or an on-window 'HH:MM-HH:MM'")
		}
		if len(p) == 0 {
			if _, both := data["projects"]; both {
				return one("use either projects: or the single-project project:/clusters:, not both")
			}
			return one("missing 'projects' (or the single-project 'project')")
		}
	}
	return one(e.ErrorKind.LocalizedString(printer))
}

func lastKey(p Path) (string, bool) {
	if len(p) == 0 {
		return "", false
	}
	s, ok := p[len(p)-1].(string)
	return s, ok
}

func containsString(p Path, s string) bool {
	for _, el := range p {
		if el == s {
			return true
		}
	}
	return false
}

func containsKey(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func numText(v any) string {
	if f, ok := v.(float64); ok && f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprint(v)
}

func ratText(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	f, _ := r.Float64()
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// closestKey returns the allowed key closest to key within edit distance 2 (case-insensitive),
// or "" when none is close.
func closestKey(key string, allowed []string) string {
	best, bestD := "", 3
	for _, a := range allowed {
		d := levenshtein(strings.ToLower(key), strings.ToLower(a))
		if d < bestD {
			best, bestD = a, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
