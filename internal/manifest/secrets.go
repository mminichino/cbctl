package manifest

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/secrets"
)

// SecretRef is a password field. The manifest only ever holds a reference: ${VAR},
// ${file:PATH} or ${base64:VALUE}. The value exists only after Resolve, and is registered with
// the redactor (the encoded VALUE of a base64 reference too).
//
// Decoding keeps whatever the file says, so a literal is reported by validation with its line
// instead of as an unpositioned JSON error. A SecretRef marshals as its reference text.
type SecretRef struct {
	// Raw is the text as written in the manifest: the reference, never a resolved value.
	Raw string

	ref      secrets.Ref
	problem  string
	value    string
	resolved bool
}

// ParseSecretRef parses text as a password reference. Problem explains why text is not one.
func ParseSecretRef(text string) SecretRef {
	s := SecretRef{Raw: text}
	switch r, ok := secrets.ParseRef(text, true); {
	case text == "":
		s.problem = "password must not be empty"
	case !ok:
		s.problem = "password must be ${VAR}, ${file:PATH} or ${base64:VALUE}, not a literal"
	default:
		s.ref = r
	}
	return s
}

// UnmarshalJSON keeps the reference text. A value that is not a string is kept as a problem.
func (s *SecretRef) UnmarshalJSON(b []byte) error {
	var text string
	if err := json.Unmarshal(b, &text); err != nil {
		*s = SecretRef{problem: "password must be a string reference (${VAR}, ${file:PATH} or ${base64:VALUE})"}
		return nil //nolint:nilerr // reported by validation with the position of the value
	}
	*s = ParseSecretRef(text)
	return nil
}

// MarshalJSON writes the reference text, never a value.
func (s SecretRef) MarshalJSON() ([]byte, error) { return json.Marshal(s.Raw) }

// String returns the reference text; it never contains a resolved value.
func (s SecretRef) String() string { return s.Raw }

// Problem returns why the field is not a valid reference, or "" when it is one.
func (s SecretRef) Problem() string { return s.problem }

// Kind returns how the reference is resolved. It is only meaningful when Problem is empty.
func (s SecretRef) Kind() secrets.RefKind { return s.ref.Kind }

// Resolved reports whether Resolve succeeded.
func (s SecretRef) Resolved() bool { return s.resolved }

// Value returns the resolved secret. It panics before a successful Resolve: reading a secret
// that has not been resolved is a programmer error.
func (s SecretRef) Value() string {
	if !s.resolved {
		panic("manifest: SecretRef.Value called before Resolve")
	}
	return s.value
}

// Resolve reads the value from the environment, a file or the base64 text. File references
// resolve relative to baseDir. The value and, for base64, the encoded text are registered
// for redaction. On failure the returned problem names the cause without the value.
func (s *SecretRef) Resolve(env func(string) string, baseDir string) *secrets.Problem {
	if s.problem != "" {
		return &secrets.Problem{Message: s.problem}
	}
	if env == nil {
		env = os.Getenv
	}
	v, p := s.ref.Resolve(secrets.RefOptions{BaseDir: baseDir, Env: env, AllowBase64: true})
	if p != nil {
		return p
	}
	redact.Register(v)
	if s.ref.Kind == secrets.RefBase64 {
		redact.Register(s.ref.Arg)
	}
	s.value, s.resolved = v, true
	return nil
}

// Resolve resolves every password reference of d. The returned error is a *ValidationError
// (exit code 3); its messages never contain a secret. Call it for every document before the
// first one is applied, so a bad reference in document 2 stops document 1 too.
func (d *Document) Resolve(env func(string) string, baseDir string) error {
	c := &collector{source: d.source, doc: d.Index + 1, ref: d.ref(), lines: d.Lines}
	resolve := func(pointer string, s *SecretRef) {
		if p := s.Resolve(env, baseDir); p != nil {
			c.add(pointer, p.Message, p.Fix)
		}
	}
	if d.Server != nil {
		resolve("/spec/credentials/password", &d.Server.Credentials.Password)
	}
	if d.Capella != nil {
		for i := range d.Capella.Credentials {
			if pw := d.Capella.Credentials[i].Password; pw != nil {
				resolve(ptr("/spec/credentials", i, "password"), pw)
			}
		}
	}
	if len(c.issues) == 0 {
		return nil
	}
	sortIssues(c.issues)
	return &ValidationError{Source: d.source, Issues: c.issues}
}

// ref returns "Kind/name".
func (d *Document) ref() string {
	if d.Envelope.Kind == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s", d.Envelope.Kind, d.Envelope.Metadata.Name)
}
